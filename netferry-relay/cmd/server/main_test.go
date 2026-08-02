package main

import (
	"net"
	"testing"
	"time"

	"github.com/hoveychen/netferry/relay/internal/mux"
)

func TestHandleDNSUpstreamTimeout(t *testing.T) {
	if dnsUpstreamTimeout >= mux.DNSFirstAttemptTimeout {
		t.Fatalf("server DNS timeout %s must be shorter than client first-attempt timeout %s",
			dnsUpstreamTimeout, mux.DNSFirstAttemptTimeout)
	}

	clientConn, resolverConn := net.Pipe()
	defer clientConn.Close()
	defer resolverConn.Close()
	received := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 12)
		if n, err := resolverConn.Read(buf); err == nil {
			received <- buf[:n]
		}
	}()

	query := []byte{
		0x12, 0x34, 0x01, 0x00,
		0x00, 0x01, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
	started := time.Now()
	response, err := forwardDNSWithDial(query, "resolver.test:53", 100*time.Millisecond,
		func(network, address string, timeout time.Duration) (net.Conn, error) {
			if network != "udp" || address != "resolver.test:53" {
				t.Fatalf("dial = %s %s, want udp resolver.test:53", network, address)
			}
			return clientConn, nil
		})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("forwardDNS succeeded against a blackhole resolver")
	}
	if elapsed >= time.Second {
		t.Fatalf("forwardDNS returned after %s, want under 1s", elapsed)
	}
	select {
	case got := <-received:
		if string(got) != string(query) {
			t.Fatalf("resolver query = %v, want %v", got, query)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("fake resolver did not receive the DNS query")
	}
	if len(response) < 4 || response[3]&0x0f != 2 {
		t.Fatalf("response = %v, want DNS SERVFAIL (RCODE=2)", response)
	}
}
