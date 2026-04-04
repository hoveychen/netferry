package mux

import (
	"encoding/binary"
	"io"
	"sync"
)

// FairWriter wraps an io.Writer and schedules smux frames using Deficit Round
// Robin (DRR) to prevent a single heavy stream from starving others.
//
// PSH (data) frames are enqueued into per-stream queues and drained in
// round-robin order, each stream receiving up to [quantum] bytes per round.
// All other frame types (SYN, NOP, UPD) are written immediately with minimal
// delay — they are small, latency-sensitive control frames.
//
// FIN frames are special-cased: if the stream has queued PSH frames, the FIN
// is appended to the stream's queue so it is sent after all preceding data.
// If the queue is empty, FIN is written immediately like other control frames.
//
// Priority frames (non-PSH) arriving during a DRR round are flushed between
// each data-frame write, ensuring NOP keepalives are never delayed by more
// than one frame-write time (~65 KB).
type FairWriter struct {
	w       io.Writer
	inputCh chan fairFrame
	done    chan struct{}
	once    sync.Once
	wErr    error
	quantum int
}

type fairFrame struct {
	data     []byte
	streamID uint32
	cmd      byte
}

const (
	// drrQuantum is the number of bytes each stream may send per DRR round.
	// Two max-size smux frames (2 × 65 KB ≈ 128 KB).  This is large enough
	// to amortise per-round overhead while small enough to give responsive
	// interleaving on slow links.
	drrQuantum = 131072

	// fairWriterBufSize is the capacity of the input channel.  It must be
	// large enough to absorb bursts while the drain goroutine is blocked on
	// a TCP write, but small enough to bound memory usage.
	// 64 frames × ~65 KB ≈ 4 MB worst-case.
	fairWriterBufSize = 64
)

// NewFairWriter creates a FairWriter that writes to w using DRR scheduling.
// A background goroutine is started; call Close to stop it.
func NewFairWriter(w io.Writer) *FairWriter {
	fw := &FairWriter{
		w:       w,
		inputCh: make(chan fairFrame, fairWriterBufSize),
		done:    make(chan struct{}),
		quantum: drrQuantum,
	}
	go fw.drain()
	return fw
}

// Write accepts a complete smux frame, enqueues it for fair scheduling, and
// returns.  It blocks only when the input buffer is full (backpressure).
func (fw *FairWriter) Write(b []byte) (int, error) {
	select {
	case <-fw.done:
		if fw.wErr != nil {
			return 0, fw.wErr
		}
		return 0, io.ErrClosedPipe
	default:
	}

	frame := make([]byte, len(b))
	copy(frame, b)

	f := fairFrame{data: frame}
	if len(b) >= smuxHdrLen {
		f.cmd = b[1]
		f.streamID = binary.LittleEndian.Uint32(b[4:8])
	}

	select {
	case fw.inputCh <- f:
		return len(b), nil
	case <-fw.done:
		if fw.wErr != nil {
			return 0, fw.wErr
		}
		return 0, io.ErrClosedPipe
	}
}

// Close stops the background drain goroutine.  It is safe to call multiple
// times.  Close does NOT close the underlying writer.
func (fw *FairWriter) Close() {
	fw.once.Do(func() { close(fw.done) })
}

// ── drain loop ───────────────────────────────────────────────────────────────

type streamQueue struct {
	frames  [][]byte
	deficit int
}

func (fw *FairWriter) drain() {
	queues := make(map[uint32]*streamQueue)
	var active []uint32
	activeSet := make(map[uint32]bool)
	var priority [][]byte

	for {
		// If nothing pending, block until a frame arrives.
		if len(active) == 0 && len(priority) == 0 {
			select {
			case f := <-fw.inputCh:
				fw.classify(f, queues, &active, activeSet, &priority)
			case <-fw.done:
				return
			}
		}

		// Collect all additional frames that have arrived (non-blocking).
		fw.collectPending(queues, &active, activeSet, &priority)

		// Flush priority frames immediately.
		if !fw.flushSlice(&priority) {
			return
		}

		// DRR round over active data streams.
		if len(active) == 0 {
			continue
		}

		// Use a fresh slice to avoid aliasing bugs: collectPending may
		// append new stream IDs during the round, and remaining shares no
		// backing array with active.
		remaining := make([]uint32, 0, len(active))

		for _, sid := range active {
			q := queues[sid]
			q.deficit += fw.quantum

			for len(q.frames) > 0 {
				fsize := len(q.frames[0])
				if q.deficit < fsize {
					break
				}
				frame := q.frames[0]
				q.frames[0] = nil // help GC
				q.frames = q.frames[1:]
				q.deficit -= fsize

				// Between data writes, collect and flush any newly arrived
				// priority frames so NOP/UPD keepalives are never delayed by
				// more than one data-frame write.
				fw.collectPending(queues, &remaining, activeSet, &priority)
				if !fw.flushSlice(&priority) {
					return
				}
				if !fw.writeFrame(frame) {
					return
				}
			}

			if len(q.frames) > 0 {
				remaining = append(remaining, sid)
			} else {
				q.deficit = 0
				delete(queues, sid)
				delete(activeSet, sid)
			}
		}
		active = remaining
	}
}

// classify routes a frame to either the priority bypass list or a per-stream
// DRR queue.
func (fw *FairWriter) classify(f fairFrame, queues map[uint32]*streamQueue, active *[]uint32, activeSet map[uint32]bool, priority *[][]byte) {
	switch f.cmd {
	case smuxCmdPSH:
		q := queues[f.streamID]
		if q == nil {
			q = &streamQueue{}
			queues[f.streamID] = q
		}
		q.frames = append(q.frames, f.data)
		if !activeSet[f.streamID] {
			*active = append(*active, f.streamID)
			activeSet[f.streamID] = true
		}

	case smuxCmdFIN:
		// Preserve per-stream ordering: if PSH frames are queued for this
		// stream, append FIN after them.  Otherwise write immediately.
		if q, ok := queues[f.streamID]; ok && len(q.frames) > 0 {
			q.frames = append(q.frames, f.data)
		} else {
			*priority = append(*priority, f.data)
		}

	default:
		// SYN, NOP, UPD — bypass the scheduler.
		*priority = append(*priority, f.data)
	}
}

// collectPending drains all currently available frames from inputCh without
// blocking.
func (fw *FairWriter) collectPending(queues map[uint32]*streamQueue, active *[]uint32, activeSet map[uint32]bool, priority *[][]byte) {
	for {
		select {
		case f := <-fw.inputCh:
			fw.classify(f, queues, active, activeSet, priority)
		default:
			return
		}
	}
}

// flushSlice writes all frames in the slice and resets it.
func (fw *FairWriter) flushSlice(frames *[][]byte) bool {
	for _, frame := range *frames {
		if !fw.writeFrame(frame) {
			return false
		}
	}
	*frames = (*frames)[:0]
	return true
}

// writeFrame writes a single frame to the underlying writer.
// Returns false on error (the drain loop should exit).
func (fw *FairWriter) writeFrame(frame []byte) bool {
	if _, err := fw.w.Write(frame); err != nil {
		fw.wErr = err
		fw.once.Do(func() { close(fw.done) })
		return false
	}
	return true
}
