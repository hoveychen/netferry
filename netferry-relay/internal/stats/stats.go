// Package stats provides tunnel statistics collection and an HTTP/SSE server
// that streams live data to the desktop frontend.
package stats

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	healthLogInterval         = 30 * time.Second
	idleWarnAfter             = 45 * time.Second
	highActiveConnThreshold   = 128
	highConnOpenRateThreshold = 200
	highDNSRateThreshold      = 200
	highKeepaliveRTT          = 2 * time.Second
)

// TunnelCounters tracks per-tunnel metrics for a single pool member.
// Each MuxClient in a pool holds a pointer to its own TunnelCounters.
type TunnelCounters struct {
	RxTotal   atomic.Int64 // cumulative bytes downloaded via this tunnel
	TxTotal   atomic.Int64 // cumulative bytes uploaded via this tunnel
	ActiveTCP atomic.Int32 // currently open connections on this tunnel
	TotalTCP  atomic.Int64 // all-time connections opened on this tunnel

	lastRTTNs atomic.Int64 // most recent SSH keepalive RTT in nanoseconds (0 = unknown)
	maxRTTNs  atomic.Int64 // maximum SSH keepalive RTT seen on this tunnel
}

func (tc *TunnelCounters) AddRx(n int64) { tc.RxTotal.Add(n) }
func (tc *TunnelCounters) AddTx(n int64) { tc.TxTotal.Add(n) }

// ObserveRTT records a keepalive round-trip measurement for this tunnel.
func (tc *TunnelCounters) ObserveRTT(rtt time.Duration) {
	if rtt <= 0 {
		return
	}
	ns := rtt.Nanoseconds()
	tc.lastRTTNs.Store(ns)
	for {
		cur := tc.maxRTTNs.Load()
		if ns <= cur {
			return
		}
		if tc.maxRTTNs.CompareAndSwap(cur, ns) {
			return
		}
	}
}

// LastRTT returns the most recently observed keepalive RTT (0 if never measured).
func (tc *TunnelCounters) LastRTT() time.Duration {
	return time.Duration(tc.lastRTTNs.Load())
}

// TunnelSnapshot is the per-tunnel data embedded in Snapshot.
type TunnelSnapshot struct {
	Index            int   `json:"index"`            // 1-based pool member index
	RxBytesPerSec    int64 `json:"rxBytesPerSec"`    // download speed on this tunnel
	TxBytesPerSec    int64 `json:"txBytesPerSec"`    // upload speed on this tunnel
	ActiveConns      int32 `json:"activeConns"`      // currently open connections
	TotalConns       int64 `json:"totalConns"`       // all-time connections
	LastKeepaliveRtt int64 `json:"lastKeepaliveRtt"` // last SSH keepalive RTT in ms (0 = not yet measured)
	MaxKeepaliveRtt  int64 `json:"maxKeepaliveRtt"`  // max SSH keepalive RTT seen in ms
}

// Counters holds all live tunnel metrics. Fields are safe for concurrent use.
type Counters struct {
	RxTotal   atomic.Int64 // cumulative bytes received from remote (download)
	TxTotal   atomic.Int64 // cumulative bytes sent to remote (upload)
	ActiveTCP atomic.Int32 // currently open TCP channels
	TotalTCP  atomic.Int64 // all-time TCP connections opened
	DNSTotal  atomic.Int64 // all-time DNS queries forwarded

	nextConnID atomic.Uint64
	peakActive atomic.Int32

	lastRxAt  atomic.Int64
	lastTxAt  atomic.Int64
	lastDNSAt atomic.Int64

	lastKeepaliveRTTNs atomic.Int64
	maxKeepaliveRTTNs  atomic.Int64

	connEventCh chan ConnEvent // connection open/close notifications for SSE

	mu         sync.Mutex
	sseClients map[chan string]struct{}
	conns      map[uint64]*connStats

	tunnelsMu sync.RWMutex
	tunnels   []*TunnelCounters // per-pool-member counters; index 0 = pool member 1
}

// Snapshot is the JSON payload sent in each "stats" SSE event.
type Snapshot struct {
	RxBytesPerSec      int64            `json:"rxBytesPerSec"`
	TxBytesPerSec      int64            `json:"txBytesPerSec"`
	TotalRxBytes       int64            `json:"totalRxBytes"`
	TotalTxBytes       int64            `json:"totalTxBytes"`
	ActiveConns        int32            `json:"activeConns"`
	TotalConns         int64            `json:"totalConns"`
	DNSQueries         int64            `json:"dnsQueries"`
	PeakConns          int32            `json:"peakConns"`
	LastActivityMs     int64            `json:"lastActivityMs"`
	LastKeepaliveRttMs int64            `json:"lastKeepaliveRttMs"`
	MaxKeepaliveRttMs  int64            `json:"maxKeepaliveRttMs"`
	Tunnels            []TunnelSnapshot `json:"tunnels,omitempty"` // per-pool-member stats; empty when pool size == 1
}

// ConnEvent is the JSON payload sent in each "connection" SSE event.
type ConnEvent struct {
	ID          uint64 `json:"id"`
	Action      string `json:"action"` // "open" or "close"
	SrcAddr     string `json:"srcAddr"`
	DstAddr     string `json:"dstAddr"`
	Host        string `json:"host,omitempty"`        // resolved hostname (from SNI / HTTP Host / SOCKS5 domain)
	TunnelIndex int    `json:"tunnelIndex,omitempty"` // 1-based pool member; 0 = single tunnel or unknown
	TimestampMs int64  `json:"timestampMs"`
}

type connStats struct {
	srcAddr     string
	dstAddr     string
	host        string
	tunnelIndex int
	openedAt    time.Time
	rxBytes     int64
	txBytes     int64
}

// NewCounters allocates a ready-to-use Counters instance.
func NewCounters() *Counters {
	now := time.Now().UnixNano()
	c := &Counters{
		connEventCh: make(chan ConnEvent, 512),
		sseClients:  make(map[chan string]struct{}),
		conns:       make(map[uint64]*connStats),
	}
	c.lastRxAt.Store(now)
	c.lastTxAt.Store(now)
	c.lastDNSAt.Store(now)
	return c
}

// TunnelCounterAt returns the TunnelCounters for the given 1-based pool member
// index, or nil if not registered.
func (c *Counters) TunnelCounterAt(idx int) *TunnelCounters {
	c.tunnelsMu.RLock()
	defer c.tunnelsMu.RUnlock()
	if idx > 0 && idx <= len(c.tunnels) {
		return c.tunnels[idx-1]
	}
	return nil
}

// RegisterTunnel registers per-tunnel counters for the given 1-based pool
// member index. Must be called before the tunnel starts accepting connections.
// Returns the TunnelCounters that the MuxClient should use.
func (c *Counters) RegisterTunnel(idx int) *TunnelCounters {
	tc := &TunnelCounters{}
	c.tunnelsMu.Lock()
	for len(c.tunnels) < idx {
		c.tunnels = append(c.tunnels, nil)
	}
	c.tunnels[idx-1] = tc
	c.tunnelsMu.Unlock()
	return tc
}

// ConnOpen records a new TCP connection and queues an SSE "open" notification.
// Returns the connection ID that must be passed to ConnClose later.
// The host parameter is the resolved hostname (from SNI, HTTP Host header, or
// SOCKS5 domain); pass "" if unknown.
// tunnelIndex is the 1-based pool member index; pass 0 for single-tunnel mode.
func (c *Counters) ConnOpen(srcAddr, dstAddr, host string, tunnelIndex int) uint64 {
	id := c.nextConnID.Add(1)
	now := time.Now()
	c.mu.Lock()
	c.conns[id] = &connStats{
		srcAddr:     srcAddr,
		dstAddr:     dstAddr,
		host:        host,
		tunnelIndex: tunnelIndex,
		openedAt:    now,
	}
	c.mu.Unlock()
	select {
	case c.connEventCh <- ConnEvent{
		ID:          id,
		Action:      "open",
		SrcAddr:     srcAddr,
		DstAddr:     dstAddr,
		Host:        host,
		TunnelIndex: tunnelIndex,
		TimestampMs: now.UnixMilli(),
	}:
	default:
	}
	return id
}

// ConnClose queues an SSE "close" notification for a previously opened connection.
func (c *Counters) ConnClose(id uint64, srcAddr, dstAddr string) {
	c.mu.Lock()
	delete(c.conns, id)
	c.mu.Unlock()
	select {
	case c.connEventCh <- ConnEvent{
		ID:          id,
		Action:      "close",
		SrcAddr:     srcAddr,
		DstAddr:     dstAddr,
		TimestampMs: time.Now().UnixMilli(),
	}:
	default:
	}
}

// PushConnEvent is a backwards-compatible helper that fires an "open" event.
// Deprecated: prefer ConnOpen + ConnClose for full lifecycle tracking.
func (c *Counters) PushConnEvent(srcAddr, dstAddr string) {
	c.ConnOpen(srcAddr, dstAddr, "", 0)
}

func (c *Counters) AddRx(n int64) {
	if n <= 0 {
		return
	}
	c.RxTotal.Add(n)
	c.lastRxAt.Store(time.Now().UnixNano())
}

func (c *Counters) AddTx(n int64) {
	if n <= 0 {
		return
	}
	c.TxTotal.Add(n)
	c.lastTxAt.Store(time.Now().UnixNano())
}

func (c *Counters) AddDNS() {
	c.DNSTotal.Add(1)
	c.lastDNSAt.Store(time.Now().UnixNano())
}

func (c *Counters) ConnAddRx(id uint64, n int64) {
	if n <= 0 || id == 0 {
		return
	}
	c.mu.Lock()
	if cs, ok := c.conns[id]; ok {
		cs.rxBytes += n
	}
	c.mu.Unlock()
}

func (c *Counters) ConnAddTx(id uint64, n int64) {
	if n <= 0 || id == 0 {
		return
	}
	c.mu.Lock()
	if cs, ok := c.conns[id]; ok {
		cs.txBytes += n
	}
	c.mu.Unlock()
}

func (c *Counters) ObserveKeepaliveRTT(rtt time.Duration) {
	if rtt <= 0 {
		return
	}
	ns := rtt.Nanoseconds()
	c.lastKeepaliveRTTNs.Store(ns)
	for {
		cur := c.maxKeepaliveRTTNs.Load()
		if ns <= cur {
			return
		}
		if c.maxKeepaliveRTTNs.CompareAndSwap(cur, ns) {
			return
		}
	}
}

func (c *Counters) NoteActiveConns(active int32) {
	for {
		cur := c.peakActive.Load()
		if active <= cur {
			return
		}
		if c.peakActive.CompareAndSwap(cur, active) {
			return
		}
	}
}

func (c *Counters) LastActivityAgo(now time.Time) time.Duration {
	last := c.lastActivityTime()
	if last.IsZero() {
		return 0
	}
	return now.Sub(last)
}

func (c *Counters) LastKeepaliveRTT() time.Duration {
	return time.Duration(c.lastKeepaliveRTTNs.Load())
}

func (c *Counters) MaxKeepaliveRTT() time.Duration {
	return time.Duration(c.maxKeepaliveRTTNs.Load())
}

func (c *Counters) ConnectionSnapshot(id uint64) (openedAt time.Time, txBytes, rxBytes int64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cs, ok := c.conns[id]
	if !ok {
		return time.Time{}, 0, 0, false
	}
	return cs.openedAt, cs.txBytes, cs.rxBytes, true
}

// ListenAndServe starts the HTTP stats server on a loopback port.
// It tries preferredPort first; if unavailable (or 0), it falls back to an
// OS-assigned port. Returns the bound port. The server runs in background
// goroutines until the process exits.
func (c *Counters) ListenAndServe(preferredPort int) (int, error) {
	var ln net.Listener
	var err error
	if preferredPort > 0 {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferredPort))
	}
	if ln == nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 0, fmt.Errorf("stats: listen: %w", err)
		}
	}
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/events", c.handleSSE)
	mux.HandleFunc("/snapshot", c.handleSnapshot)

	go c.broadcaster()
	go func() {
		if err := http.Serve(ln, mux); err != nil {
			log.Printf("stats: server stopped: %v", err)
		}
	}()

	return port, nil
}

const connSnapshotInterval = 30 * time.Second

// broadcaster fans out SSE messages: stats once per second, connection events
// immediately as they arrive.
func (c *Counters) broadcaster() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var prevRx, prevTx, prevTotalConns, prevDNS int64
	// per-tunnel previous totals for speed calculation
	tunnelPrevRx := map[int]int64{}
	tunnelPrevTx := map[int]int64{}
	lastHealthLog := time.Now()
	lastConnSnapshot := time.Now()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			curRx := c.RxTotal.Load()
			curTx := c.TxTotal.Load()
			active := c.ActiveTCP.Load()
			c.NoteActiveConns(active)

			// Build per-tunnel snapshot.
			c.tunnelsMu.RLock()
			var tunnelSnaps []TunnelSnapshot
			for i, tc := range c.tunnels {
				if tc == nil {
					continue
				}
				curTRx := tc.RxTotal.Load()
				curTTx := tc.TxTotal.Load()
				tunnelSnaps = append(tunnelSnaps, TunnelSnapshot{
					Index:            i + 1,
					RxBytesPerSec:    curTRx - tunnelPrevRx[i],
					TxBytesPerSec:    curTTx - tunnelPrevTx[i],
					ActiveConns:      tc.ActiveTCP.Load(),
					TotalConns:       tc.TotalTCP.Load(),
					LastKeepaliveRtt: tc.LastRTT().Milliseconds(),
					MaxKeepaliveRtt:  time.Duration(tc.maxRTTNs.Load()).Milliseconds(),
				})
				tunnelPrevRx[i] = curTRx
				tunnelPrevTx[i] = curTTx
			}
			c.tunnelsMu.RUnlock()

			snap := Snapshot{
				RxBytesPerSec:      curRx - prevRx,
				TxBytesPerSec:      curTx - prevTx,
				TotalRxBytes:       curRx,
				TotalTxBytes:       curTx,
				ActiveConns:        active,
				TotalConns:         c.TotalTCP.Load(),
				DNSQueries:         c.DNSTotal.Load(),
				PeakConns:          c.peakActive.Load(),
				LastActivityMs:     c.LastActivityAgo(now).Milliseconds(),
				LastKeepaliveRttMs: c.LastKeepaliveRTT().Milliseconds(),
				MaxKeepaliveRttMs:  c.MaxKeepaliveRTT().Milliseconds(),
				Tunnels:            tunnelSnaps,
			}
			prevRx = curRx
			prevTx = curTx

			data, _ := json.Marshal(snap)
			c.broadcast(fmt.Sprintf("event: stats\ndata: %s\n\n", data))

			if now.Sub(lastHealthLog) >= healthLogInterval {
				openedPerWindow := snap.TotalConns - prevTotalConns
				dnsPerWindow := snap.DNSQueries - prevDNS
				lastHealthLog = now
				prevTotalConns = snap.TotalConns
				prevDNS = snap.DNSQueries
				c.logHealth(now, snap, openedPerWindow, dnsPerWindow)
			}

			if now.Sub(lastConnSnapshot) >= connSnapshotInterval {
				lastConnSnapshot = now
				c.mu.Lock()
				connSnap := c.buildConnSnapshotLocked()
				c.mu.Unlock()
				if data, err := json.Marshal(connSnap); err == nil {
					c.broadcast(fmt.Sprintf("event: connections_snapshot\ndata: %s\n\n", data))
				}
			}

		case ev := <-c.connEventCh:
			data, _ := json.Marshal(ev)
			c.broadcast(fmt.Sprintf("event: connection\ndata: %s\n\n", data))
		}
	}
}

func (c *Counters) broadcast(msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ch := range c.sseClients {
		select {
		case ch <- msg:
		default: // slow client; drop frame
		}
	}
}

// buildConnSnapshotLocked builds a snapshot of all currently open connections.
// Caller must hold c.mu.
func (c *Counters) buildConnSnapshotLocked() []ConnEvent {
	snap := make([]ConnEvent, 0, len(c.conns))
	for id, cs := range c.conns {
		snap = append(snap, ConnEvent{
			ID:          id,
			Action:      "open",
			SrcAddr:     cs.srcAddr,
			DstAddr:     cs.dstAddr,
			Host:        cs.host,
			TunnelIndex: cs.tunnelIndex,
			TimestampMs: cs.openedAt.UnixMilli(),
		})
	}
	return snap
}

func (c *Counters) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ch := make(chan string, 256)
	// Register and snapshot atomically so we never miss a close event.
	c.mu.Lock()
	c.sseClients[ch] = struct{}{}
	snapshot := c.buildConnSnapshotLocked()
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.sseClients, ch)
		c.mu.Unlock()
	}()

	// Send the initial snapshot so the client has authoritative state on connect.
	if len(snapshot) > 0 {
		if data, err := json.Marshal(snapshot); err == nil {
			fmt.Fprintf(w, "event: connections_snapshot\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}

	for {
		select {
		case msg := <-ch:
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (c *Counters) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	now := time.Now()
	snap := Snapshot{
		TotalRxBytes:       c.RxTotal.Load(),
		TotalTxBytes:       c.TxTotal.Load(),
		ActiveConns:        c.ActiveTCP.Load(),
		TotalConns:         c.TotalTCP.Load(),
		DNSQueries:         c.DNSTotal.Load(),
		PeakConns:          c.peakActive.Load(),
		LastActivityMs:     c.LastActivityAgo(now).Milliseconds(),
		LastKeepaliveRttMs: c.LastKeepaliveRTT().Milliseconds(),
		MaxKeepaliveRttMs:  c.MaxKeepaliveRTT().Milliseconds(),
	}
	json.NewEncoder(w).Encode(snap)
}

func (c *Counters) lastActivityTime() time.Time {
	last := c.lastRxAt.Load()
	if tx := c.lastTxAt.Load(); tx > last {
		last = tx
	}
	if dns := c.lastDNSAt.Load(); dns > last {
		last = dns
	}
	if last == 0 {
		return time.Time{}
	}
	return time.Unix(0, last)
}

func (c *Counters) logHealth(now time.Time, snap Snapshot, openedPerWindow, dnsPerWindow int64) {
	idle := c.LastActivityAgo(now)
	log.Printf(
		"health: active=%d peak=%d total=%d opened/%ds=%d dns/%ds=%d rx=%s/s tx=%s/s idle=%s keepalive_rtt=%s keepalive_rtt_max=%s",
		snap.ActiveConns,
		snap.PeakConns,
		snap.TotalConns,
		int(healthLogInterval/time.Second),
		openedPerWindow,
		int(healthLogInterval/time.Second),
		dnsPerWindow,
		formatBytesPerSec(snap.RxBytesPerSec),
		formatBytesPerSec(snap.TxBytesPerSec),
		idle.Round(time.Second),
		c.LastKeepaliveRTT().Round(time.Millisecond),
		c.MaxKeepaliveRTT().Round(time.Millisecond),
	)

	switch {
	case snap.ActiveConns >= highActiveConnThreshold:
		log.Printf("warning: tunnel pressure: active_conns=%d peak=%d rx=%s/s tx=%s/s", snap.ActiveConns, snap.PeakConns, formatBytesPerSec(snap.RxBytesPerSec), formatBytesPerSec(snap.TxBytesPerSec))
	case openedPerWindow >= highConnOpenRateThreshold:
		log.Printf("warning: tunnel pressure: connection churn is high, opened=%d over %s", openedPerWindow, healthLogInterval)
	case dnsPerWindow >= highDNSRateThreshold:
		log.Printf("warning: tunnel pressure: dns volume is high, queries=%d over %s", dnsPerWindow, healthLogInterval)
	case snap.ActiveConns > 0 && idle >= idleWarnAfter:
		log.Printf("warning: tunnel appears stalled: active_conns=%d idle=%s last_keepalive_rtt=%s", snap.ActiveConns, idle.Round(time.Second), c.LastKeepaliveRTT().Round(time.Millisecond))
	case c.LastKeepaliveRTT() >= highKeepaliveRTT:
		log.Printf("warning: tunnel keepalive RTT is high: rtt=%s active_conns=%d", c.LastKeepaliveRTT().Round(time.Millisecond), snap.ActiveConns)
	}
}

func formatBytesPerSec(v int64) string {
	if v < 1024 {
		return fmt.Sprintf("%dB", v)
	}
	const unit = 1024
	div, exp := int64(unit), 0
	for n := v / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(v)/float64(div), "KMGTPE"[exp])
}
