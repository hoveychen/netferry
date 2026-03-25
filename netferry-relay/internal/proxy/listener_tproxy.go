//go:build linux

package proxy

import (
	"context"
	"fmt"
	"log"
	"net"
	"syscall"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// ListenTProxy starts a TPROXY-aware TCP listener.
// TPROXY preserves the original destination address in the socket, so
// conn.LocalAddr() returns the real target (no NAT lookup needed).
// The listener must bind to 0.0.0.0 with IP_TRANSPARENT so the kernel
// allows accepting connections destined for any IP.
func ListenTProxy(port int, client *mux.MuxClient, counters *stats.Counters) error {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TRANSPARENT, 1)
			})
		},
	}

	ln, err := lc.Listen(context.Background(), "tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return fmt.Errorf("tproxy listen :%d: %w", port, err)
	}
	defer ln.Close()

	log.Printf("proxy: tproxy listening on :%d", port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleConn(conn, client, counters)
	}
}

// ListenDNSTProxy creates a TPROXY-aware UDP socket for DNS interception.
// TPROXY does not rewrite packet headers, so the socket must have
// IP_TRANSPARENT to accept packets with non-local destination addresses.
// Must bind to 0.0.0.0 (not 127.0.0.1) for the same reason.
func ListenDNSTProxy(port int) (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TRANSPARENT, 1)
				// IP_RECVORIGDSTADDR allows recvmsg to return the original
				// destination, but for our DNS proxy we don't need it — we just
				// need to accept the packets.
			})
		},
	}
	return lc.ListenPacket(context.Background(), "udp", fmt.Sprintf("0.0.0.0:%d", port))
}
