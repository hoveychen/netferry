// Package proxy implements the local transparent TCP proxy and DNS interceptor.
package proxy

import (
	"fmt"
	"io"
	"log"
	"net"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// QueryOrigDstFunc is the platform-specific function to resolve the original
// destination of a redirected TCP connection.
// Set by platform-specific init() or by the caller before Listen().
var QueryOrigDstFunc func(conn net.Conn) (ip string, port int, err error)

// UseTProxy selects the TPROXY listener instead of the default NAT-based one.
// Set by cmd/tunnel when --method=tproxy is chosen. Only used on Linux.
var UseTProxy bool

// Listen accepts connections on the local proxy port and forwards them via mux.
// Blocks until the listener is closed.
func Listen(port int, client *mux.MuxClient, counters *stats.Counters) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("proxy listen :%d: %w", port, err)
	}
	defer ln.Close()

	log.Printf("proxy: listening on :%d", port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleConn(conn, client, counters)
	}
}

func handleConn(conn net.Conn, client *mux.MuxClient, counters *stats.Counters) {
	defer conn.Close()

	// Resolve original destination.
	var dstIP string
	var dstPort int
	var err error

	if QueryOrigDstFunc != nil {
		dstIP, dstPort, err = QueryOrigDstFunc(conn)
		if err != nil {
			log.Printf("proxy: origdst lookup: %v", err)
			return
		}
	} else {
		// Fallback: use local address (happens when no firewall redirect).
		la := conn.LocalAddr().(*net.TCPAddr)
		dstIP = la.IP.String()
		dstPort = la.Port
	}

	// Determine address family.
	ip := net.ParseIP(dstIP)
	family := 2 // AF_INET
	if ip != nil && ip.To4() == nil {
		family = 10 // AF_INET6
	}

	srcAddr := conn.RemoteAddr().String()
	dstAddr := fmt.Sprintf("%s:%d", dstIP, dstPort)

	// Peek at the first bytes to extract the hostname (TLS SNI or HTTP Host).
	host, br := peekHost(conn, dstPort)
	if host != "" {
		log.Printf("c : Accept TCP: %s -> %s (%s).", srcAddr, dstAddr, host)
	} else {
		log.Printf("c : Accept TCP: %s -> %s.", srcAddr, dstAddr)
	}

	var connID uint64
	if counters != nil {
		connID = counters.ConnOpen(srcAddr, dstAddr, host)
		defer counters.ConnClose(connID, srcAddr, dstAddr)
	}

	// Open a mux channel.
	muxConn, err := client.OpenTCP(family, dstIP, dstPort)
	if err != nil {
		log.Printf("proxy: open channel to %s:%d: %v", dstIP, dstPort, err)
		return
	}
	defer muxConn.Close()

	// Bidirectional copy. Use the buffered reader (br) so peeked bytes are
	// replayed into the mux channel.
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(muxConn, br)
		muxConn.CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		io.Copy(conn, muxConn)
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}
