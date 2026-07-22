package mux

import (
	"encoding/binary"
	"io"
	"log"
	"sync"
)

// smux v2 frame cmd constants (byte 1 of the 8-byte frame header).
const (
	smuxCmdSYN byte = 0
	smuxCmdFIN byte = 1
	smuxCmdPSH byte = 2
	smuxCmdNOP byte = 3
	smuxCmdUPD byte = 4
	smuxHdrLen      = 8 // fixed header size for both v1 and v2
)

// isDataCmd reports whether the smux command is a stream-data command
// (as opposed to session-level NOP/UPD).
//
// Note: although SYN is classified as a data command by this function,
// splitWriter sends every regular SYN through the ctrl channel for low latency
// AND mirrors it onto the data channel for ordering (see Write).  PSH and FIN
// continue to travel on the data channel (unless the stream is explicitly
// registered for full ctrl routing, e.g. DNS, in which case all of its frames
// travel on ctrl).
func isDataCmd(cmd byte) bool {
	return cmd == smuxCmdSYN || cmd == smuxCmdPSH || cmd == smuxCmdFIN
}

// streamID extracts the little-endian stream ID from a smux frame header.
func streamID(frame []byte) uint32 {
	return binary.LittleEndian.Uint32(frame[4:8])
}

// SplitConn presents a single io.ReadWriteCloser to smux while routing frames
// over two physically separate connections:
//
//   - data connection: PSH and FIN frames (bulk stream data)
//   - ctrl connection: SYN, NOP, UPD frames, plus selected low-latency streams
//
// Certain streams (e.g. DNS) can be routed via the ctrl connection for lower
// latency.  The client side pre-registers a stream via routeNextSYN before
// calling OpenStream; the server side auto-learns by observing data-cmd frames
// arriving on the ctrl connection.
//
// Data frames are written asynchronously: Write() copies the frame into a
// buffered channel and returns immediately, while a background goroutine
// drains the channel into the data TCP connection.  This prevents a blocked
// data TCP write from stalling smux's single-threaded write loop, which would
// delay NOP/UPD frames and trigger keepalive timeouts.
//
// Ctrl frames (including ctrl-routed stream frames) are written synchronously
// — they are small and the ctrl TCP is never congested.
//
// Assumption: smux always issues one Write call per complete frame
// (header + payload in a single buffer).  This holds because smux only uses
// scatter-gather I/O (WriteBuffers) when the underlying conn implements that
// interface; SplitConn does not, so smux always takes the combined-buffer path.
type SplitConn struct {
	mr          *mergedReader
	sw          *splitWriter
	ctrlStreams sync.Map // uint32 → struct{}: stream IDs routed via ctrl

	// openMu serializes OpenStream calls so that routeNextSYN is consumed by
	// the correct SYN frame.  Only the client side uses this.
	openMu       sync.Mutex
	routeNextSYN bool
}

// NewSplitConn creates a SplitConn backed by two independent read/write pairs.
// dataR/dataW carry SYN+PSH+FIN frames; ctrlR/ctrlW carry NOP+UPD frames.
func NewSplitConn(dataR io.Reader, dataW io.Writer, ctrlR io.Reader, ctrlW io.Writer) *SplitConn {
	sc := &SplitConn{}
	sc.mr = newMergedReader(dataR, ctrlR, &sc.ctrlStreams)
	sc.sw = newSplitWriter(dataW, ctrlW, sc)
	return sc
}

func (s *SplitConn) Read(b []byte) (int, error)  { return s.mr.Read(b) }
func (s *SplitConn) Write(b []byte) (int, error) { return s.sw.Write(b) }
func (s *SplitConn) Close() error {
	s.mr.close()
	s.sw.close()
	return nil
}

// ── splitWriter ───────────────────────────────────────────────────────────────

// splitWriter routes smux frames to two connections.  Data frames (SYN mirror,
// PSH, FIN) are appended to an in-memory elastic queue and written by a
// background goroutine so that a blocked data TCP never stalls smux's single
// write loop.  Ctrl frames (NOP, UPD, and ctrl-routed streams) are written
// synchronously.
//
// Why an elastic queue rather than a bounded channel: smux drives Write from
// ONE write loop.  If a data-frame enqueue blocks (bounded channel full while
// the data TCP is stalled by cross-border bufferbloat), that loop stalls — every
// stream on the connection freezes AND ctrl frames (NOP keepalive, UPD window
// updates) queue behind the blocked data frame.  That head-of-line blocking is
// exactly the "connection is up but nothing loads" symptom.  Keeping every
// enqueue non-blocking lets the write loop keep moving so ctrl frames always
// flow.  The queue does not grow without bound: smux's own per-stream and
// per-session flow control caps total in-flight data, so a persistently stalled
// data connection backpressures the individual stalled streams (their Write
// blocks on the smux window), not the shared write loop.
type splitWriter struct {
	data io.Writer
	ctrl io.Writer
	sc   *SplitConn // back-pointer for ctrlStreams & routeNextSYN

	mu  sync.Mutex
	syn [][]byte // SYN mirrors — highest priority (ordering)
	psh [][]byte // bulk PSH data frames
	fin [][]byte // FIN — drained only after all pending PSH (PSH→FIN ordering)

	signal chan struct{} // cap 1: wakes drainData when a data frame is queued
	done   chan struct{} // closed on close() or fatal data-write error
	once   sync.Once
	wErr   error // first data-write error, readable after done closes
}

func newSplitWriter(data io.Writer, ctrl io.Writer, sc *SplitConn) *splitWriter {
	sw := &splitWriter{
		data:   data,
		ctrl:   ctrl,
		sc:     sc,
		signal: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	go sw.drainData()
	return sw
}

// wake signals drainData that new work is queued (non-blocking; the cap-1
// signal channel coalesces bursts — one wake drains everything pending).
func (sw *splitWriter) wake() {
	select {
	case sw.signal <- struct{}{}:
	default:
	}
}

// enqueueData appends a copy of frame to the given queue without blocking and
// wakes the drain goroutine.  Returns false if the writer has been closed.
func (sw *splitWriter) enqueueData(q *[][]byte, frame []byte) bool {
	buf := make([]byte, len(frame))
	copy(buf, frame)
	sw.mu.Lock()
	select {
	case <-sw.done:
		sw.mu.Unlock()
		return false
	default:
	}
	*q = append(*q, buf)
	sw.mu.Unlock()
	sw.wake()
	return true
}

// popData removes and returns the next data frame in priority order
// (SYN > PSH > FIN), or (nil, false) when all queues are empty.  FIN is drained
// only when no PSH remain, which preserves per-stream PSH→FIN ordering: by the
// time smux writes FIN(X) all PSH(X) are already queued, so they are written
// first.  SYN mirrors precede PSH for the same reason (smux writes SYN(X)
// before PSH(X), and SYN has the highest drain priority).
func (sw *splitWriter) popData() ([]byte, bool) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	switch {
	case len(sw.syn) > 0:
		f := sw.syn[0]
		sw.syn[0] = nil
		sw.syn = sw.syn[1:]
		return f, true
	case len(sw.psh) > 0:
		f := sw.psh[0]
		sw.psh[0] = nil
		sw.psh = sw.psh[1:]
		return f, true
	case len(sw.fin) > 0:
		f := sw.fin[0]
		sw.fin[0] = nil
		sw.fin = sw.fin[1:]
		return f, true
	default:
		return nil, false
	}
}

// drainData writes queued data frames to the data TCP connection in priority
// order.  It blocks (on the signal channel) only when the queue is empty, never
// while frames are pending; a slow data.Write applies no backpressure to the
// enqueue side — the queue simply grows until smux flow control throttles the
// producing streams.
func (sw *splitWriter) drainData() {
	for {
		select {
		case <-sw.done:
			return
		default:
		}
		frame, ok := sw.popData()
		if !ok {
			select {
			case <-sw.signal:
			case <-sw.done:
				return
			}
			continue
		}
		if !sw.writeDataFrame(frame) {
			return
		}
	}
}

// writeDataFrame writes a single frame to the data TCP connection.
// Returns false and records the error if the write fails.
func (sw *splitWriter) writeDataFrame(frame []byte) bool {
	if _, err := sw.data.Write(frame); err != nil {
		log.Printf("mux: split-conn data writer: %v", err)
		sw.wErr = err
		sw.once.Do(func() { close(sw.done) })
		return false
	}
	return true
}

func (sw *splitWriter) close() {
	sw.once.Do(func() { close(sw.done) })
}

// closedErr reports the terminal write error (or a generic closed-pipe error)
// after the writer's done channel has been observed closed.
func (sw *splitWriter) closedErr() (int, error) {
	if sw.wErr != nil {
		return 0, sw.wErr
	}
	return 0, io.ErrClosedPipe
}

// Write routes a complete smux frame to either the data or ctrl channel.
//
// Data frames (SYN mirror, PSH, FIN) are appended to the elastic queue and
// return immediately.  Ctrl frames are written synchronously to the ctrl TCP.
func (sw *splitWriter) Write(b []byte) (int, error) {
	// Check for a previous async data-write error.
	select {
	case <-sw.done:
		if sw.wErr != nil {
			return 0, sw.wErr
		}
		return 0, io.ErrClosedPipe
	default:
	}

	if len(b) < smuxHdrLen {
		return sw.ctrl.Write(b)
	}

	if isDataCmd(b[1]) {
		sid := streamID(b)

		// SYN frames travel via ctrl for low latency — they are header-only
		// (8 bytes) and must not queue behind bulk PSH frames, otherwise a
		// single congested download can starve new stream creation for tens of
		// seconds.
		//
		// For a regular stream we ALSO mirror the SYN onto the data channel
		// (top-priority syn queue).  The ctrl and data channels are independent
		// TCP connections: a lossy/reordering link can deliver the data PSH
		// before the ctrl SYN, and smux silently drops data for a not-yet-open
		// stream.  Mirroring the SYN onto the (ordered) data connection ahead
		// of the stream's PSH guarantees the receiver opens the stream first.
		// The mirror is idempotent — smux ignores a duplicate SYN.
		//
		// If routeNextSYN is set, the stream is registered for full ctrl
		// routing (e.g. DNS): SYN+PSH+FIN all travel on ctrl (a single ordered
		// connection), so no data mirror is needed and none is sent.
		if b[1] == smuxCmdSYN {
			if sw.sc.routeNextSYN {
				sw.sc.routeNextSYN = false
				sw.sc.ctrlStreams.Store(sid, struct{}{})
				return sw.ctrl.Write(b)
			}
			// Mirror onto the data channel (top priority) before returning via
			// ctrl.  The enqueue is non-blocking.
			if !sw.enqueueData(&sw.syn, b) {
				return sw.closedErr()
			}
			return sw.ctrl.Write(b)
		}

		// Check if this stream was registered for full ctrl routing.
		if _, ok := sw.sc.ctrlStreams.Load(sid); ok {
			if b[1] == smuxCmdFIN {
				sw.sc.ctrlStreams.Delete(sid)
			}
			return sw.ctrl.Write(b)
		}

		// FIN: separate queue so it doesn't queue behind bulk PSH frames from
		// other streams; drainData writes it only after all pending PSH, so a
		// stream's PSH still precede its FIN on the wire.
		if b[1] == smuxCmdFIN {
			if !sw.enqueueData(&sw.fin, b) {
				return sw.closedErr()
			}
			return len(b), nil
		}

		// PSH: elastic data queue.
		if !sw.enqueueData(&sw.psh, b) {
			return sw.closedErr()
		}
		return len(b), nil
	}

	return sw.ctrl.Write(b)
}

// ── mergedReader ──────────────────────────────────────────────────────────────

// mergedReader combines two byte-stream sources into one by reading complete
// smux frames from each and forwarding them in arrival order.
type mergedReader struct {
	ch          chan []byte
	buf         []byte
	done        chan struct{}
	once        sync.Once
	ctrlStreams *sync.Map // shared with SplitConn for auto-learning
}

func newMergedReader(data io.Reader, ctrl io.Reader, ctrlStreams *sync.Map) *mergedReader {
	mr := &mergedReader{
		ch:          make(chan []byte, 128),
		done:        make(chan struct{}),
		ctrlStreams: ctrlStreams,
	}
	go mr.pump(data, "data")
	go mr.pump(ctrl, "ctrl")
	return mr
}

// pump reads complete smux frames from r and forwards them to ch.
// Any read error (including io.EOF) closes the done channel, unblocking Read.
func (mr *mergedReader) pump(r io.Reader, label string) {
	hdr := make([]byte, smuxHdrLen)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			log.Printf("mux: split-conn %s pump closed: %v", label, err)
			mr.close()
			return
		}
		// smux uses little-endian for the length field (bytes [2:4]).
		size := binary.LittleEndian.Uint16(hdr[2:4])
		frame := make([]byte, smuxHdrLen+int(size))
		copy(frame, hdr)
		if size > 0 {
			if _, err := io.ReadFull(r, frame[smuxHdrLen:]); err != nil {
				log.Printf("mux: split-conn %s pump payload read: %v", label, err)
				mr.close()
				return
			}
		}
		// Auto-learn: if a non-SYN data-cmd frame arrives on ctrl, the
		// remote side explicitly routed this stream for full ctrl transport.
		// SYN is excluded because ALL SYN frames travel via ctrl for low
		// latency; that alone does not mean the stream is fully ctrl-routed.
		if label == "ctrl" && isDataCmd(frame[1]) && frame[1] != smuxCmdSYN {
			sid := streamID(frame)
			if frame[1] == smuxCmdFIN {
				mr.ctrlStreams.Delete(sid)
			} else {
				mr.ctrlStreams.Store(sid, struct{}{})
			}
		}
		select {
		case mr.ch <- frame:
		case <-mr.done:
			return
		}
	}
}

func (mr *mergedReader) close() {
	mr.once.Do(func() { close(mr.done) })
}

// Read satisfies io.Reader, serving bytes from merged frames in arrival order.
func (mr *mergedReader) Read(b []byte) (int, error) {
	if len(mr.buf) > 0 {
		n := copy(b, mr.buf)
		mr.buf = mr.buf[n:]
		return n, nil
	}
	select {
	case frame := <-mr.ch:
		n := copy(b, frame)
		if n < len(frame) {
			mr.buf = frame[n:]
		}
		return n, nil
	case <-mr.done:
		// Drain one last frame that may have arrived concurrently.
		select {
		case frame := <-mr.ch:
			n := copy(b, frame)
			if n < len(frame) {
				mr.buf = frame[n:]
			}
			return n, nil
		default:
			return 0, io.EOF
		}
	}
}
