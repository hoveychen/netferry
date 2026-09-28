package tui

import (
	"bytes"
	"sync"
)

// MaxLogLines is how many tunnel log lines the Logs tab keeps (the desktop
// keeps 500).
const MaxLogLines = 500

// LogRing is an io.Writer that keeps the last MaxLogLines complete lines of
// tunnel output and fans each completed line out to subscribers.
type LogRing struct {
	mu      sync.Mutex
	lines   []string
	pending []byte
	subs    []chan string
	maxLine int
}

// NewLogRing returns a ring that truncates single lines longer than maxLine
// bytes (defends against a binary blob on stderr).
func NewLogRing(maxLine int) *LogRing { return &LogRing{maxLine: maxLine} }

func (r *LogRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, p...)
	for {
		i := bytes.IndexByte(r.pending, '\n')
		if i < 0 {
			break
		}
		line := r.pending[:i]
		if len(line) > r.maxLine {
			line = line[:r.maxLine]
		}
		s := string(bytes.TrimRight(line, "\r"))
		r.pending = r.pending[i+1:]
		r.lines = append(r.lines, s)
		if len(r.lines) > MaxLogLines {
			r.lines = r.lines[len(r.lines)-MaxLogLines:]
		}
		for _, ch := range r.subs {
			select {
			case ch <- s:
			default: // slow subscriber; Lines() is the source of truth
			}
		}
	}
	if len(r.pending) > r.maxLine {
		r.pending = r.pending[:0]
	}
	return len(p), nil
}

// Lines returns a copy of the retained lines, oldest first.
func (r *LogRing) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

// Clear drops retained lines (the desktop resets logs on each connect).
func (r *LogRing) Clear() {
	r.mu.Lock()
	r.lines = nil
	r.mu.Unlock()
}

// Subscribe returns a channel receiving each completed line.
func (r *LogRing) Subscribe() <-chan string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan string, 1024)
	r.subs = append(r.subs, ch)
	return ch
}
