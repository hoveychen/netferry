package main

import (
	"bytes"
	"sync"
)

// logRing is an io.Writer that retains the last N bytes of output and
// fans line-completed writes to optional subscribers. Used by the TUI to
// (a) seed a viewport with prior output and (b) drive live log rendering.
type logRing struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	maxBytes int
	subs     []chan<- string
	pending  []byte
}

func newLogRing(maxBytes int) *logRing {
	return &logRing{maxBytes: maxBytes}
}

// Write appends to the ring (truncating from the head when over capacity)
// and emits each completed line to every subscriber.
func (r *logRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf.Write(p)
	if r.buf.Len() > r.maxBytes {
		excess := r.buf.Len() - r.maxBytes
		r.buf.Next(excess)
	}

	r.pending = append(r.pending, p...)
	for {
		idx := bytes.IndexByte(r.pending, '\n')
		if idx < 0 {
			break
		}
		line := string(r.pending[:idx])
		r.pending = r.pending[idx+1:]
		for _, ch := range r.subs {
			select {
			case ch <- line:
			default: // drop if subscriber is slow — viewport reads ring on resync
			}
		}
	}
	return len(p), nil
}

// Snapshot returns the entire retained buffer as a string.
func (r *logRing) Snapshot() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

// Subscribe returns a channel that receives one string per completed line.
// The caller is responsible for draining it; slow consumers drop messages.
func (r *logRing) Subscribe() <-chan string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan string, 256)
	r.subs = append(r.subs, ch)
	return ch
}
