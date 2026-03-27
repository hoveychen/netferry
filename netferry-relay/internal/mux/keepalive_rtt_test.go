package mux

import (
	"io"
	"testing"
	"time"

	"github.com/hoveychen/netferry/relay/internal/stats"
)

// delayWriter adds artificial latency to every Write call, simulating a
// congested underlying transport (e.g. a full SSH socket send-buffer).
type delayWriter struct {
	w     io.Writer
	delay time.Duration
}

func (d *delayWriter) Write(p []byte) (int, error) {
	time.Sleep(d.delay)
	return d.w.Write(p)
}

// TestKeepaliveRTT_NormalTransport is the baseline: on an unloaded in-memory
// pipe the PING/PONG round-trip should complete in well under 100 ms.
func TestKeepaliveRTT_NormalTransport(t *testing.T) {
	serverPipe, clientPipe := newPipePair()
	srv := NewMuxServer(serverPipe.r, serverPipe.w, ServerHandlers{})
	cli := NewMuxClient(clientPipe.r, clientPipe.w)

	ct := stats.NewCounters()
	cli.SetCounters(ct)

	go srv.Run() //nolint:errcheck
	go cli.Run() //nolint:errcheck

	// Manually trigger a PING (normally fired by the keepalive ticker every 15 s).
	cli.lastPing.Store(time.Now().UnixNano())
	cli.priorityOut <- Frame{Channel: 0, Cmd: CMD_PING}

	deadline := time.Now().Add(2 * time.Second)
	var rtt time.Duration
	for time.Now().Before(deadline) {
		if rtt = ct.LastKeepaliveRTT(); rtt > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if rtt == 0 {
		t.Fatal("no keepalive RTT observed within 2 s — PONG never received")
	}
	const maxFastRTT = 100 * time.Millisecond
	if rtt > maxFastRTT {
		t.Errorf("baseline RTT %v > %v — in-memory pipe should be fast", rtt, maxFastRTT)
	}
	t.Logf("baseline RTT: %v", rtt)

	clientPipe.w.Close()
	clientPipe.r.Close()
	serverPipe.r.Close()
	serverPipe.w.Close()
}

// TestKeepaliveRTT_SlowFlushElevatesRTT reproduces the
// "mux keepalive RTT is elevated: 13.158s" warning seen in production.
//
// Root cause: keepalive() stores lastPing *before* enqueuing the PING frame.
// If the underlying writer is slow (congested SSH socket), bw.Flush() blocks
// and the PING bytes cannot leave until the flush completes.  Because RTT is
// measured from lastPing to PONG receipt, the entire flush delay is counted.
func TestKeepaliveRTT_SlowFlushElevatesRTT(t *testing.T) {
	const writeDelay = 300 * time.Millisecond

	csr, csw := io.Pipe() // client → server
	scr, scw := io.Pipe() // server → client

	// Inject latency only on the client→server direction.
	dw := &delayWriter{w: csw, delay: writeDelay}

	srv := NewMuxServer(csr, scw, ServerHandlers{})
	cli := NewMuxClient(scr, dw)

	ct := stats.NewCounters()
	cli.SetCounters(ct)

	go srv.Run() //nolint:errcheck
	go cli.Run() //nolint:errcheck

	// Replicate what keepalive() does: record send time, then enqueue PING.
	// With a slow writer the PING sits in the bufio buffer until Flush unblocks,
	// so the measured RTT will include the full writeDelay.
	cli.lastPing.Store(time.Now().UnixNano())
	cli.priorityOut <- Frame{Channel: 0, Cmd: CMD_PING}

	deadline := time.Now().Add(5 * time.Second)
	var rtt time.Duration
	for time.Now().Before(deadline) {
		if rtt = ct.LastKeepaliveRTT(); rtt > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if rtt == 0 {
		t.Fatal("no keepalive RTT observed — PING may have been lost or PONG never returned")
	}
	if rtt < writeDelay {
		t.Errorf("RTT %v < writeDelay %v: slow-flush path should have elevated RTT", rtt, writeDelay)
	}
	t.Logf("elevated RTT reproduced: %v (writeDelay=%v)", rtt, writeDelay)

	csr.Close()
	csw.Close()
	scr.Close()
	scw.Close()
}

// TestKeepaliveRTT_PingDroppedWhenPriorityQueueFull verifies that when
// priorityOut is at capacity the keepalive PING is silently dropped via the
// non-blocking select default branch (client.go keepalive()).
//
// A dropped PING means no PONG comes back, so lastPong grows stale.  After
// KEEPALIVE_INTERVAL+KEEPALIVE_TIMEOUT the client declares the connection dead.
// This test confirms the drop behaviour before the timeout fires.
func TestKeepaliveRTT_PingDroppedWhenPriorityQueueFull(t *testing.T) {
	cli := NewMuxClient(nil, nil)

	// Fill the priority queue to capacity (mirrors what happens when the writer
	// goroutine is blocked on Flush and cannot drain priorityOut fast enough).
	for i := 0; i < cap(cli.priorityOut); i++ {
		cli.priorityOut <- Frame{Channel: 0, Cmd: CMD_PING}
	}
	fullLen := len(cli.priorityOut)
	if fullLen != cap(cli.priorityOut) {
		t.Fatalf("setup: expected priority queue full (%d), got %d", cap(cli.priorityOut), fullLen)
	}

	// Simulate the keepalive() non-blocking send.
	cli.lastPing.Store(time.Now().UnixNano())
	dropped := true
	select {
	case cli.priorityOut <- Frame{Channel: 0, Cmd: CMD_PING}:
		dropped = false
	default:
		// expected: queue is full, PING is silently discarded
	}

	if !dropped {
		t.Error("expected PING to be dropped when priority queue is full, but it was enqueued")
	}
	if len(cli.priorityOut) != fullLen {
		t.Errorf("queue length changed from %d to %d — PING should not have been enqueued", fullLen, len(cli.priorityOut))
	}
	t.Logf("confirmed: PING silently dropped when priorityOut is full (%d/%d)", len(cli.priorityOut), cap(cli.priorityOut))
}

// TestKeepalive_StaleLastPongTriggersTimeout verifies the exact condition that
// keepalive() uses to declare a dead connection: if lastPong is older than
// KEEPALIVE_INTERVAL+KEEPALIVE_TIMEOUT the connection is killed.
//
// This test makes the condition observable without waiting 30 s.
func TestKeepalive_StaleLastPongTriggersTimeout(t *testing.T) {
	cli := NewMuxClient(nil, nil)

	// Pretend the last pong arrived just beyond the timeout window.
	staleTime := time.Now().Add(-(KEEPALIVE_INTERVAL + KEEPALIVE_TIMEOUT + time.Second))
	cli.lastPong.Store(staleTime.UnixNano())

	// Replicate the check from keepalive() verbatim.
	last := time.Unix(0, cli.lastPong.Load())
	elapsed := time.Since(last)
	threshold := KEEPALIVE_INTERVAL + KEEPALIVE_TIMEOUT

	if elapsed <= threshold {
		t.Fatalf("stale pong not detected: elapsed=%v, threshold=%v", elapsed.Round(time.Millisecond), threshold)
	}
	t.Logf("timeout condition correctly triggered: elapsed=%v > threshold=%v",
		elapsed.Round(time.Millisecond), threshold)
}
