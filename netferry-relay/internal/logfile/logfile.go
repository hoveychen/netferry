// Package logfile provides a size-based rotating log file writer.
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	DefaultMaxSize    = 64 * 1024 * 1024 // 64 MB
	DefaultMaxBackups = 3
)

// RotatingWriter writes to a file and rotates when the file exceeds maxSize.
// It keeps up to maxBackups old files (e.g. client.log.1, client.log.2, ...).
// It is safe for concurrent use.
type RotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxSize    int64
	maxBackups int
	file       *os.File
	size       int64
}

// New creates a RotatingWriter that writes to path with the given max size and
// backup count. The parent directory is created if it does not exist.
func New(path string, maxSize int64, maxBackups int) (*RotatingWriter, error) {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	if maxBackups <= 0 {
		maxBackups = DefaultMaxBackups
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("logfile: mkdir: %w", err)
	}
	w := &RotatingWriter{
		path:       path,
		maxSize:    maxSize,
		maxBackups: maxBackups,
	}
	if err := w.openExisting(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) openExisting() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("logfile: open: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("logfile: stat: %w", err)
	}
	w.file = f
	w.size = info.Size()
	return nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Prepend a timestamp to each line written to the log file.
	stamped := w.addTimestamps(p)

	if w.size+int64(len(stamped)) > w.maxSize {
		if err := w.rotate(); err != nil {
			// Best effort: continue writing to the current file.
			_ = err
		}
	}
	n, err := w.file.Write(stamped)
	w.size += int64(n)
	// Return original length so io.MultiWriter doesn't report a short write.
	return len(p), err
}

// addTimestamps prefixes each non-empty line in p with a timestamp.
func (w *RotatingWriter) addTimestamps(p []byte) []byte {
	ts := time.Now().Format("2006-01-02 15:04:05.000 ")
	// Fast path: single line (common case for log.Print).
	hasNewline := len(p) > 0 && p[len(p)-1] == '\n'
	lineCount := 0
	for _, b := range p {
		if b == '\n' {
			lineCount++
		}
	}
	if hasNewline && lineCount == 1 {
		out := make([]byte, 0, len(ts)+len(p))
		out = append(out, ts...)
		out = append(out, p...)
		return out
	}
	// Multi-line: stamp each line.
	out := make([]byte, 0, len(p)+lineCount*len(ts)+len(ts))
	start := 0
	for i, b := range p {
		if b == '\n' {
			out = append(out, ts...)
			out = append(out, p[start:i+1]...)
			start = i + 1
		}
	}
	// Trailing content without a newline.
	if start < len(p) {
		out = append(out, ts...)
		out = append(out, p[start:]...)
	}
	return out
}

func (w *RotatingWriter) rotate() error {
	w.file.Close()

	// Shift existing backups: .3 → delete, .2 → .3, .1 → .2, current → .1
	for i := w.maxBackups; i >= 1; i-- {
		src := w.backupName(i - 1)
		dst := w.backupName(i)
		if i == w.maxBackups {
			os.Remove(dst)
		}
		os.Rename(src, dst)
	}
	// backupName(0) == w.path, so w.path has been renamed to .1.
	// Open a fresh file.
	return w.openExisting()
}

func (w *RotatingWriter) backupName(index int) string {
	if index == 0 {
		return w.path
	}
	return fmt.Sprintf("%s.%d", w.path, index)
}

// Close closes the underlying file.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
