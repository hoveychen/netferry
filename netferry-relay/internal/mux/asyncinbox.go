package mux

import (
	"sync"
	"time"
)

const (
	// inboxChanSize is the buffered channel capacity exposed to consumers.
	inboxChanSize = 64

	// maxOverflowFrames is the hard limit on frames buffered in the
	// overflow slice. Beyond this the consumer is assumed dead and the
	// inbox is closed. 8192 frames × 64 KB max ≈ 512 MB worst-case.
	// Increased from 1024 to give slow consumers (e.g. writing to a
	// remote server under TCP backpressure) more room.
	maxOverflowFrames = 8192
)

// asyncInbox decouples the mux reader goroutine from per-channel consumers.
//
// send() never blocks: it first tries a direct channel send; on failure it
// appends to an internal overflow slice.  A background drainer goroutine
// moves overflow frames into the channel.  If the consumer stalls for
// INBOX_SEND_TIMEOUT the inbox is closed (same semantics as before, but
// without blocking the global reader).
type asyncInbox struct {
	ch chan Frame // consumer reads from here

	mu     sync.Mutex
	buf    []Frame
	closed bool

	wake chan struct{} // 1-buffered; nudges drainer
	done chan struct{} // closed on shutdown

	closeOnce sync.Once
}

func newAsyncInbox() *asyncInbox {
	ai := &asyncInbox{
		ch:   make(chan Frame, inboxChanSize),
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
	go ai.drain()
	return ai
}

// send enqueues a frame without blocking the caller.
// Returns false if the inbox is closed or the overflow limit is exceeded.
func (ai *asyncInbox) send(f Frame) bool {
	// Check closed state under lock first to avoid send-on-closed-channel panic.
	ai.mu.Lock()
	if ai.closed {
		ai.mu.Unlock()
		return false
	}

	// Fast path: direct send if channel has space.
	select {
	case ai.ch <- f:
		ai.mu.Unlock()
		return true
	default:
	}

	// Slow path: buffer in overflow slice.
	ai.buf = append(ai.buf, f)
	n := len(ai.buf)
	ai.mu.Unlock()

	// Nudge the drainer.
	select {
	case ai.wake <- struct{}{}:
	default:
	}

	// Hard limit: consumer is hopelessly behind.
	if n > maxOverflowFrames {
		ai.Close()
		return false
	}
	return true
}

// C returns the read-only channel consumers should range over.
func (ai *asyncInbox) C() <-chan Frame {
	return ai.ch
}

// Close shuts down the inbox and stops the drainer goroutine.
// Overflow frames are drained into the channel buffer so consumers
// can read any remaining data after the channel is closed.
func (ai *asyncInbox) Close() {
	ai.closeOnce.Do(func() {
		ai.mu.Lock()
		ai.closed = true
		// Drain remaining overflow frames into the channel buffer before
		// closing. This prevents data loss for frames that were in the
		// overflow slice but hadn't been moved to the channel yet.
		remaining := ai.buf
		ai.buf = nil
		ai.mu.Unlock()

		// Stop the drainer goroutine first so it doesn't race with us.
		close(ai.done)

		// Best-effort drain: push overflow frames into the channel.
		// If the channel buffer is full, remaining frames are dropped.
		// In practice the consumer is still active and draining, so the
		// channel usually has space.
		for _, f := range remaining {
			select {
			case ai.ch <- f:
			default:
				// Channel buffer full — stop trying. The consumer will
				// get what's already in the channel after it's closed.
				goto closeCh
			}
		}
	closeCh:
		close(ai.ch)
	})
}

// drain moves frames from the overflow slice into the consumer channel.
// It runs in its own goroutine so that a slow consumer only blocks this
// goroutine, never the mux reader.
func (ai *asyncInbox) drain() {
	for {
		select {
		case <-ai.wake:
		case <-ai.done:
			return
		}

		for {
			ai.mu.Lock()
			if len(ai.buf) == 0 || ai.closed {
				ai.mu.Unlock()
				break
			}
			f := ai.buf[0]
			ai.buf[0] = Frame{} // allow GC of frame data
			ai.buf = ai.buf[1:]
			if len(ai.buf) == 0 {
				ai.buf = nil // release backing array
			}
			ai.mu.Unlock()

			select {
			case ai.ch <- f:
			case <-time.After(INBOX_SEND_TIMEOUT):
				// Consumer didn't read for too long — give up.
				ai.Close()
				return
			case <-ai.done:
				return
			}
		}
	}
}
