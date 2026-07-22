//go:build linux || darwin

package netmon

import "syscall"

// isRetryableRecvErr reports whether a raw socket read error is transient, so
// the netmon read loop should retry rather than abort.
//
// EAGAIN/EWOULDBLOCK are the read-timeout wakeups (SO_RCVTIMEO) used to poll
// the done channel between reads. EINTR happens when a signal interrupts the
// blocking syscall — notably Go's own async-preemption SIGURG, which can hit
// any raw syscall (the os/net wrappers retry EINTR internally, but
// syscall.Recvfrom / syscall.Read do not). Treating EINTR as fatal made
// netmon.Watch return an error, which the engine turns into a spurious full
// exit-for-reconnect; retrying instead keeps the watch alive.
func isRetryableRecvErr(err error) bool {
	return err == syscall.EAGAIN || err == syscall.EWOULDBLOCK || err == syscall.EINTR
}
