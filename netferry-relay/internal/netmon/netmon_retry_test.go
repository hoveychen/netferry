//go:build linux || darwin

package netmon

import (
	"syscall"
	"testing"
)

// TestIsRetryableRecvErr verifies the netmon read loop retries on transient
// errors — including EINTR. Without EINTR handling, a signal-interrupted raw
// syscall (e.g. Go async-preemption SIGURG) makes Watch return an error, which
// the engine escalates into a spurious full exit-for-reconnect.
func TestIsRetryableRecvErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"EAGAIN", syscall.EAGAIN, true},
		{"EWOULDBLOCK", syscall.EWOULDBLOCK, true},
		{"EINTR", syscall.EINTR, true},
		{"EBADF (fatal)", syscall.EBADF, false},
	}
	for _, c := range cases {
		if got := isRetryableRecvErr(c.err); got != c.want {
			t.Errorf("isRetryableRecvErr(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}
