// Package stats provides tunnel statistics collection and an HTTP/SSE server
// that streams live data to the desktop frontend.
package stats

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"sort"
	"strings"
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

// rttWindowSize is the number of recent RTT samples kept for min/jitter computation.
// With a 30-second keepalive interval this covers ~5 minutes.
const rttWindowSize = 10

// TunnelState represents the lifecycle state of a pool member.
type TunnelState int32

const (
	TunnelAlive        TunnelState = 0 // connected and healthy
	TunnelReconnecting TunnelState = 1 // SSH connection lost, attempting to reconnect
	TunnelDead         TunnelState = 2 // gave up reconnecting
)

// TunnelCounters tracks per-tunnel metrics for a single pool member.
// Each MuxClient in a pool holds a pointer to its own TunnelCounters.
type TunnelCounters struct {
	RxTotal   atomic.Int64 // cumulative bytes downloaded via this tunnel
	TxTotal   atomic.Int64 // cumulative bytes uploaded via this tunnel
	ActiveTCP atomic.Int32 // currently open connections on this tunnel
	TotalTCP  atomic.Int64 // all-time connections opened on this tunnel

	state     atomic.Int32 // TunnelState: 0=alive, 1=reconnecting, 2=dead
	lastRTTNs atomic.Int64 // most recent SSH keepalive RTT in nanoseconds (0 = unknown)
	maxRTTNs  atomic.Int64 // maximum SSH keepalive RTT seen on this tunnel

	rttMu      sync.Mutex
	rttRing    [rttWindowSize]int64 // circular buffer of recent RTT values (ns)
	rttRingPos int                  // next write position
	rttCount   int                  // total samples written (min(count, windowSize) are valid)
	prevRTTNs  int64               // previous RTT for jitter computation
	jitterNs   atomic.Int64        // |last - prev| in nanoseconds
}

func (tc *TunnelCounters) AddRx(n int64)            { tc.RxTotal.Add(n) }
func (tc *TunnelCounters) AddTx(n int64)            { tc.TxTotal.Add(n) }
func (tc *TunnelCounters) SetState(s TunnelState)   { tc.state.Store(int32(s)) }
func (tc *TunnelCounters) State() TunnelState        { return TunnelState(tc.state.Load()) }

// ObserveRTT records a keepalive round-trip measurement for this tunnel.
func (tc *TunnelCounters) ObserveRTT(rtt time.Duration) {
	if rtt <= 0 {
		return
	}
	ns := rtt.Nanoseconds()
	tc.lastRTTNs.Store(ns)

	// Update max.
	for {
		cur := tc.maxRTTNs.Load()
		if ns <= cur {
			break
		}
		if tc.maxRTTNs.CompareAndSwap(cur, ns) {
			break
		}
	}

	// Update ring buffer and jitter.
	tc.rttMu.Lock()
	tc.rttRing[tc.rttRingPos] = ns
	tc.rttRingPos = (tc.rttRingPos + 1) % rttWindowSize
	tc.rttCount++

	if tc.prevRTTNs > 0 {
		diff := ns - tc.prevRTTNs
		if diff < 0 {
			diff = -diff
		}
		tc.jitterNs.Store(diff)
	}
	tc.prevRTTNs = ns
	tc.rttMu.Unlock()
}

// LastRTT returns the most recently observed keepalive RTT (0 if never measured).
func (tc *TunnelCounters) LastRTT() time.Duration {
	return time.Duration(tc.lastRTTNs.Load())
}

// MinRTT returns the minimum RTT in the recent sliding window (0 if no data).
func (tc *TunnelCounters) MinRTT() time.Duration {
	tc.rttMu.Lock()
	defer tc.rttMu.Unlock()
	n := tc.rttCount
	if n > rttWindowSize {
		n = rttWindowSize
	}
	if n == 0 {
		return 0
	}
	minNs := tc.rttRing[0]
	for i := 1; i < n; i++ {
		if tc.rttRing[i] < minNs {
			minNs = tc.rttRing[i]
		}
	}
	return time.Duration(minNs)
}

// Jitter returns the absolute difference between the two most recent RTT samples.
func (tc *TunnelCounters) Jitter() time.Duration {
	return time.Duration(tc.jitterNs.Load())
}

// TunnelSnapshot is the per-tunnel data embedded in Snapshot.
type TunnelSnapshot struct {
	Index           int     `json:"index"`           // 1-based pool member index
	State           string  `json:"state"`           // "alive", "reconnecting", or "dead"
	RxBytesPerSec   int64   `json:"rxBytesPerSec"`   // download speed on this tunnel
	TxBytesPerSec   int64   `json:"txBytesPerSec"`   // upload speed on this tunnel
	ActiveConns     int32   `json:"activeConns"`     // currently open connections
	TotalConns      int64   `json:"totalConns"`      // all-time connections
	LastRttUs       int64   `json:"lastRttUs"`       // last SSH keepalive RTT in µs (0 = not yet measured)
	MinRttUs        int64   `json:"minRttUs"`        // min RTT over recent window in µs (network floor)
	MaxRttUs        int64   `json:"maxRttUs"`        // max RTT in µs
	JitterUs        int64   `json:"jitterUs"`        // |last - prev| in µs
	CongestionScore float64 `json:"congestionScore"` // streams × (1 + rtt_ms/50); lower = less loaded
}

func tunnelStateString(s TunnelState) string {
	switch s {
	case TunnelReconnecting:
		return "reconnecting"
	case TunnelDead:
		return "dead"
	default:
		return "alive"
	}
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
	dests      map[string]*destStats // per-destination aggregates keyed by normalised host/IP
	priorities map[string]int        // per-destination priority (1=low, 3=normal, 5=high)
	routeModes map[string]RouteMode  // per-destination route mode (tunnel/direct/blocked)

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
	destKey     string // normalized destination key used for destStats lookup
}

// destStats tracks per-destination aggregate metrics.
type destStats struct {
	host         string // display name: SNI hostname or fallback IP
	activeConns  int32
	totalConns   int64
	rxBytes      int64
	txBytes      int64
	firstSeenAt  time.Time
	lastSeenAt   time.Time
	processNames map[string]struct{} // unique process names that accessed this destination
}

// DestinationSnapshot is the per-destination data sent in the "destinations_snapshot" SSE event.
type DestinationSnapshot struct {
	Host          string `json:"host"`          // hostname or IP
	ActiveConns   int32  `json:"activeConns"`   // currently open connections
	TotalConns    int64  `json:"totalConns"`    // all-time connections opened
	RxBytes       int64  `json:"rxBytes"`       // cumulative bytes downloaded
	TxBytes       int64  `json:"txBytes"`       // cumulative bytes uploaded
	RxBytesPerSec int64  `json:"rxBytesPerSec"` // download speed (calculated by broadcaster)
	TxBytesPerSec int64  `json:"txBytesPerSec"` // upload speed (calculated by broadcaster)
	FirstSeenMs   int64  `json:"firstSeenMs"`   // timestamp of first connection
	LastSeenMs    int64  `json:"lastSeenMs"`    // timestamp of last activity
	Priority      int       `json:"priority"`                // 1=low, 3=normal (default), 5=high
	Route         RouteMode `json:"route"`                   // tunnel, direct, or blocked
	ProcessNames  []string  `json:"processNames,omitempty"`  // local processes that connected to this destination
}

// NewCounters allocates a ready-to-use Counters instance.
func NewCounters() *Counters {
	now := time.Now().UnixNano()
	c := &Counters{
		connEventCh: make(chan ConnEvent, 512),
		sseClients:  make(map[chan string]struct{}),
		conns:       make(map[uint64]*connStats),
		dests:       make(map[string]*destStats),
		priorities:  make(map[string]int),
		routeModes:  make(map[string]RouteMode),
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

// destKey returns a normalised key for per-destination aggregation.
// Prefers the resolved hostname (SNI / HTTP Host); falls back to the IP
// portion of dstAddr.
func destKey(dstAddr, host string) string {
	if host != "" {
		return host
	}
	// dstAddr is "ip:port" — strip the port.
	if idx := strings.LastIndex(dstAddr, ":"); idx > 0 {
		return dstAddr[:idx]
	}
	return dstAddr
}

// ConnOpen records a new TCP connection and queues an SSE "open" notification.
// Returns the connection ID that must be passed to ConnClose later.
// The host parameter is the resolved hostname (from SNI, HTTP Host header, or
// SOCKS5 domain); pass "" if unknown.
// tunnelIndex is the 1-based pool member index; pass 0 for single-tunnel mode.
func (c *Counters) ConnOpen(srcAddr, dstAddr, host string, tunnelIndex int) uint64 {
	id := c.nextConnID.Add(1)
	now := time.Now()
	dk := destKey(dstAddr, host)
	displayHost := dk
	if host != "" {
		displayHost = host
	}
	c.mu.Lock()
	c.conns[id] = &connStats{
		srcAddr:     srcAddr,
		dstAddr:     dstAddr,
		host:        host,
		tunnelIndex: tunnelIndex,
		openedAt:    now,
		destKey:     dk,
	}
	ds, ok := c.dests[dk]
	if !ok {
		ds = &destStats{host: displayHost, firstSeenAt: now}
		c.dests[dk] = ds
	}
	ds.activeConns++
	ds.totalConns++
	ds.lastSeenAt = now
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
	if cs, ok := c.conns[id]; ok {
		if ds, ok2 := c.dests[cs.destKey]; ok2 {
			ds.activeConns--
			ds.lastSeenAt = time.Now()
		}
	}
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
		if ds, ok2 := c.dests[cs.destKey]; ok2 {
			ds.rxBytes += n
		}
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
		if ds, ok2 := c.dests[cs.destKey]; ok2 {
			ds.txBytes += n
		}
	}
	c.mu.Unlock()
}

// RouteMode controls how a destination's traffic is handled.
type RouteMode string

const (
	RouteTunnel  RouteMode = "tunnel"  // default: proxy through tunnel
	RouteDirect  RouteMode = "direct"  // bypass tunnel, connect directly
	RouteBlocked RouteMode = "blocked" // reject connection
)

// DefaultPriority is used when no explicit priority has been set for a destination.
const DefaultPriority = 3

// LookupPriority returns the priority for a destination key (1–5).
// Returns DefaultPriority if not explicitly set.
func (c *Counters) LookupPriority(dstAddr, host string) int {
	dk := destKey(dstAddr, host)
	c.mu.Lock()
	p, ok := c.priorities[dk]
	c.mu.Unlock()
	if !ok {
		return DefaultPriority
	}
	return p
}

// SetPriorities replaces all destination priorities at once.
func (c *Counters) SetPriorities(m map[string]int) {
	c.mu.Lock()
	c.priorities = m
	c.mu.Unlock()
}

// SetPriority sets the priority for a single destination key.
func (c *Counters) SetPriority(host string, priority int) {
	if priority < 1 {
		priority = 1
	}
	if priority > 5 {
		priority = 5
	}
	c.mu.Lock()
	if priority == DefaultPriority {
		delete(c.priorities, host)
	} else {
		c.priorities[host] = priority
	}
	c.mu.Unlock()
}

// Priorities returns a copy of the current priority map.
func (c *Counters) Priorities() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := make(map[string]int, len(c.priorities))
	for k, v := range c.priorities {
		m[k] = v
	}
	return m
}

// LookupRouteMode returns the route mode for a destination key.
// Returns RouteTunnel if not explicitly set.
func (c *Counters) LookupRouteMode(dstAddr, host string) RouteMode {
	dk := destKey(dstAddr, host)
	c.mu.Lock()
	m := c.routeModes[dk]
	c.mu.Unlock()
	if m == "" {
		return RouteTunnel
	}
	return m
}

// lookupRouteModeLocked returns the route mode; caller must hold c.mu.
func (c *Counters) lookupRouteModeLocked(key string) RouteMode {
	m := c.routeModes[key]
	if m == "" {
		return RouteTunnel
	}
	return m
}

// SetRouteModes replaces all route modes at once.
func (c *Counters) SetRouteModes(m map[string]RouteMode) {
	c.mu.Lock()
	c.routeModes = m
	c.mu.Unlock()
}

// RouteModes returns a copy of the current route mode map.
func (c *Counters) RouteModes() map[string]RouteMode {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := make(map[string]RouteMode, len(c.routeModes))
	for k, v := range c.routeModes {
		m[k] = v
	}
	return m
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
	mux.HandleFunc("/priorities", c.handlePriorities)
	mux.HandleFunc("/routes", c.handleRoutes)

	go c.broadcaster()
	go func() {
		if err := http.Serve(ln, mux); err != nil {
			log.Printf("stats: server stopped: %v", err)
		}
	}()

	return port, nil
}

const connSnapshotInterval = 3 * time.Second

// broadcaster fans out SSE messages: stats once per second, connection events
// immediately as they arrive.
func (c *Counters) broadcaster() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var prevRx, prevTx, prevTotalConns, prevDNS int64
	// per-tunnel previous totals for speed calculation
	tunnelPrevRx := map[int]int64{}
	tunnelPrevTx := map[int]int64{}
	// per-destination previous totals for speed calculation
	destPrevRx := map[string]int64{}
	destPrevTx := map[string]int64{}
	lastHealthLog := time.Now()
	lastConnSnapshot := time.Now()
	lastDestSnapshot := time.Now()

	// Asynchronous process-name lookup: results are delivered via channel
	// so the broadcaster is never blocked by lsof/netstat.
	type procResult struct {
		procs        map[string]string
		srcToDestKey map[string]string
	}
	procResultCh := make(chan procResult, 1)
	procLookupRunning := false

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
				lastRTT := tc.LastRTT()
				rttMs := float64(lastRTT) / float64(time.Millisecond)
				activeConns := tc.ActiveTCP.Load()
				congestion := float64(activeConns) * (1.0 + rttMs/50.0)
				tunnelSnaps = append(tunnelSnaps, TunnelSnapshot{
					Index:           i + 1,
					State:           tunnelStateString(tc.State()),
					RxBytesPerSec:   curTRx - tunnelPrevRx[i],
					TxBytesPerSec:   curTTx - tunnelPrevTx[i],
					ActiveConns:     activeConns,
					TotalConns:      tc.TotalTCP.Load(),
					LastRttUs:       lastRTT.Microseconds(),
					MinRttUs:        tc.MinRTT().Microseconds(),
					MaxRttUs:        time.Duration(tc.maxRTTNs.Load()).Microseconds(),
					JitterUs:        tc.Jitter().Microseconds(),
					CongestionScore: math.Round(congestion*10) / 10,
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

			if now.Sub(lastDestSnapshot) >= destSnapshotInterval {
				elapsed := now.Sub(lastDestSnapshot).Seconds()
				lastDestSnapshot = now

				if !procLookupRunning {
					c.mu.Lock()
					srcToDestKey := make(map[string]string, len(c.conns))
					addrSet := make(map[string]struct{}, len(c.conns))
					for _, cs := range c.conns {
						srcToDestKey[cs.srcAddr] = cs.destKey
						addrSet[cs.srcAddr] = struct{}{}
					}
					c.mu.Unlock()
					procLookupRunning = true
					go func() {
						procs := lookupProcesses(addrSet)
						procResultCh <- procResult{procs: procs, srcToDestKey: srcToDestKey}
					}()
				}

				// Build and send destination snapshot using whatever process
				// names have already been merged (from previous lookups).
				c.mu.Lock()
				destSnap := c.buildDestSnapshotLocked(destPrevRx, destPrevTx, elapsed)
				for key, ds := range c.dests {
					destPrevRx[key] = ds.rxBytes
					destPrevTx[key] = ds.txBytes
				}
				c.mu.Unlock()
				if data, err := json.Marshal(destSnap); err == nil {
					c.broadcast(fmt.Sprintf("event: destinations_snapshot\ndata: %s\n\n", data))
				}
			}

		case pr := <-procResultCh:
			procLookupRunning = false
			c.mu.Lock()
			for srcAddr, procName := range pr.procs {
				if dk, ok := pr.srcToDestKey[srcAddr]; ok {
					if ds, ok2 := c.dests[dk]; ok2 {
						if ds.processNames == nil {
							ds.processNames = make(map[string]struct{})
						}
						ds.processNames[procName] = struct{}{}
					}
				}
			}
			c.mu.Unlock()

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

const destSnapshotInterval = 5 * time.Second

// buildDestSnapshotLocked builds a snapshot of all destination stats, sorted by
// total bytes descending. Caller must hold c.mu.
func (c *Counters) buildDestSnapshotLocked(prevRx, prevTx map[string]int64, elapsed float64) []DestinationSnapshot {
	snaps := make([]DestinationSnapshot, 0, len(c.dests))
	for key, ds := range c.dests {
		rxPerSec := int64(0)
		txPerSec := int64(0)
		if elapsed > 0 {
			rxPerSec = int64(float64(ds.rxBytes-prevRx[key]) / elapsed)
			txPerSec = int64(float64(ds.txBytes-prevTx[key]) / elapsed)
		}
		prio := c.priorities[key]
		if prio == 0 {
			prio = DefaultPriority
		}
		snaps = append(snaps, DestinationSnapshot{
			Host:          ds.host,
			ActiveConns:   ds.activeConns,
			TotalConns:    ds.totalConns,
			RxBytes:       ds.rxBytes,
			TxBytes:       ds.txBytes,
			RxBytesPerSec: rxPerSec,
			TxBytesPerSec: txPerSec,
			FirstSeenMs:   ds.firstSeenAt.UnixMilli(),
			LastSeenMs:    ds.lastSeenAt.UnixMilli(),
			Priority:      prio,
		Route:         c.lookupRouteModeLocked(key),
		ProcessNames:  sortedProcessNames(ds.processNames),
		})
	}
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].RxBytes+snaps[i].TxBytes > snaps[j].RxBytes+snaps[j].TxBytes
	})
	return snaps
}

// sortedProcessNames returns a sorted slice from a set of process names.
func sortedProcessNames(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
	destSnap := c.buildDestSnapshotLocked(nil, nil, 0)
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.sseClients, ch)
		c.mu.Unlock()
	}()

	// Send the initial snapshots so the client has authoritative state on connect.
	if len(snapshot) > 0 {
		if data, err := json.Marshal(snapshot); err == nil {
			fmt.Fprintf(w, "event: connections_snapshot\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
	if len(destSnap) > 0 {
		if data, err := json.Marshal(destSnap); err == nil {
			fmt.Fprintf(w, "event: destinations_snapshot\ndata: %s\n\n", data)
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

// handlePriorities serves GET (read all) and POST (update) for destination priorities.
// POST body: {"host": priority, ...} where priority is 1–5.
func (c *Counters) handlePriorities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(c.Priorities())
	case http.MethodPost:
		var m map[string]int
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.SetPriorities(m)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(c.Priorities())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleRoutes serves GET (read all) and POST (update) for destination route modes.
// POST body: {"host": "tunnel"|"direct"|"blocked", ...}
func (c *Counters) handleRoutes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(c.RouteModes())
	case http.MethodPost:
		var m map[string]RouteMode
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.SetRouteModes(m)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(c.RouteModes())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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
