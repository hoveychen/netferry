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
// splitWriter fast-paths ALL SYN frames through the ctrl channel for low
// latency.  This is ordering-safe because ctrl is at least as fast as data,
// so SYN always arrives before any PSH for the same stream.  PSH and FIN
// continue to travel on the data channel (unless the stream is explicitly
// registered for full ctrl routing, e.g. DNS).
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

// splitWriter routes smux frames to two connections.  Data frames (SYN, PSH,
// FIN) are queued into a buffered channel and written by a background goroutine
// so that a blocked data TCP never stalls the smux write loop.  Ctrl frames
// (NOP, UPD) are written synchronously.
type splitWriter struct {
	data   io.Writer
	ctrl   io.Writer
	sc     *SplitConn    // back-pointer for ctrlStreams & routeNextSYN
	dataCh chan []byte    // async queue for data frames
	done   chan struct{}  // closed on fatal data-write error
	once   sync.Once
	wErr   error         // first data-write error, readable after done closes
}

// dataChSize is the capacity of the async data-frame queue.
//
// Keep this SMALL (2–4 frames).  A large buffer lets one heavy download stream
// dump many frames into the channel before other streams get a turn, starving
// lighter streams.  With a small buffer, smux's priority-based shaper controls
// inter-stream fairness, and the maximum NOP delay is bounded by one frame
// drain time (e.g. 64 KB @ 256 Kbps ≈ 2 s), well within the 30 s keepalive
// timeout.
const dataChSize = 4

func newSplitWriter(data io.Writer, ctrl io.Writer, sc *SplitConn) *splitWriter {
	sw := &splitWriter{
		data:   data,
		ctrl:   ctrl,
		sc:     sc,
		dataCh: make(chan []byte, dataChSize),
		done:   make(chan struct{}),
	}
	go sw.drainData()
	return sw
}

// drainData writes queued data frames to the data TCP connection.
// If a write fails, it records the error and signals via done.
func (sw *splitWriter) drainData() {
	for {
		select {
		case frame := <-sw.dataCh:
			if _, err := sw.data.Write(frame); err != nil {
				log.Printf("mux: split-conn data writer: %v", err)
				sw.wErr = err
				sw.once.Do(func() { close(sw.done) })
				return
			}
		case <-sw.done:
			return
		}
	}
}

func (sw *splitWriter) close() {
	sw.once.Do(func() { close(sw.done) })
}

// Write routes a complete smux frame to either the data or ctrl channel.
//
// Data frames are copied into the async queue and return immediately.
// Ctrl frames are written synchronously to the ctrl TCP.
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

		// SYN frames always travel via ctrl for low latency.  They are
		// header-only (8 bytes) and must not queue behind bulk PSH frames
		// in dataCh — otherwise a single congested download can starve new
		// stream creation for tens of seconds.
		//
		// Ordering is safe: ctrl is at least as fast as data, so SYN
		// always arrives before any PSH for the same stream.
		//
		// If routeNextSYN is set, the stream is additionally registered
		// for full ctrl routing (e.g. DNS) so that subsequent PSH/FIN
		// also bypass the data channel.
		if b[1] == smuxCmdSYN {
			if sw.sc.routeNextSYN {
				sw.sc.routeNextSYN = false
				sw.sc.ctrlStreams.Store(sid, struct{}{})
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

		// Copy: smux reuses its write buffer across iterations.
		frame := make([]byte, len(b))
		copy(frame, b)
		select {
		case sw.dataCh <- frame:
			return len(b), nil
		case <-sw.done:
			if sw.wErr != nil {
				return 0, sw.wErr
			}
			return 0, io.ErrClosedPipe
		}
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
