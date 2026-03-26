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

// Counters holds all live tunnel metrics. Fields are safe for concurrent use.
type Counters struct {
	RxTotal   atomic.Int64 // cumulative bytes received from remote (download)
	TxTotal   atomic.Int64 // cumulative bytes sent to remote (upload)
	ActiveTCP atomic.Int32 // currently open TCP channels
	TotalTCP  atomic.Int64 // all-time TCP connections opened
	DNSTotal  atomic.Int64 // all-time DNS queries forwarded

	nextConnID atomic.Uint64

	connEventCh chan ConnEvent // connection open/close notifications for SSE

	mu         sync.Mutex
	sseClients map[chan string]struct{}
}

// Snapshot is the JSON payload sent in each "stats" SSE event.
type Snapshot struct {
	RxBytesPerSec int64 `json:"rxBytesPerSec"`
	TxBytesPerSec int64 `json:"txBytesPerSec"`
	TotalRxBytes  int64 `json:"totalRxBytes"`
	TotalTxBytes  int64 `json:"totalTxBytes"`
	ActiveConns   int32 `json:"activeConns"`
	TotalConns    int64 `json:"totalConns"`
	DNSQueries    int64 `json:"dnsQueries"`
}

// ConnEvent is the JSON payload sent in each "connection" SSE event.
type ConnEvent struct {
	ID          uint64 `json:"id"`
	Action      string `json:"action"` // "open" or "close"
	SrcAddr     string `json:"srcAddr"`
	DstAddr     string `json:"dstAddr"`
	Host        string `json:"host,omitempty"` // resolved hostname (from SNI / HTTP Host / SOCKS5 domain)
	TimestampMs int64  `json:"timestampMs"`
}

// NewCounters allocates a ready-to-use Counters instance.
func NewCounters() *Counters {
	return &Counters{
		connEventCh: make(chan ConnEvent, 64),
		sseClients:  make(map[chan string]struct{}),
	}
}

// ConnOpen records a new TCP connection and queues an SSE "open" notification.
// Returns the connection ID that must be passed to ConnClose later.
// The host parameter is the resolved hostname (from SNI, HTTP Host header, or
// SOCKS5 domain); pass "" if unknown.
func (c *Counters) ConnOpen(srcAddr, dstAddr, host string) uint64 {
	id := c.nextConnID.Add(1)
	select {
	case c.connEventCh <- ConnEvent{
		ID:          id,
		Action:      "open",
		SrcAddr:     srcAddr,
		DstAddr:     dstAddr,
		Host:        host,
		TimestampMs: time.Now().UnixMilli(),
	}:
	default:
	}
	return id
}

// ConnClose queues an SSE "close" notification for a previously opened connection.
func (c *Counters) ConnClose(id uint64, srcAddr, dstAddr string) {
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
	c.ConnOpen(srcAddr, dstAddr, "")
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

// broadcaster fans out SSE messages: stats once per second, connection events
// immediately as they arrive.
func (c *Counters) broadcaster() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var prevRx, prevTx int64

	for {
		select {
		case <-ticker.C:
			curRx := c.RxTotal.Load()
			curTx := c.TxTotal.Load()

			snap := Snapshot{
				RxBytesPerSec: curRx - prevRx,
				TxBytesPerSec: curTx - prevTx,
				TotalRxBytes:  curRx,
				TotalTxBytes:  curTx,
				ActiveConns:   c.ActiveTCP.Load(),
				TotalConns:    c.TotalTCP.Load(),
				DNSQueries:    c.DNSTotal.Load(),
			}
			prevRx = curRx
			prevTx = curTx

			data, _ := json.Marshal(snap)
			c.broadcast(fmt.Sprintf("event: stats\ndata: %s\n\n", data))

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

	ch := make(chan string, 16)
	c.mu.Lock()
	c.sseClients[ch] = struct{}{}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.sseClients, ch)
		c.mu.Unlock()
	}()

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

	snap := Snapshot{
		TotalRxBytes: c.RxTotal.Load(),
		TotalTxBytes: c.TxTotal.Load(),
		ActiveConns:  c.ActiveTCP.Load(),
		TotalConns:   c.TotalTCP.Load(),
		DNSQueries:   c.DNSTotal.Load(),
	}
	json.NewEncoder(w).Encode(snap)
}
