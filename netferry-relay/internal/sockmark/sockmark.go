// Package sockmark tags the tunnel's own outbound sockets so the Linux
// firewall rules can let them through instead of redirecting them back into
// the local proxy.
//
// Matching on the socket mark (rather than on the owning uid) keeps the
// exemption scoped to netferry itself: a user who runs everything as root
// still has their traffic proxied.
package sockmark

import (
	"context"
	"net"
	"time"
)

// Bypass is the SO_MARK value set on the tunnel's direct-dial sockets. The
// firewall returns early for packets carrying it. It must differ from the
// TPROXY fwmark (default 1).
const Bypass = 0x4e46 // "NF"

// DialTimeout is net.DialTimeout with the bypass mark applied to the socket.
func DialTimeout(network, addr string, timeout time.Duration) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout, Control: control}
	return d.Dial(network, addr)
}

// ListenUDP is net.ListenUDP("udp", nil) with the bypass mark applied.
func ListenUDP() (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: control}
	pc, err := lc.ListenPacket(context.Background(), "udp", ":0")
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}
