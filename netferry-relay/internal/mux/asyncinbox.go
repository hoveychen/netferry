package mux

import (
	"log"
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
	label string
	ch    chan Frame // consumer reads from here

	mu          sync.Mutex
	buf         []Frame
	closed      bool
	warnedLevel int

	wake chan struct{} // 1-buffered; nudges drainer
	done chan struct{} // closed by drainer on shutdown

	closeOnce sync.Once
}

func newAsyncInbox(labels ...string) *asyncInbox {
	label := "unknown"
	if len(labels) > 0 && labels[0] != "" {
		label = labels[0]
	}
	ai := &asyncInbox{
		label: label,
		ch:    make(chan Frame, inboxChanSize),
		wake:  make(chan struct{}, 1),
		done:  make(chan struct{}),
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
	ai.buf = append(ai.buf, f)
	n := len(ai.buf)
	level := n / 512
	if level > ai.warnedLevel {
		ai.warnedLevel = level
		log.Printf("warning: mux inbox backlog growing: label=%s backlog=%d frames", ai.label, n)
	}
	ai.mu.Unlock()

	// Nudge the drainer.
	select {
	case ai.wake <- struct{}{}:
	default:
	}

	// Hard limit: consumer is hopelessly behind.
	if n > maxOverflowFrames {
		log.Printf("warning: mux inbox overflow limit exceeded: label=%s backlog=%d frames", ai.label, n)
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
		buffered := len(ai.buf)
		ai.mu.Unlock()

		log.Printf("mux: inbox closing: label=%s buffered=%d", ai.label, buffered)

		// Wake the drainer so it can finish draining buffered frames and close ch.
		select {
		case ai.wake <- struct{}{}:
		default:
		}
	})
}

// drain moves frames from the overflow slice into the consumer channel.
// It runs in its own goroutine so that a slow consumer only blocks this
// goroutine, never the mux reader.
func (ai *asyncInbox) drain() {
	defer close(ai.done)
	for {
		ai.mu.Lock()
		if len(ai.buf) == 0 {
			closed := ai.closed
			ai.mu.Unlock()
			if closed {
				close(ai.ch)
				return
			}
			<-ai.wake
			continue
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
			ai.mu.Lock()
			pending := len(ai.buf)
			ai.closed = true
			ai.buf = nil
			ai.mu.Unlock()

			// Consumer didn't read for too long — give up and close with
			// whatever data is already buffered in ai.ch.
			log.Printf("warning: mux inbox consumer stalled: label=%s timeout=%s pending=%d", ai.label, INBOX_SEND_TIMEOUT, pending)
			close(ai.ch)
			return
		}
	}
}
