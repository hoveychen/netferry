package mux

import (
	"encoding/binary"
	"testing"
	"time"
)

// chanWriter is an io.Writer backed by a channel.  Each Write blocks until
// someone receives from ch, making it easy to simulate a congested connection.
type chanWriter struct {
	ch chan []byte
}

func (cw *chanWriter) Write(b []byte) (int, error) {
	frame := make([]byte, len(b))
	copy(frame, b)
	cw.ch <- frame
	return len(b), nil
}

// buildFrame constructs a minimal smux v2 frame.
func buildFrame(cmd byte, sid uint32, payload []byte) []byte {
	frame := make([]byte, smuxHdrLen+len(payload))
	frame[0] = 2 // smux v2
	frame[1] = cmd
	binary.LittleEndian.PutUint16(frame[2:4], uint16(len(payload)))
	binary.LittleEndian.PutUint32(frame[4:8], sid)
	copy(frame[smuxHdrLen:], payload)
	return frame
}

// TestSplitWriterSYNBypassesCongestedDataCh verifies that SYN frames are
// written to the ctrl channel even when the data connection is stalled.
//
// Setup: dataW blocks every write (unbuffered channel), so drainData blocks on
// the first frame.  Then we write a SYN frame and verify it completes instantly
// via ctrl regardless of the stalled data path.
func TestSplitWriterSYNBypassesCongestedDataCh(t *testing.T) {
	// dataW: unbuffered → every Write blocks until someone receives.
	dataW := &chanWriter{ch: make(chan []byte)}
	// ctrlW: buffered → Write returns immediately.
	ctrlW := &chanWriter{ch: make(chan []byte, 100)}

	sc := &SplitConn{}
	sw := newSplitWriter(dataW, ctrlW, sc)
	defer sw.close()

	// Step 1: write one PSH frame. drainData picks it up and blocks trying to
	// write to dataW (unbuffered channel, nobody reads) — the data conn stall.
	sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
	time.Sleep(50 * time.Millisecond) // let drainData pick it up

	// Step 2: queue several more PSH frames.  With the elastic queue these are
	// appended without blocking while drainData stays stuck on the first frame.
	for i := 0; i < 8; i++ {
		sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
	}

	// Step 3: enqueuing PSH must NOT block the caller even while the data
	// connection is stalled — that non-blocking property is what keeps smux's
	// single write loop (and thus ctrl NOP/UPD frames) moving.
	pshDone := make(chan struct{})
	go func() {
		sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
		close(pshDone)
	}()
	select {
	case <-pshDone:
		// Good — PSH enqueue returns promptly despite the stalled data conn.
	case <-time.After(2 * time.Second):
		t.Fatal("PSH enqueue blocked on a stalled data connection (head-of-line blocking)")
	}

	// Step 4: write a SYN frame. It goes to ctrl and returns immediately,
	// unaffected by the stalled data path.
	synDone := make(chan struct{})
	go func() {
		sw.Write(buildFrame(smuxCmdSYN, 99, nil))
		close(synDone)
	}()
	select {
	case <-synDone:
		t.Log("SYN bypassed congested dataCh via ctrl")
	case <-time.After(2 * time.Second):
		t.Fatal("SYN blocked by full dataCh — not routed to ctrl")
	}

	// Step 5: verify the SYN frame arrived on ctrlW, not dataW.
	select {
	case frame := <-ctrlW.ch:
		if frame[1] != smuxCmdSYN {
			t.Fatalf("ctrl received cmd %d, want SYN (%d)", frame[1], smuxCmdSYN)
		}
		if sid := binary.LittleEndian.Uint32(frame[4:8]); sid != 99 {
			t.Fatalf("ctrl SYN stream ID = %d, want 99", sid)
		}
	default:
		t.Fatal("SYN frame not found on ctrl channel")
	}
}

// TestSplitWriterDNSFullCtrlRouting verifies that streams registered via
// routeNextSYN have ALL their frames (SYN, PSH, FIN) written to ctrl,
// even when the data channel is congested.
func TestSplitWriterDNSFullCtrlRouting(t *testing.T) {
	dataW := &chanWriter{ch: make(chan []byte)}
	ctrlW := &chanWriter{ch: make(chan []byte, 100)}

	sc := &SplitConn{}
	sw := newSplitWriter(dataW, ctrlW, sc)
	defer sw.close()

	// Stall the data connection and queue a backlog behind the first frame.
	sw.Write(buildFrame(smuxCmdPSH, 1, []byte("bulk")))
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 8; i++ {
		sw.Write(buildFrame(smuxCmdPSH, 1, []byte("bulk")))
	}

	dnsSID := uint32(42)

	// Register the next SYN for full ctrl routing (DNS pattern).
	sc.routeNextSYN = true

	// SYN → ctrl (fast).
	sw.Write(buildFrame(smuxCmdSYN, dnsSID, nil))
	if !sc.routeNextSYN == true {
		// routeNextSYN should be consumed.
	}
	select {
	case f := <-ctrlW.ch:
		if f[1] != smuxCmdSYN {
			t.Fatalf("expected SYN on ctrl, got cmd %d", f[1])
		}
	default:
		t.Fatal("SYN not on ctrl")
	}

	// PSH → ctrl (because stream is registered for full ctrl routing).
	pshDone := make(chan struct{})
	go func() {
		sw.Write(buildFrame(smuxCmdPSH, dnsSID, []byte("dns query")))
		close(pshDone)
	}()
	select {
	case <-pshDone:
		// Good — PSH for DNS stream bypassed dataCh.
	case <-time.After(2 * time.Second):
		t.Fatal("DNS PSH blocked — not routed to ctrl")
	}
	select {
	case f := <-ctrlW.ch:
		if f[1] != smuxCmdPSH {
			t.Fatalf("expected PSH on ctrl, got cmd %d", f[1])
		}
	default:
		t.Fatal("DNS PSH not on ctrl")
	}

	// FIN → ctrl, and stream should be de-registered.
	finDone := make(chan struct{})
	go func() {
		sw.Write(buildFrame(smuxCmdFIN, dnsSID, nil))
		close(finDone)
	}()
	select {
	case <-finDone:
	case <-time.After(2 * time.Second):
		t.Fatal("DNS FIN blocked")
	}
	if _, ok := sc.ctrlStreams.Load(dnsSID); ok {
		t.Fatal("stream should be de-registered after FIN")
	}
}

// TestSplitWriterRegularSYNMirroredToData verifies the fix for cross-connection
// SYN/PSH reordering: a regular (non-DNS) stream's SYN is written to BOTH the
// ctrl channel (low latency) AND the data channel (ordering).  Without the data
// mirror, a lossy/reordering link can deliver the data-channel PSH before the
// ctrl-channel SYN, and smux silently drops the PSH (data loss).
func TestSplitWriterRegularSYNMirroredToData(t *testing.T) {
	dataW := &chanWriter{ch: make(chan []byte, 100)} // buffered → drains fast
	ctrlW := &chanWriter{ch: make(chan []byte, 100)}

	sc := &SplitConn{}
	sw := newSplitWriter(dataW, ctrlW, sc)
	defer sw.close()

	sid := uint32(7)
	sw.Write(buildFrame(smuxCmdSYN, sid, nil))

	// ctrl must receive the SYN (low-latency path, unchanged by the fix).
	select {
	case f := <-ctrlW.ch:
		if f[1] != smuxCmdSYN || streamID(f) != sid {
			t.Fatalf("ctrl: got cmd=%d sid=%d, want SYN sid=%d", f[1], streamID(f), sid)
		}
	case <-time.After(time.Second):
		t.Fatal("SYN not delivered on ctrl")
	}

	// data must ALSO receive the SYN (the fix).  Before the fix the data
	// channel never sees the SYN and this times out.
	select {
	case f := <-dataW.ch:
		if f[1] != smuxCmdSYN || streamID(f) != sid {
			t.Fatalf("data: got cmd=%d sid=%d, want SYN sid=%d", f[1], streamID(f), sid)
		}
	case <-time.After(time.Second):
		t.Fatal("FIX MISSING: regular SYN was not mirrored to the data channel")
	}
}

// TestSplitWriterSYNMirrorPrecedesPSH verifies the data-channel SYN mirror is
// written before the stream's first PSH, so the receiver's smux opens the
// stream before any data for it arrives on the (ordered) data connection.
func TestSplitWriterSYNMirrorPrecedesPSH(t *testing.T) {
	dataW := &chanWriter{ch: make(chan []byte, 100)}
	ctrlW := &chanWriter{ch: make(chan []byte, 100)}

	sc := &SplitConn{}
	sw := newSplitWriter(dataW, ctrlW, sc)
	defer sw.close()

	sid := uint32(5)
	sw.Write(buildFrame(smuxCmdSYN, sid, nil))
	sw.Write(buildFrame(smuxCmdPSH, sid, []byte("first")))
	time.Sleep(100 * time.Millisecond) // let drainData flush both

	var order []byte
	for {
		select {
		case f := <-dataW.ch:
			order = append(order, f[1])
			continue
		default:
		}
		break
	}
	if len(order) < 2 || order[0] != smuxCmdSYN || order[1] != smuxCmdPSH {
		t.Fatalf("data frame order = %v, want [SYN(%d), PSH(%d)]", order, smuxCmdSYN, smuxCmdPSH)
	}
}

// TestSplitWriterFINBypassesCongestedDataCh verifies that FIN frames are
// written promptly via the high-priority finCh even when dataCh is full,
// and that ordering is preserved (PSH before FIN on the wire).
func TestSplitWriterFINBypassesCongestedDataCh(t *testing.T) {
	// dataW: buffered so we can inspect the write order.
	dataW := &chanWriter{ch: make(chan []byte, 100)}
	ctrlW := &chanWriter{ch: make(chan []byte, 100)}

	sc := &SplitConn{}
	sw := newSplitWriter(dataW, ctrlW, sc)
	defer sw.close()

	tcpSID := uint32(10)

	// Write SYN (goes to ctrl).
	sw.Write(buildFrame(smuxCmdSYN, tcpSID, nil))

	// Write some PSH frames for this stream.
	sw.Write(buildFrame(smuxCmdPSH, tcpSID, []byte("chunk1")))
	sw.Write(buildFrame(smuxCmdPSH, tcpSID, []byte("chunk2")))

	// Write FIN — should NOT block even if dataCh were full.
	finDone := make(chan struct{})
	go func() {
		sw.Write(buildFrame(smuxCmdFIN, tcpSID, nil))
		close(finDone)
	}()
	select {
	case <-finDone:
		// Good — FIN returned promptly.
	case <-time.After(2 * time.Second):
		t.Fatal("FIN blocked — not routed to finCh")
	}

	// Let drainData flush everything.
	time.Sleep(100 * time.Millisecond)

	// Collect all frames written to dataW and verify ordering:
	// PSH(chunk1), PSH(chunk2) must appear before FIN.
	var frames []byte
	for {
		select {
		case f := <-dataW.ch:
			frames = append(frames, f[1]) // collect cmd bytes
		default:
			goto done
		}
	}
done:
	// Find FIN position and verify all PSH come before it.
	finIdx := -1
	pshCount := 0
	for i, cmd := range frames {
		if cmd == smuxCmdFIN {
			finIdx = i
		}
		if cmd == smuxCmdPSH {
			pshCount++
			if finIdx >= 0 {
				t.Fatalf("PSH at index %d appeared after FIN at index %d", i, finIdx)
			}
		}
	}
	if finIdx < 0 {
		t.Fatal("FIN not found in data writer output")
	}
	if pshCount < 2 {
		t.Fatalf("expected at least 2 PSH frames before FIN, got %d", pshCount)
	}
}

// TestSplitWriterFINNotBlockedByCongestedDataCh verifies FIN enqueue returns
// promptly even when the data connection is stalled (as does PSH — neither is
// allowed to block the caller / smux write loop).
func TestSplitWriterFINNotBlockedByCongestedDataCh(t *testing.T) {
	// dataW: unbuffered → every Write blocks until someone receives.
	dataW := &chanWriter{ch: make(chan []byte)}
	ctrlW := &chanWriter{ch: make(chan []byte, 100)}

	sc := &SplitConn{}
	sw := newSplitWriter(dataW, ctrlW, sc)
	defer sw.close()

	// Stall the data conn: one frame blocks in drainData, queue a backlog.
	sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 8; i++ {
		sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
	}

	// PSH enqueue must NOT block on a stalled data connection (elastic queue).
	pshDone := make(chan struct{})
	go func() {
		sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
		close(pshDone)
	}()
	select {
	case <-pshDone:
	case <-time.After(2 * time.Second):
		t.Fatal("PSH enqueue blocked on a stalled data connection")
	}

	// FIN must likewise NOT block.
	finDone := make(chan struct{})
	go func() {
		sw.Write(buildFrame(smuxCmdFIN, 99, nil))
		close(finDone)
	}()
	select {
	case <-finDone:
		t.Log("FIN enqueue returned promptly despite stalled data conn")
	case <-time.After(2 * time.Second):
		t.Fatal("FIN enqueue blocked on a stalled data connection")
	}
}

// TestSplitWriterDataStallDoesNotBlockWriteLoop verifies the elastic PSH queue:
// when the data connection stalls (bufferbloat / cross-border jitter),
// enqueuing PSH must NOT block the caller.  smux drives SplitConn.Write from a
// single write loop; if a PSH enqueue blocks, that loop stalls and every stream
// on the connection freezes — plus ctrl frames (NOP keepalive, UPD window
// updates) queue behind it — the head-of-line-blocking bug.  With the bounded
// dataCh this test blocks once ~4 frames are queued; with the elastic queue all
// writes return promptly and a ctrl NOP still gets through during the stall.
func TestSplitWriterDataStallDoesNotBlockWriteLoop(t *testing.T) {
	dataW := &chanWriter{ch: make(chan []byte)} // unbuffered → stalls forever
	ctrlW := &chanWriter{ch: make(chan []byte, 100)}

	sc := &SplitConn{}
	sw := newSplitWriter(dataW, ctrlW, sc)
	defer sw.close()

	// First PSH is picked up by drainData, which then blocks on dataW.Write.
	sw.Write(buildFrame(smuxCmdPSH, 1, []byte("stall")))
	time.Sleep(50 * time.Millisecond)

	// Enqueue far more PSH frames than any bounded channel would hold.  With the
	// elastic queue these all return promptly; the old bounded dataCh blocks the
	// caller (and thus smux's write loop) once it fills.
	writesDone := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			sw.Write(buildFrame(smuxCmdPSH, uint32(i+2), []byte("more")))
		}
		close(writesDone)
	}()
	select {
	case <-writesDone:
	case <-time.After(2 * time.Second):
		t.Fatal("PSH writes blocked on a stalled data connection (head-of-line blocking)")
	}

	// A ctrl frame (NOP keepalive) must still reach the ctrl connection while
	// the data connection is stalled.
	sw.Write(buildFrame(smuxCmdNOP, 0, nil))
	select {
	case f := <-ctrlW.ch:
		if f[1] != smuxCmdNOP {
			t.Fatalf("ctrl received cmd %d, want NOP (%d)", f[1], smuxCmdNOP)
		}
	case <-time.After(time.Second):
		t.Fatal("NOP not delivered on ctrl during data stall")
	}
}

// TestSplitWriterNonDNSSYNDoesNotRegisterCtrl verifies that a normal TCP
// stream's SYN going through ctrl does NOT register the stream for full
// ctrl routing — subsequent PSH/FIN should still go through dataCh.
func TestSplitWriterNonDNSSYNDoesNotRegisterCtrl(t *testing.T) {
	dataW := &chanWriter{ch: make(chan []byte, 100)} // buffered, fast
	ctrlW := &chanWriter{ch: make(chan []byte, 100)}

	sc := &SplitConn{}
	sw := newSplitWriter(dataW, ctrlW, sc)
	defer sw.close()

	tcpSID := uint32(7)

	// SYN goes to ctrl (low latency) AND is mirrored to data (ordering), but
	// does NOT register the stream for full ctrl routing.
	sw.Write(buildFrame(smuxCmdSYN, tcpSID, nil))
	if _, ok := sc.ctrlStreams.Load(tcpSID); ok {
		t.Fatal("non-DNS stream should not be registered in ctrlStreams after SYN")
	}

	// Drain the ctrl-channel SYN.
	select {
	case f := <-ctrlW.ch:
		if f[1] != smuxCmdSYN {
			t.Fatalf("ctrl: expected SYN, got cmd %d", f[1])
		}
	default:
		t.Fatal("SYN not found on ctrl")
	}

	// Drain the data-channel SYN mirror (written ahead of any PSH).
	select {
	case f := <-dataW.ch:
		if f[1] != smuxCmdSYN {
			t.Fatalf("data: expected SYN mirror, got cmd %d", f[1])
		}
	case <-time.After(time.Second):
		t.Fatal("SYN mirror not found on data")
	}

	// PSH should go to dataCh (eventually written to dataW), NOT ctrl.
	sw.Write(buildFrame(smuxCmdPSH, tcpSID, []byte("http request")))

	select {
	case f := <-dataW.ch:
		if f[1] != smuxCmdPSH {
			t.Fatalf("expected PSH on data, got cmd %d", f[1])
		}
	case <-ctrlW.ch:
		t.Fatal("TCP PSH routed to ctrl — should go to data")
	case <-time.After(time.Second):
		t.Fatal("PSH not delivered")
	}
}
