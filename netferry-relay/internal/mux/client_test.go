package mux

import (
	"net"
	"testing"
	"time"
)

// recordingCloser records whether Close was called.
type recordingCloser struct{ closed chan struct{} }

func (r *recordingCloser) Close() error {
	close(r.closed)
	return nil
}

// TestMuxClientClosesCtrlCloserOnRun verifies the split-mode ctrl SSH
// connection is closed when the client's Run() returns (member death /
// reconnect / teardown), preventing a ctrl connection leak.  Before the fix,
// Run's teardown closes only the FairWriter and ignores the ctrl connection.
func TestMuxClientClosesCtrlCloserOnRun(t *testing.T) {
	cli, srv := net.Pipe()
	mc := NewMuxClient(cli, cli)
	rc := &recordingCloser{closed: make(chan struct{})}
	mc.SetCtrlCloser(rc)

	done := make(chan struct{})
	go func() { _ = mc.Run(); close(done) }()

	// Kill the underlying transport → smux AcceptStream errors → Run returns.
	srv.Close()
	cli.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after transport closed")
	}
	select {
	case <-rc.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("ctrlCloser was not closed after Run returned (ctrl connection leak)")
	}
}
