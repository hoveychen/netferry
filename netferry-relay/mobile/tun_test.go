package mobile

import (
	"os"
	"testing"
	"time"

	"github.com/hoveychen/netferry/relay/internal/stats"
)

// TestTunForwarderCloseUnblocksReader verifies that Close() returns even when
// readFromTUN is blocked on tunFile.Read(). Without the fix, Close() hangs
// indefinitely because readFromTUN never observes ctx.Done — this is the
// Android "profile stays connected, DNS spam" bug.
func TestTunForwarderCloseUnblocksReader(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	tf, err := newTunForwarder(r, 1500, nil, stats.NewCounters())
	if err != nil {
		t.Fatalf("newTunForwarder: %v", err)
	}

	// Give readFromTUN a moment to enter its blocking Read.
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		tf.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tunForwarder.Close() did not return within 2s — readFromTUN goroutine is leaking")
	}
}
