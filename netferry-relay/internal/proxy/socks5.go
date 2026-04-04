// Package proxy — SOCKS5 server used on Windows where transparent packet
// interception requires kernel drivers. Applications configured to use the
// SOCKS5 proxy connect here; the destination is extracted from the SOCKS5
// handshake and forwarded through the mux tunnel.
package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// SOCKS5 protocol constants (RFC 1928).
const (
	socks5Version    = 5
	socks5AuthNone   = 0
	socks5CmdConnect = 1
	socks5AddrIPv4   = 1
	socks5AddrDomain = 3
	socks5AddrIPv6   = 4
	socks5ReplyOK    = 0
	socks5ReplyFail  = 1
)

// ListenSOCKS5 starts a SOCKS5 proxy on the given port and forwards all
// CONNECT requests through the mux tunnel. Blocks until the listener closes.
// This is the primary proxy mode on Windows.
func ListenSOCKS5(port int, client mux.TunnelClient, counters *stats.Counters) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("socks5 listen :%d: %w", port, err)
	}
	defer ln.Close()

	log.Printf("proxy: SOCKS5 listening on :%d", port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleSOCKS5(conn, client, counters)
	}
}

// handleSOCKS5 performs the SOCKS5 handshake and then proxies data.
func handleSOCKS5(conn net.Conn, client mux.TunnelClient, counters *stats.Counters) {
	defer conn.Close()
	startedAt := time.Now()

	dstIP, dstPort, err := socks5Handshake(conn)
	if err != nil {
		log.Printf("socks5: handshake: %v", err)
		return
	}

	// Determine address family.
	ip := net.ParseIP(dstIP)
	family := 2 // AF_INET
	if ip != nil && ip.To4() == nil {
		family = 10 // AF_INET6
	}

	srcAddr := conn.RemoteAddr().String()
	dstAddr := fmt.Sprintf("%s:%d", dstIP, dstPort)
	// For SOCKS5 domain addresses, dstIP is the hostname itself.
	host := ""
	if net.ParseIP(dstIP) == nil {
		host = dstIP
	}
	log.Printf("c : Accept TCP: %s -> %s.", srcAddr, dstAddr)

	priority := stats.DefaultPriority
	routeMode := stats.RouteTunnel
	if counters != nil {
		priority = counters.LookupPriority(dstAddr, host)
		routeMode = counters.LookupRouteMode(dstAddr, host)
	}

	switch routeMode {
	case stats.RouteBlocked:
		log.Printf("socks5: blocked %s -> %s", srcAddr, dstAddr)
		return
	case stats.RouteDirect:
		handleDirect(conn, conn, dstAddr, srcAddr, host, counters, startedAt)
		return
	}

	muxConn, err := client.OpenTCP(family, dstIP, dstPort, priority)
	if err != nil {
		log.Printf("socks5: open channel to %s:%d: %v", dstIP, dstPort, err)
		return
	}
	defer muxConn.Close()

	var connID uint64
	if counters != nil {
		connID = counters.ConnOpen(srcAddr, dstAddr, host, muxConn.TunnelIndex)
	}

	touch := func() {
		deadline := time.Now().Add(connIdleTimeout)
		conn.SetDeadline(deadline)
		muxConn.SetDeadline(deadline)
	}
	touch()

	done := make(chan copyResult, 2)
	go func() {
		n, err := io.Copy(&countingWriter{w: muxConn, touch: touch, onWrite: func(wrote int) {
			if counters != nil {
				counters.ConnAddTx(connID, int64(wrote))
			}
		}}, conn)
		muxConn.CloseWrite()
		done <- copyResult{direction: "upload", bytes: n, err: normalizeCopyErr(err)}
	}()
	go func() {
		n, err := io.Copy(&countingWriter{w: conn, touch: touch, onWrite: func(wrote int) {
			if counters != nil {
				counters.ConnAddRx(connID, int64(wrote))
			}
		}}, muxConn)
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- copyResult{direction: "download", bytes: n, err: normalizeCopyErr(err)}
	}()
	first := <-done
	second := <-done
	if counters != nil {
		counters.ConnClose(connID, srcAddr, dstAddr)
	}
	logConnSummary("socks5", connID, srcAddr, dstAddr, host, startedAt, first, second)
}

// socks5Handshake handles the SOCKS5 greeting and request, sends the reply,
// and returns the requested destination host and port.
func socks5Handshake(conn net.Conn) (host string, port int, err error) {
	// ── Greeting ─────────────────────────────────────────────────────────────
	// Client: VER NMETHODS METHODS...
	hdr := make([]byte, 2)
	if _, err = io.ReadFull(conn, hdr); err != nil {
		return
	}
	if hdr[0] != socks5Version {
		err = fmt.Errorf("unsupported SOCKS version %d", hdr[0])
		return
	}
	methods := make([]byte, hdr[1])
	if _, err = io.ReadFull(conn, methods); err != nil {
		return
	}
	// Server: VER METHOD (always choose NO AUTH)
	if _, err = conn.Write([]byte{socks5Version, socks5AuthNone}); err != nil {
		return
	}

	// ── Request ──────────────────────────────────────────────────────────────
	// Client: VER CMD RSV ATYP [DST.ADDR] DST.PORT
	req := make([]byte, 4)
	if _, err = io.ReadFull(conn, req); err != nil {
		return
	}
	if req[0] != socks5Version {
		err = fmt.Errorf("bad SOCKS5 version in request: %d", req[0])
		return
	}
	if req[1] != socks5CmdConnect {
		sendSOCKS5Reply(conn, 7) // command not supported
		err = fmt.Errorf("unsupported SOCKS5 command %d", req[1])
		return
	}

	var addrBytes []byte
	switch req[3] {
	case socks5AddrIPv4:
		addrBytes = make([]byte, 4)
		if _, err = io.ReadFull(conn, addrBytes); err != nil {
			return
		}
		host = net.IP(addrBytes).String()

	case socks5AddrIPv6:
		addrBytes = make([]byte, 16)
		if _, err = io.ReadFull(conn, addrBytes); err != nil {
			return
		}
		host = net.IP(addrBytes).String()

	case socks5AddrDomain:
		lenBuf := make([]byte, 1)
		if _, err = io.ReadFull(conn, lenBuf); err != nil {
			return
		}
		domain := make([]byte, lenBuf[0])
		if _, err = io.ReadFull(conn, domain); err != nil {
			return
		}
		host = string(domain)
		// Pass the domain name through to the mux layer so that DNS
		// resolution happens on the remote server. This is critical for
		// accessing internal hostnames only resolvable by the remote DNS.
		// The mux OpenTCP handler on the server side will resolve it.

	default:
		sendSOCKS5Reply(conn, 8) // address type not supported
		err = fmt.Errorf("unsupported SOCKS5 address type %d", req[3])
		return
	}

	portBuf := make([]byte, 2)
	if _, err = io.ReadFull(conn, portBuf); err != nil {
		return
	}
	port = int(binary.BigEndian.Uint16(portBuf))

	// ── Reply ─────────────────────────────────────────────────────────────────
	// Server: VER REP RSV ATYP BND.ADDR BND.PORT (use 0.0.0.0:0 as bound addr)
	sendSOCKS5Reply(conn, socks5ReplyOK)
	return
}

func sendSOCKS5Reply(conn net.Conn, rep byte) {
	// VER REP RSV ATYP(IPv4) BND.ADDR(4 bytes) BND.PORT(2 bytes)
	reply := []byte{socks5Version, rep, 0, socks5AddrIPv4, 0, 0, 0, 0, 0, 0}
	conn.Write(reply)
}
