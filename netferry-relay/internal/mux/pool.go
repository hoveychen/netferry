package mux

import "sync/atomic"

// TunnelClient is the interface satisfied by both MuxClient and MuxPool.
// The proxy layer uses this interface to open connections through the tunnel,
// keeping it decoupled from the underlying transport topology.
type TunnelClient interface {
	OpenTCP(family int, dstIP string, dstPort int) (*ClientConn, error)
	DNSRequest(data []byte) ([]byte, error)
	OpenUDP(family int) (*UDPChannel, error)
}

// MuxPool distributes new TCP connections across a fixed set of MuxClient
// instances in round-robin order. Each MuxClient runs on its own SSH TCP
// connection to the server, so aggregate throughput across many concurrent
// connections scales with pool size.
//
// DNS and UDP requests are always routed through clients[0] (the primary
// connection) to avoid timeouts from distributing latency-sensitive requests
// across connections with unequal congestion.
//
// Note: connection bonding improves aggregate bandwidth when many channels
// are active simultaneously. It does NOT increase the throughput of a single
// channel — that would require per-frame striping with reorder buffers.
//
// Suggested pool size for 50 concurrent TCP connections: 2–4.
type MuxPool struct {
	clients []*MuxClient
	next    atomic.Uint64
}

// NewMuxPool creates a pool from the given clients. All clients should
// already have Run() called (or be about to be started in goroutines).
func NewMuxPool(clients []*MuxClient) *MuxPool {
	if len(clients) == 0 {
		panic("mux: NewMuxPool requires at least one client")
	}
	return &MuxPool{clients: clients}
}

func (p *MuxPool) pick() *MuxClient {
	idx := p.next.Add(1) - 1
	return p.clients[idx%uint64(len(p.clients))]
}

// OpenTCP picks the next client in round-robin order and opens a TCP channel.
func (p *MuxPool) OpenTCP(family int, dstIP string, dstPort int) (*ClientConn, error) {
	return p.pick().OpenTCP(family, dstIP, dstPort)
}

// DNSRequest routes through the primary client (clients[0]) only.
// DNS is latency-sensitive; round-robining across connections with unequal
// congestion causes timeouts that stall name resolution for the whole system.
func (p *MuxPool) DNSRequest(data []byte) ([]byte, error) {
	return p.clients[0].DNSRequest(data)
}

// OpenUDP routes through the primary client (clients[0]) only.
// UDP flows are typically low-volume and latency-sensitive.
func (p *MuxPool) OpenUDP(family int) (*UDPChannel, error) {
	return p.clients[0].OpenUDP(family)
}
