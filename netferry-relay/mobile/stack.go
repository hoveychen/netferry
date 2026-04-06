package mobile

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/proxy"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// tunStack runs a local SOCKS5 proxy + DNS relay that forward traffic through
// the mux tunnel. The native side (VpnService / NEPacketTunnelProvider) is
// responsible for setting up a TUN device and routing device traffic to these
// local ports.
//
// Architecture:
//
//	Device traffic → TUN (native) → SOCKS5 proxy (Go) → mux → SSH → remote
//	DNS queries    → TUN (native) → DNS relay  (Go)  → mux → SSH → remote
//
// This avoids a userspace TCP/IP stack (gVisor netstack) entirely, keeping
// the Go library lightweight and mobile-friendly.
type tunStack struct {
	socksLn net.Listener
	dnsConn net.PacketConn
	wg      sync.WaitGroup
}

// newTunStack starts a local SOCKS5 proxy and DNS relay, both forwarding
// through the mux tunnel. Returns immediately; servers run in background
// goroutines until Close() is called.
func newTunStack(cfg *Config, tunnel mux.TunnelClient, counters *stats.Counters) (*tunStack, error) {
	ts := &tunStack{}

	// ── SOCKS5 proxy ────────────────────────────────────────────────────────
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("socks5 listen: %w", err)
	}
	ts.socksLn = socksLn
	log.Printf("mobile: SOCKS5 proxy on %s", socksLn.Addr())

	ts.wg.Add(1)
	go func() {
		defer ts.wg.Done()
		proxy.ServeSOCKS5(socksLn, tunnel, counters)
	}()

	// ── DNS relay ───────────────────────────────────────────────────────────
	if cfg.dnsEnabled() {
		dnsConn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			socksLn.Close()
			return nil, fmt.Errorf("dns listen: %w", err)
		}
		ts.dnsConn = dnsConn
		log.Printf("mobile: DNS relay on %s", dnsConn.LocalAddr())

		ts.wg.Add(1)
		go func() {
			defer ts.wg.Done()
			if err := proxy.ServeDNS(dnsConn, tunnel, counters); err != nil {
				log.Printf("mobile: dns relay: %v", err)
			}
		}()
	}

	return ts, nil
}

// SOCKSPort returns the local SOCKS5 proxy port.
func (ts *tunStack) SOCKSPort() int {
	if ts.socksLn == nil {
		return 0
	}
	return ts.socksLn.Addr().(*net.TCPAddr).Port
}

// DNSPort returns the local DNS relay port, or 0 if DNS is not enabled.
func (ts *tunStack) DNSPort() int {
	if ts.dnsConn == nil {
		return 0
	}
	return ts.dnsConn.LocalAddr().(*net.UDPAddr).Port
}

// Close shuts down the SOCKS5 proxy and DNS relay.
func (ts *tunStack) Close() {
	if ts.socksLn != nil {
		ts.socksLn.Close()
	}
	if ts.dnsConn != nil {
		ts.dnsConn.Close()
	}
	ts.wg.Wait()
}

// buildDNSServFail creates a minimal DNS SERVFAIL response for the given query.
func buildDNSServFail(query []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	resp := make([]byte, 12)
	copy(resp, query[:2])                                                     // Transaction ID.
	binary.BigEndian.PutUint16(resp[2:], 0x8182)                              // Response, SERVFAIL.
	binary.BigEndian.PutUint16(resp[4:], binary.BigEndian.Uint16(query[4:6])) // QDCOUNT.
	return resp
}
