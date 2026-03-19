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

	"github.com/hoveychen/netferry/relay/internal/mux"
)

// SOCKS5 protocol constants (RFC 1928).
const (
	socks5Version     = 5
	socks5AuthNone    = 0
	socks5CmdConnect  = 1
	socks5AddrIPv4    = 1
	socks5AddrDomain  = 3
	socks5AddrIPv6    = 4
	socks5ReplyOK     = 0
	socks5ReplyFail   = 1
)

// ListenSOCKS5 starts a SOCKS5 proxy on the given port and forwards all
// CONNECT requests through the mux tunnel. Blocks until the listener closes.
// This is the primary proxy mode on Windows.
func ListenSOCKS5(port int, client *mux.MuxClient) error {
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
		go handleSOCKS5(conn, client)
	}
}

// handleSOCKS5 performs the SOCKS5 handshake and then proxies data.
func handleSOCKS5(conn net.Conn, client *mux.MuxClient) {
	defer conn.Close()

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
	log.Printf("c : Accept TCP: %s -> %s:%d.", srcAddr, dstIP, dstPort)

	muxConn, err := client.OpenTCP(family, dstIP, dstPort)
	if err != nil {
		log.Printf("socks5: open channel to %s:%d: %v", dstIP, dstPort, err)
		return
	}
	defer muxConn.Close()

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(muxConn, conn)
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
		// Resolve domain to IP for the mux protocol.
		addrs, rerr := net.LookupHost(host)
		if rerr != nil || len(addrs) == 0 {
			sendSOCKS5Reply(conn, socks5ReplyFail)
			err = fmt.Errorf("DNS resolve %q: %v", host, rerr)
			return
		}
		host = addrs[0]

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
