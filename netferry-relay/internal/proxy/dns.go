package proxy

import (
	"fmt"
	"log"
	"net"

	"github.com/hoveychen/netferry/relay/internal/mux"
)

// ListenDNS starts a UDP DNS interceptor on the given port.
// Queries are forwarded to the remote server via the mux DNS proxy.
func ListenDNS(port int, client *mux.MuxClient) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("dns listen :%d: %w", port, err)
	}
	defer conn.Close()

	log.Printf("proxy: DNS listening on :%d", port)
	buf := make([]byte, 4096)
	for {
		n, src, err := conn.ReadFrom(buf)
		if err != nil {
			return err
		}
		query := make([]byte, n)
		copy(query, buf[:n])
		go func(q []byte, srcAddr net.Addr) {
			resp, err := client.DNSRequest(q)
			if err != nil {
				log.Printf("proxy: DNS request: %v", err)
				return
			}
			conn.WriteTo(resp, srcAddr)
		}(query, src)
	}
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
