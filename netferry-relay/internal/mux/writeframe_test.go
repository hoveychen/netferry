package mux

import (
	"bytes"
	"sync"
	"testing"
)

// countingWriter counts the number of Write calls.
type countingWriter struct {
	mu     sync.Mutex
	calls  int
	total  int
	chunks []int // size of each write
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	cw.calls++
	cw.total += len(p)
	cw.chunks = append(cw.chunks, len(p))
	return len(p), nil
}

// TestWriteFrame_WriteCalls checks how many Write calls are made per frame.
// Currently WriteFrame makes 2 calls (header + data) for each frame with data.
// This is problematic for SSH sessions where each Write becomes a separate
// SSH_MSG_CHANNEL_DATA packet.
func TestWriteFrame_WriteCalls(t *testing.T) {
	cw := &countingWriter{}

	f := Frame{Channel: 1, Cmd: CMD_TCP_DATA, Data: []byte("hello")}
	if err := WriteFrame(cw, f); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	// Current behavior: 2 writes (header + data).
	// Ideal behavior: 1 write (header + data combined).
	t.Logf("WriteFrame made %d Write() calls for a %d-byte frame (chunks: %v)",
		cw.calls, cw.total, cw.chunks)

	if cw.calls > 1 {
		t.Logf("BUG: WriteFrame makes %d Write calls per frame — each becomes a separate SSH packet", cw.calls)
	}
}

// TestWriteFrame_SmallFrameOverhead measures overhead for small SSE-like chunks
// (simulating LLM streaming tokens).
func TestWriteFrame_SmallFrameOverhead(t *testing.T) {
	cw := &countingWriter{}

	// Simulate 100 small SSE events (~30 bytes each, like LLM tokens).
	for i := 0; i < 100; i++ {
		f := Frame{Channel: 1, Cmd: CMD_TCP_DATA, Data: []byte(`data: {"token":"hello"}` + "\n\n")}
		if err := WriteFrame(cw, f); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}

	t.Logf("100 small frames: %d Write() calls, %d total bytes", cw.calls, cw.total)
	t.Logf("average %.1f Write() calls per frame", float64(cw.calls)/100)

	// With buffered writer, this should be significantly fewer calls.
	if cw.calls > 100 {
		t.Logf("ISSUE: %d Write calls for 100 frames — each pair is 2 SSH packets (header+data). "+
			"With buffering this should be ~1 flush per batch.", cw.calls)
	}
}

// TestWriteFrame_AtomicWrite verifies data integrity when writing to a buffer.
func TestWriteFrame_AtomicWrite(t *testing.T) {
	var buf bytes.Buffer

	frames := make([]Frame, 50)
	for i := range frames {
		data := make([]byte, 100)
		for j := range data {
			data[j] = byte(i)
		}
		frames[i] = Frame{Channel: uint16(i), Cmd: CMD_TCP_DATA, Data: data}
	}

	for _, f := range frames {
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}

	// Read them back and verify.
	for i, want := range frames {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("frame %d: ReadFrame: %v", i, err)
		}
		if got.Channel != want.Channel {
			t.Errorf("frame %d: channel got %d, want %d", i, got.Channel, want.Channel)
		}
		if !bytes.Equal(got.Data, want.Data) {
			t.Errorf("frame %d: data mismatch", i)
		}
	}
}
