package mux

import (
	"fmt"
	"io"
	"sync"
)

// fairScheduler provides per-channel weighted fair queuing for mux data frames.
//
// Problem it solves: the previous single FIFO out-queue allowed a single
// high-bandwidth channel (e.g. a large download) to fill the queue and starve
// all other channels. With the fair scheduler each active channel gets its own
// FIFO, and the writer drains them in strict round-robin order — so 135
// concurrent connections each get an equal share of the output bandwidth.
//
// Backpressure: a token semaphore bounds the total number of queued frames to
// maxFrames (same as the old MUX_OUT_BUF). Callers block in Enqueue until a
// slot is available or stopCh is closed, preserving the existing back-pressure
// semantics.
type fairScheduler struct {
	// tokens is a "pool" semaphore: each token represents a free slot.
	// Enqueue takes a token; Dequeue returns one.
	tokens chan struct{}

	// ready is signalled (non-blocking) whenever a frame is enqueued so the
	// writer goroutine can wake from its select without polling.
	ready chan struct{}

	mu     sync.Mutex
	queues map[uint16][]Frame
	order  []uint16 // channels with queued frames, in insertion order
	pos    int      // next round-robin index into order
	closed bool
}

func newFairScheduler(maxFrames int) *fairScheduler {
	tokens := make(chan struct{}, maxFrames)
	for i := 0; i < maxFrames; i++ {
		tokens <- struct{}{}
	}
	return &fairScheduler{
		tokens: tokens,
		ready:  make(chan struct{}, 1),
		queues: make(map[uint16][]Frame),
	}
}

// Enqueue adds f to the channel's per-channel queue.
// Blocks until a capacity slot is available or stopCh is closed.
// Returns false if the scheduler is closed or stopCh fires.
func (fs *fairScheduler) Enqueue(f Frame, stopCh <-chan struct{}) bool {
	select {
	case <-fs.tokens: // acquire a capacity slot
	case <-stopCh:
		return false
	}

	fs.mu.Lock()
	if fs.closed {
		fs.mu.Unlock()
		fs.tokens <- struct{}{} // return slot on failure
		return false
	}
	fs.addFrame(f)
	fs.mu.Unlock()

	select {
	case fs.ready <- struct{}{}:
	default:
	}
	return true
}

// addFrame appends f to the per-channel queue. Must hold fs.mu.
func (fs *fairScheduler) addFrame(f Frame) {
	if _, exists := fs.queues[f.Channel]; !exists {
		fs.order = append(fs.order, f.Channel)
	}
	fs.queues[f.Channel] = append(fs.queues[f.Channel], f)
}

// Dequeue returns the next frame in round-robin channel order.
// Returns (Frame{}, false) if no frames are queued. Non-blocking.
func (fs *fairScheduler) Dequeue() (Frame, bool) {
	fs.mu.Lock()
	f, ok := fs.nextFrame()
	fs.mu.Unlock()
	if ok {
		fs.tokens <- struct{}{} // release capacity slot
	}
	return f, ok
}

// nextFrame picks the next frame using round-robin ordering. Must hold fs.mu.
func (fs *fairScheduler) nextFrame() (Frame, bool) {
	n := len(fs.order)
	if n == 0 {
		return Frame{}, false
	}
	for i := 0; i < n; i++ {
		idx := (fs.pos + i) % n
		ch := fs.order[idx]
		q := fs.queues[ch]
		if len(q) == 0 {
			// Stale entry — clean up and continue.
			delete(fs.queues, ch)
			fs.order = append(fs.order[:idx], fs.order[idx+1:]...)
			n--
			if n > 0 && fs.pos >= n {
				fs.pos = 0
			}
			continue
		}
		f := q[0]
		q = q[1:]
		if len(q) == 0 {
			delete(fs.queues, ch)
			fs.order = append(fs.order[:idx], fs.order[idx+1:]...)
			n--
			if n > 0 {
				fs.pos = idx % n
			} else {
				fs.pos = 0
			}
		} else {
			fs.queues[ch] = q
			fs.pos = (idx + 1) % n
		}
		return f, true
	}
	return Frame{}, false
}

// ReadyCh returns a channel that is signalled when at least one frame is queued.
// The writer goroutine selects on this alongside stopCh and priorityOut.
func (fs *fairScheduler) ReadyCh() <-chan struct{} {
	return fs.ready
}

// Len returns the total number of queued frames across all channels.
func (fs *fairScheduler) Len() int {
	// Total queued = capacity - available tokens.
	return cap(fs.tokens) - len(fs.tokens)
}

// Close marks the scheduler closed, unblocking any goroutine waiting in Enqueue
// via stopCh (the caller's responsibility). Signals ready so the writer can exit.
func (fs *fairScheduler) Close() {
	fs.mu.Lock()
	fs.closed = true
	fs.mu.Unlock()
	select {
	case fs.ready <- struct{}{}:
	default:
	}
}

// drainAndFlushSched writes all immediately available frames directly to w.
// Priority (control) frames are written before data frames;
// data frames are served in round-robin channel order via sched.
// Mirrors drainAndFlush but uses fairScheduler instead of a raw channel.
func drainAndFlushSched(w io.Writer, priority <-chan Frame, sched *fairScheduler, errCh chan<- error) error {
	for {
		// Priority frames first (PONG, DNS responses, WINDOW_UPDATEs).
		select {
		case f, ok := <-priority:
			if !ok {
				return fmt.Errorf("priority channel closed")
			}
			if err := WriteFrame(w, f); err != nil {
				errCh <- err
				return err
			}
			continue
		default:
		}
		// Next data frame in fair round-robin order.
		if f, ok := sched.Dequeue(); ok {
			if err := WriteFrame(w, f); err != nil {
				errCh <- err
				return err
			}
		} else {
			return nil
		}
	}
}
