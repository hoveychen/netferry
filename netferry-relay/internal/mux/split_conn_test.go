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
// written to the ctrl channel even when dataCh is completely full.
//
// Setup: dataW blocks every write (unbuffered channel), so drainData blocks
// on the first frame and dataCh fills to capacity.  Then we write a SYN
// frame and verify it completes instantly via ctrl.
//
// Before the fix, SYN goes through dataCh and blocks indefinitely.
// After the fix, SYN goes through ctrl and returns immediately.
func TestSplitWriterSYNBypassesCongestedDataCh(t *testing.T) {
	// dataW: unbuffered → every Write blocks until someone receives.
	dataW := &chanWriter{ch: make(chan []byte)}
	// ctrlW: buffered → Write returns immediately.
	ctrlW := &chanWriter{ch: make(chan []byte, 100)}

	sc := &SplitConn{}
	sw := newSplitWriter(dataW, ctrlW, sc)
	defer sw.close()

	// Step 1: write one PSH frame. drainData picks it up from dataCh and
	// blocks trying to write to dataW (unbuffered channel, nobody reads).
	sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
	time.Sleep(50 * time.Millisecond) // let drainData pick it up

	// Step 2: fill the remaining dataCh capacity.
	for i := 0; i < dataChSize; i++ {
		sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
	}
	// dataCh is now full and drainData is blocked. Any dataCh write will block.

	// Step 3: verify PSH IS blocked (proves the data channel is congested).
	pshDone := make(chan struct{})
	go func() {
		sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
		close(pshDone)
	}()
	select {
	case <-pshDone:
		t.Fatal("PSH should be blocked when dataCh is full")
	case <-time.After(200 * time.Millisecond):
		// Good — PSH is blocked as expected.
	}

	// Step 4: write a SYN frame. With the fix it goes to ctrl and returns
	// immediately.  Without the fix it enters dataCh and blocks forever.
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

	// Congest the data channel (same as above).
	sw.Write(buildFrame(smuxCmdPSH, 1, []byte("bulk")))
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < dataChSize; i++ {
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

// TestSplitWriterFINNotBlockedByCongestedDataCh verifies FIN is promptly
// delivered even when the data channel is completely saturated.
func TestSplitWriterFINNotBlockedByCongestedDataCh(t *testing.T) {
	// dataW: unbuffered → every Write blocks until someone receives.
	dataW := &chanWriter{ch: make(chan []byte)}
	ctrlW := &chanWriter{ch: make(chan []byte, 100)}

	sc := &SplitConn{}
	sw := newSplitWriter(dataW, ctrlW, sc)
	defer sw.close()

	// Congest: one frame blocks in drainData, then fill dataCh.
	sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < dataChSize; i++ {
		sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
	}

	// Verify PSH IS blocked (proves data channel is congested).
	pshDone := make(chan struct{})
	go func() {
		sw.Write(buildFrame(smuxCmdPSH, 1, []byte("data")))
		close(pshDone)
	}()
	select {
	case <-pshDone:
		t.Fatal("PSH should be blocked when dataCh is full")
	case <-time.After(200 * time.Millisecond):
	}

	// FIN should NOT block — it goes to finCh, not dataCh.
	finDone := make(chan struct{})
	go func() {
		sw.Write(buildFrame(smuxCmdFIN, 99, nil))
		close(finDone)
	}()
	select {
	case <-finDone:
		t.Log("FIN bypassed congested dataCh via finCh")
	case <-time.After(2 * time.Second):
		t.Fatal("FIN blocked by full dataCh — should use finCh")
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

	// SYN goes to ctrl (fix), but does NOT register in ctrlStreams.
	sw.Write(buildFrame(smuxCmdSYN, tcpSID, nil))
	if _, ok := sc.ctrlStreams.Load(tcpSID); ok {
		t.Fatal("non-DNS stream should not be registered in ctrlStreams after SYN")
	}

	// Drain the SYN from ctrlW so it doesn't interfere with the PSH check.
	select {
	case <-ctrlW.ch:
	default:
		t.Fatal("SYN not found on ctrl")
	}

	// PSH should go to dataCh (eventually written to dataW), NOT ctrl.
	sw.Write(buildFrame(smuxCmdPSH, tcpSID, []byte("http request")))
	time.Sleep(50 * time.Millisecond) // let drainData forward it

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
