package mobile

import (
	"fmt"
	"net"
	"syscall"
	"time"
)

// protectedDial dials a TCP connection and calls ProtectSocket on the
// underlying fd so the OS doesn't route the connection back through the VPN.
// This is critical on Android where VpnService.protect() must be called
// before the socket connects.
func protectedDial(network, addr string, timeout time.Duration, callback PlatformCallback) (net.Conn, error) {
	d := &net.Dialer{
		Timeout: timeout,
		Control: func(network, address string, c syscall.RawConn) error {
			var fd int
			c.Control(func(rawFD uintptr) {
				fd = int(rawFD)
			})
			if !callback.ProtectSocket(int32(fd)) {
				return fmt.Errorf("ProtectSocket(%d) failed", fd)
			}
			return nil
		},
	}
	return d.Dial(network, addr)
}
