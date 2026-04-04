package mux

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// LBStrategy controls how TCP connections are assigned to pool members.
type LBStrategy int

const (
	// LBRoundRobin assigns TCP connections in round-robin order (default).
	LBRoundRobin LBStrategy = iota
	// LBLeastLoaded assigns TCP connections to the pool member with the fewest
	// open smux streams, mirroring the strategy already used for DNS/UDP.
	LBLeastLoaded
)

// TunnelClient is the interface satisfied by both MuxClient and MuxPool.
// The proxy layer uses this interface to open connections through the tunnel,
// keeping it decoupled from the underlying transport topology.
//
// priority is a hint (1=low, 3=normal, 5=high) that influences tunnel
// selection in a pool.  Single-tunnel implementations ignore it.
type TunnelClient interface {
	OpenTCP(family int, dstIP string, dstPort int, priority int) (*ClientConn, error)
	DNSRequest(data []byte) ([]byte, error)
	OpenUDP(family int) (*UDPChannel, error)
}

// MuxPool distributes new TCP connections across a fixed set of MuxClient
// instances in round-robin order. Each MuxClient runs on its own SSH TCP
// connection to the server, so aggregate throughput across many concurrent
// connections scales with pool size.
//
// DNS and UDP requests are routed to the least-loaded client (fewest open
// smux streams) to avoid queueing behind bulk TCP data on a congested
// connection. If all clients are equally idle the primary (clients[0]) is
// preferred.
//
// Note: connection bonding improves aggregate bandwidth when many channels
// are active simultaneously. It does NOT increase the throughput of a single
// channel — that would require per-frame striping with reorder buffers.
//
// Suggested pool size for 50 concurrent TCP connections: 2–4.
type MuxPool struct {
	mu          sync.RWMutex
	clients     []*MuxClient
	next        atomic.Uint64
	tcpStrategy LBStrategy
}

// NewMuxPool creates a pool with the default round-robin TCP strategy.
func NewMuxPool(clients []*MuxClient) *MuxPool {
	return NewMuxPoolWithStrategy(clients, LBRoundRobin)
}

// NewMuxPoolWithStrategy creates a pool from the given clients using the
// specified TCP load-balancing strategy.
func NewMuxPoolWithStrategy(clients []*MuxClient, strategy LBStrategy) *MuxPool {
	if len(clients) == 0 {
		panic("mux: NewMuxPool requires at least one client")
	}
	return &MuxPool{clients: clients, tcpStrategy: strategy}
}

func (p *MuxPool) pick() *MuxClient {
	p.mu.RLock()
	defer p.mu.RUnlock()

	n := uint64(len(p.clients))
	start := p.next.Add(1) - 1
	// Try all clients starting from the round-robin position; skip dead ones.
	for i := uint64(0); i < n; i++ {
		c := p.clients[(start+i)%n]
		if !c.IsClosed() {
			return c
		}
	}
	// All dead — return primary so the caller gets an error that propagates
	// up to the muxErrCh and triggers a full tunnel restart.
	return p.clients[0]
}

// ReplaceClient swaps the client at idx with a newly reconnected one.
func (p *MuxPool) ReplaceClient(idx int, c *MuxClient) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients[idx] = c
}

// OpenTCP picks a client according to the configured TCP strategy and opens a
// TCP channel. Default is round-robin; use LBLeastLoaded for least-loaded.
//
// The priority hint (1–5) influences tunnel selection:
//   - High priority (4–5): always picks the least-loaded tunnel for best quality
//   - Normal priority (3): uses the configured strategy
//   - Low priority (1–2): uses the most-loaded live tunnel, reserving
//     better tunnels for higher-priority traffic
func (p *MuxPool) OpenTCP(family int, dstIP string, dstPort int, priority int) (*ClientConn, error) {
	var client *MuxClient
	switch {
	case priority >= 4:
		client = p.pickLeastLoaded()
	case priority <= 2:
		client = p.pickMostLoaded()
	default:
		// Normal priority — use configured strategy.
		switch p.tcpStrategy {
		case LBLeastLoaded:
			client = p.pickLeastLoaded()
		default:
			client = p.pick()
		}
	}
	return client.OpenTCP(family, dstIP, dstPort, priority)
}

// congestionScore computes a quality score for a client.
// Lower score = better candidate for the next connection.
//
// Formula: streams × (1 + rtt_ms/50)
//
//   - A tunnel with 0 RTT data (not yet measured) is treated as 0ms, giving it
//     a pure stream-count score. This is intentional: new tunnels should be
//     considered as good candidates until proven otherwise.
//   - A tunnel with 10 streams and 10ms RTT scores 10×1.2 = 12.
//   - A tunnel with 8 streams and 100ms RTT scores 8×3.0 = 24.
//
// The RTT factor prevents a fast-but-overloaded tunnel from always winning
// over a slightly-less-loaded but high-latency peer.
func congestionScore(c *MuxClient) float64 {
	streams := float64(c.NumStreams())
	rttMs := float64(c.LastRTT() / time.Millisecond)
	return streams * (1.0 + rttMs/50.0)
}

// pickLeastLoaded returns the live client with the lowest congestion score.
// Score = streams × (1 + rtt_ms/50), so it jointly minimises open streams and
// keepalive latency. Falls back to clients[0] if all are dead.
func (p *MuxPool) pickLeastLoaded() *MuxClient {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var best *MuxClient
	bestScore := math.MaxFloat64
	for _, c := range p.clients {
		if c.IsClosed() {
			continue
		}
		if s := congestionScore(c); s < bestScore {
			best = c
			bestScore = s
		}
	}
	if best == nil {
		// All dead — return first so the caller gets an error.
		return p.clients[0]
	}
	return best
}

// pickMostLoaded returns the live client with the highest congestion score.
// Low-priority traffic is funneled here to keep the best tunnels available for
// high-priority destinations.  Falls back to clients[0] if all are dead.
func (p *MuxPool) pickMostLoaded() *MuxClient {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var best *MuxClient
	bestScore := -1.0
	for _, c := range p.clients {
		if c.IsClosed() {
			continue
		}
		if s := congestionScore(c); s > bestScore {
			best = c
			bestScore = s
		}
	}
	if best == nil {
		return p.clients[0]
	}
	return best
}

// DNSRequest routes through the least-loaded client (fewest open smux
// streams). This prevents DNS queries from queuing behind bulk TCP data on a
// congested connection, which would cause name-resolution timeouts.
func (p *MuxPool) DNSRequest(data []byte) ([]byte, error) {
	return p.pickLeastLoaded().DNSRequest(data)
}

// OpenUDP routes through the least-loaded client.
// UDP flows are typically low-volume and latency-sensitive.
func (p *MuxPool) OpenUDP(family int) (*UDPChannel, error) {
	return p.pickLeastLoaded().OpenUDP(family)
}
