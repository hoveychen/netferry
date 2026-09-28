package mobile

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"
)

// protectControl calls ProtectSocket on a socket's fd before it is used.
func protectControl(callback PlatformCallback) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var fd int
		c.Control(func(rawFD uintptr) {
			fd = int(rawFD)
		})
		if !callback.ProtectSocket(int32(fd)) {
			return fmt.Errorf("ProtectSocket(%d) failed", fd)
		}
		return nil
	}
}

// protectedDial dials a TCP connection and calls ProtectSocket on the
// underlying fd so the OS doesn't route the connection back through the VPN.
// This is critical on Android where VpnService.protect() must be called
// before the socket connects.
func protectedDial(network, addr string, timeout time.Duration, callback PlatformCallback) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout, Control: protectControl(callback)}
	return d.Dial(network, addr)
}

// protectedListenUDP opens the unconnected UDP socket fectun sends from,
// protected the same way so its datagrams bypass the VPN.
func protectedListenUDP(callback PlatformCallback) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: protectControl(callback)}
	pc, err := lc.ListenPacket(context.Background(), "udp", ":0")
	if err != nil {
		return nil, err
	}
	c := pc.(*net.UDPConn)
	c.SetReadBuffer(4 << 20)
	c.SetWriteBuffer(4 << 20)
	return c, nil
}
