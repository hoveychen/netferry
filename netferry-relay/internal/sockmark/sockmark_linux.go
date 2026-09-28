//go:build linux && !android

package sockmark

import "syscall"

// control sets SO_MARK on the socket before it connects. SO_MARK needs
// CAP_NET_ADMIN; the tunnel runs as root on Linux, but if it cannot be set the
// dial still proceeds unmarked — the proxy's direct-dial loop guard is the
// fallback.
func control(network, address string, c syscall.RawConn) error {
	c.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, Bypass)
	})
	return nil
}
