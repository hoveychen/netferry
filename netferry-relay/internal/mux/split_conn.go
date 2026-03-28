package mux

import (
	"encoding/binary"
	"io"
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

// isDataCmd reports whether the smux command belongs on the data channel.
//
// SYN, PSH, and FIN must all travel on the same connection to preserve
// ordering: SYN opens a stream before PSH carries its payload, and FIN
// signals EOF after the last PSH.  Putting SYN on a faster ctrl connection
// would be safe in one direction, but a slower ctrl connection would let PSH
// arrive before SYN — causing the remote smux to reject the frame for a
// nonexistent stream.
//
// The ctrl channel carries only NOP (keepalive) and UPD (v2 window update).
// These are the frames whose latency matters most — UPD unblocks the remote
// sender, and NOP detects dead connections — and they have no ordering
// dependency on data frames.
func isDataCmd(cmd byte) bool {
	return cmd == smuxCmdSYN || cmd == smuxCmdPSH || cmd == smuxCmdFIN
}

// SplitConn presents a single io.ReadWriteCloser to smux while routing frames
// over two physically separate connections:
//
//   - data connection: PSH and FIN frames
//   - ctrl connection: SYN, NOP, and UPD frames
//
// This prevents large bulk writes (PSH) from delaying UPD window-update frames
// in the OS send buffer, which is the primary cause of throughput collapse under
// simultaneous upload and download.
//
// Assumption: smux always issues one Write call per complete frame
// (header + payload in a single buffer).  This holds because smux only uses
// scatter-gather I/O (WriteBuffers) when the underlying conn implements that
// interface; SplitConn does not, so smux always takes the combined-buffer path.
type SplitConn struct {
	mr *mergedReader
	sw *splitWriter
}

// NewSplitConn creates a SplitConn backed by two independent read/write pairs.
// dataR/dataW carry PSH+FIN frames; ctrlR/ctrlW carry SYN+NOP+UPD frames.
func NewSplitConn(dataR io.Reader, dataW io.Writer, ctrlR io.Reader, ctrlW io.Writer) *SplitConn {
	return &SplitConn{
		mr: newMergedReader(dataR, ctrlR),
		sw: &splitWriter{data: dataW, ctrl: ctrlW},
	}
}

func (s *SplitConn) Read(b []byte) (int, error)  { return s.mr.Read(b) }
func (s *SplitConn) Write(b []byte) (int, error) { return s.sw.Write(b) }
func (s *SplitConn) Close() error {
	s.mr.close()
	return nil
}

// ── splitWriter ───────────────────────────────────────────────────────────────

type splitWriter struct {
	data io.Writer
	ctrl io.Writer
}

// Write routes a complete smux frame to either the data or ctrl channel.
// b[1] is the cmd byte; routing is decided entirely from that byte.
func (sw *splitWriter) Write(b []byte) (int, error) {
	if len(b) < smuxHdrLen {
		// Shouldn't happen with well-behaved smux, but route to ctrl as a
		// safe fallback (small control-like fragment).
		return sw.ctrl.Write(b)
	}
	if isDataCmd(b[1]) {
		return sw.data.Write(b)
	}
	return sw.ctrl.Write(b)
}

// ── mergedReader ──────────────────────────────────────────────────────────────

// mergedReader combines two byte-stream sources into one by reading complete
// smux frames from each and forwarding them in arrival order.
type mergedReader struct {
	ch   chan []byte
	buf  []byte
	done chan struct{}
	once sync.Once
}

func newMergedReader(data io.Reader, ctrl io.Reader) *mergedReader {
	mr := &mergedReader{
		ch:   make(chan []byte, 128),
		done: make(chan struct{}),
	}
	go mr.pump(data)
	go mr.pump(ctrl)
	return mr
}

// pump reads complete smux frames from r and forwards them to ch.
// Any read error (including io.EOF) closes the done channel, unblocking Read.
func (mr *mergedReader) pump(r io.Reader) {
	hdr := make([]byte, smuxHdrLen)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			mr.close()
			return
		}
		// smux uses little-endian for the length field (bytes [2:4]).
		size := binary.LittleEndian.Uint16(hdr[2:4])
		frame := make([]byte, smuxHdrLen+int(size))
		copy(frame, hdr)
		if size > 0 {
			if _, err := io.ReadFull(r, frame[smuxHdrLen:]); err != nil {
				mr.close()
				return
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
