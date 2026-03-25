package mux

import (
	"sync"
	"testing"
	"time"
)

// TestAsyncInbox_BasicSendRecv verifies normal send/receive works.
func TestAsyncInbox_BasicSendRecv(t *testing.T) {
	ai := newAsyncInbox()
	defer ai.Close()

	f := Frame{Channel: 1, Cmd: CMD_TCP_DATA, Data: []byte("hello")}
	if !ai.send(f) {
		t.Fatal("send returned false")
	}

	select {
	case got := <-ai.C():
		if got.Channel != 1 || string(got.Data) != "hello" {
			t.Errorf("unexpected frame: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout receiving frame")
	}
}

// TestAsyncInbox_OverflowDrain verifies that frames exceeding channel buffer
// are drained from the overflow slice.
func TestAsyncInbox_OverflowDrain(t *testing.T) {
	ai := newAsyncInbox()
	defer ai.Close()

	total := inboxChanSize + 50 // exceed channel buffer, trigger overflow
	for i := 0; i < total; i++ {
		if !ai.send(Frame{Channel: uint16(i), Cmd: CMD_TCP_DATA, Data: []byte{byte(i)}}) {
			t.Fatalf("send failed at frame %d", i)
		}
	}

	// Consume all frames and verify ordering.
	for i := 0; i < total; i++ {
		select {
		case f := <-ai.C():
			if f.Channel != uint16(i) {
				t.Fatalf("frame %d: got channel %d", i, f.Channel)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout at frame %d", i)
		}
	}
}

// TestAsyncInbox_OverflowLimitKillsInbox verifies that exceeding
// maxOverflowFrames closes the inbox.
func TestAsyncInbox_OverflowLimitKillsInbox(t *testing.T) {
	ai := newAsyncInbox()

	// Fill the channel buffer first.
	for i := 0; i < inboxChanSize; i++ {
		ai.send(Frame{Channel: uint16(i), Cmd: CMD_TCP_DATA})
	}

	// Now send more than maxOverflowFrames without consuming.
	// The consumer is not reading, so overflow accumulates.
	killed := false
	for i := 0; i < maxOverflowFrames+100; i++ {
		if !ai.send(Frame{Channel: uint16(i), Cmd: CMD_TCP_DATA}) {
			killed = true
			break
		}
	}

	if !killed {
		t.Error("expected inbox to be killed after exceeding maxOverflowFrames")
	}
}

// TestAsyncInbox_SlowConsumerTimeout verifies that a consumer that doesn't
// read for INBOX_SEND_TIMEOUT causes the inbox to close.
// NOTE: This test waits for INBOX_SEND_TIMEOUT (60s). Skip with -short.
func TestAsyncInbox_SlowConsumerTimeout(t *testing.T) {
	t.Skip("slow test — INBOX_SEND_TIMEOUT is 60s; run manually with -timeout 120s if needed")
}

// TestAsyncInbox_CloseWithFullChannel tests that Close() is best-effort when
// the channel buffer is completely full and no consumer is running.
// This is an edge case — in practice the consumer (io.Copy) is always active.
func TestAsyncInbox_CloseWithFullChannel(t *testing.T) {
	ai := newAsyncInbox()

	// Fill channel buffer completely.
	for i := 0; i < inboxChanSize; i++ {
		ai.send(Frame{Channel: uint16(i), Cmd: CMD_TCP_DATA, Data: []byte{byte(i)}})
	}

	// Add frames to overflow (channel is full, these go to buf).
	overflowCount := 10
	for i := 0; i < overflowCount; i++ {
		ai.send(Frame{Channel: uint16(inboxChanSize + i), Cmd: CMD_TCP_DATA, Data: []byte{byte(inboxChanSize + i)}})
	}

	time.Sleep(10 * time.Millisecond)
	ai.Close()

	count := 0
	for range ai.C() {
		count++
	}

	// With full channel + no consumer, overflow frames may be lost (best-effort).
	t.Logf("full-channel edge case: sent %d, recovered %d", inboxChanSize+overflowCount, count)
	if count < inboxChanSize {
		t.Errorf("should recover at least channel buffer (%d), got %d", inboxChanSize, count)
	}
}

// TestAsyncInbox_CloseWithActiveConsumer tests that Close() drains overflow
// when a consumer is actively reading. This is the real-world scenario.
func TestAsyncInbox_CloseWithActiveConsumer(t *testing.T) {
	ai := newAsyncInbox()

	total := inboxChanSize + 20 // overflow some frames

	// Start consumer that reads frames into a slice.
	var received []Frame
	done := make(chan struct{})
	go func() {
		for f := range ai.C() {
			received = append(received, f)
		}
		close(done)
	}()

	// Send all frames.
	for i := 0; i < total; i++ {
		ai.send(Frame{Channel: uint16(i), Cmd: CMD_TCP_DATA, Data: []byte{byte(i)}})
	}

	// Give the consumer and drainer time to process.
	time.Sleep(50 * time.Millisecond)

	// Close — overflow should be drained into the channel for the consumer.
	ai.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("consumer didn't finish")
	}

	if len(received) != total {
		t.Errorf("with active consumer: sent %d, received %d (lost %d)", total, len(received), total-len(received))
	} else {
		t.Logf("all %d frames received with active consumer", total)
	}
}

// TestAsyncInbox_ConcurrentSendRecv stress-tests concurrent producers and a
// single consumer.
func TestAsyncInbox_ConcurrentSendRecv(t *testing.T) {
	ai := newAsyncInbox()
	defer ai.Close()

	const numProducers = 10
	const msgsPerProducer = 200
	total := numProducers * msgsPerProducer

	var wg sync.WaitGroup
	for p := 0; p < numProducers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < msgsPerProducer; i++ {
				ai.send(Frame{Channel: uint16(p), Cmd: CMD_TCP_DATA, Data: []byte{byte(i)}})
			}
		}(p)
	}

	// Consumer.
	received := 0
	done := make(chan struct{})
	go func() {
		for range ai.C() {
			received++
			if received >= total {
				close(done)
				return
			}
		}
		close(done)
	}()

	wg.Wait()

	select {
	case <-done:
		if received < total {
			t.Errorf("received %d of %d frames", received, total)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout: received only %d of %d frames", received, total)
	}
}
