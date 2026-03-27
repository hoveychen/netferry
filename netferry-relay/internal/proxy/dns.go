package proxy

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// ServeDNS runs the DNS interceptor on a pre-bound PacketConn.
// Use this instead of ListenDNS to avoid the gap between firewall setup and
// socket binding (which causes ICMP port-unreachable → "DNS probe failed").
func ServeDNS(conn net.PacketConn, client *mux.MuxClient, counters *stats.Counters) error {
	defer conn.Close()

	log.Printf("proxy: DNS serving on %s", conn.LocalAddr())
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

// ListenDNS starts a UDP DNS interceptor on the given port.
// Prefer ServeDNS with a pre-bound listener when firewall rules are
// installed before the listener starts.
func ListenDNS(port int, client *mux.MuxClient, counters *stats.Counters) error {
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
