package proxy

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// FilterAAAA, when true, makes ServeDNS short-circuit AAAA (IPv6 address)
// queries with an empty NoError response instead of forwarding them. This
// pairs with the firewall's IPv6 block: stopping AAAA at the resolver keeps
// applications from even attempting IPv6, which avoids the connect-timeout
// pause that Happy Eyeballs would otherwise hit when the block kicks in.
var FilterAAAA bool

// dnsTypeAAAA is the QTYPE for IPv6 address records (RFC 3596).
const dnsTypeAAAA = 28

// ServeDNS runs the DNS interceptor on a pre-bound PacketConn.
// Use this instead of ListenDNS to avoid the gap between firewall setup and
// socket binding (which causes ICMP port-unreachable → "DNS probe failed").
func ServeDNS(conn net.PacketConn, client mux.TunnelClient, counters *stats.Counters) error {
	defer conn.Close()

	log.Printf("proxy: DNS serving on %s (filterAAAA=%v)", conn.LocalAddr(), FilterAAAA)
	buf := make([]byte, 65535)
	for {
		n, src, err := conn.ReadFrom(buf)
		if err != nil {
			return err
		}
		query := make([]byte, n)
		copy(query, buf[:n])
		if counters != nil {
			counters.AddDNS()
		}
		// Short-circuit AAAA queries when IPv6 is disabled. The reply is an
		// empty NoError answer — semantically "this name has no IPv6 address"
		// — which makes the resolver fall back to A immediately.
		if FilterAAAA && isAAAAQuery(query) {
			if reply := buildEmptyNoError(query); reply != nil {
				conn.WriteTo(reply, src)
			}
			continue
		}
		go func(q []byte, srcAddr net.Addr) {
			resp, err := client.DNSRequest(q)
			if err != nil {
				log.Printf("proxy: DNS request: %v", err)
				// Send a SERVFAIL response so the client retries quickly
				// instead of waiting for a full timeout.
				if fail := buildDNSServFail(q); fail != nil {
					conn.WriteTo(fail, srcAddr)
				}
				return
			}
			conn.WriteTo(resp, srcAddr)
		}(query, src)
	}
}

// isAAAAQuery returns true if the DNS message contains exactly one question
// with QTYPE=AAAA. Accepts only well-formed single-question queries — anything
// unusual is forwarded unchanged.
func isAAAAQuery(msg []byte) bool {
	if len(msg) < 12 {
		return false
	}
	flags := binary.BigEndian.Uint16(msg[2:4])
	if flags&0x8000 != 0 {
		return false // response, not query
	}
	if binary.BigEndian.Uint16(msg[4:6]) != 1 {
		return false // QDCOUNT must be 1
	}
	// Skip QNAME: sequence of length-prefixed labels terminated by 0.
	pos := 12
	for pos < len(msg) {
		l := int(msg[pos])
		if l == 0 {
			pos++
			break
		}
		if l&0xc0 != 0 {
			return false // pointer compression in query QNAME — bail out
		}
		pos += 1 + l
	}
	if pos+4 > len(msg) {
		return false
	}
	qtype := binary.BigEndian.Uint16(msg[pos : pos+2])
	return qtype == dnsTypeAAAA
}

// buildEmptyNoError constructs a DNS response that echoes the question with
// zero answer/authority/additional records and RCODE=0 (NoError). Returns nil
// if the query is too short to read the question section.
func buildEmptyNoError(query []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	// Find end of question section (header + QNAME + QTYPE/QCLASS).
	pos := 12
	for pos < len(query) {
		l := int(query[pos])
		if l == 0 {
			pos++
			break
		}
		if l&0xc0 != 0 {
			return nil
		}
		pos += 1 + l
	}
	if pos+4 > len(query) {
		return nil
	}
	qend := pos + 4

	resp := make([]byte, qend)
	copy(resp, query[:qend])
	// Flags: QR=1, Opcode preserved, AA=0, TC=0, RD preserved, RA=1, RCODE=0.
	flags := binary.BigEndian.Uint16(query[2:4])
	opcode := flags & 0x7800
	rd := flags & 0x0100
	binary.BigEndian.PutUint16(resp[2:4], 0x8080|opcode|rd)
	// QDCOUNT preserved (=1); ANCOUNT/NSCOUNT/ARCOUNT zero.
	binary.BigEndian.PutUint16(resp[6:8], 0)
	binary.BigEndian.PutUint16(resp[8:10], 0)
	binary.BigEndian.PutUint16(resp[10:12], 0)
	return resp
}

// ListenDNS starts a UDP DNS interceptor on the given port.
// Prefer ServeDNS with a pre-bound listener when firewall rules are
// installed before the listener starts.
func ListenDNS(port int, client mux.TunnelClient, counters *stats.Counters) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("dns listen :%d: %w", port, err)
	}
	return ServeDNS(conn, client, counters)
}

// buildDNSServFail creates a minimal DNS SERVFAIL response for the given query.
// Returns nil if the query is too short to be a valid DNS packet.
func buildDNSServFail(query []byte) []byte {
	if len(query) < 12 {
		return nil // too short for a DNS header
	}
	resp := make([]byte, 12)
	// Copy the transaction ID from the query.
	copy(resp[0:2], query[0:2])
	// Flags: QR=1 (response), Opcode from query, RCODE=2 (SERVFAIL).
	flags := binary.BigEndian.Uint16(query[2:4])
	opcode := flags & 0x7800                             // preserve opcode bits
	binary.BigEndian.PutUint16(resp[2:4], 0x8002|opcode) // QR=1, RCODE=SERVFAIL
	// QDCOUNT=0, ANCOUNT=0, NSCOUNT=0, ARCOUNT=0 (already zero).
	return resp
}

// DetectDNSServers returns the system's DNS server IPs.
// On macOS, tries scutil; falls back to /etc/resolv.conf everywhere.
func DetectDNSServers() []string {
	servers := detectDNSPlatform()
	if len(servers) == 0 {
		servers = parseResolvConf()
	}
	if len(servers) == 0 {
		servers = []string{"8.8.8.8", "8.8.4.4"}
	}
	return servers
}

func parseResolvConf() []string {
	data, err := readFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	var servers []string
	for _, line := range splitLines(data) {
		if len(line) > 11 && line[:11] == "nameserver " {
			ip := trimSpace(line[11:])
			if net.ParseIP(ip) != nil {
				servers = append(servers, ip)
			}
		}
	}
	return servers
}
