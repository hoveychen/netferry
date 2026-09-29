package stats

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxConnLog is how many closed / failed connections /connections remembers.
// At a few hundred bytes per record this bounds the history to ~1–2 MB.
const maxConnLog = 4096

// ConnRecord is one connection in the /connections response. Timing is
// connection-level only: several HTTP requests can share one keep-alive or
// HTTP/2 connection, and the tunnel never sees inside TLS.
type ConnRecord struct {
	ID          uint64 `json:"id"`
	Route       string `json:"route"` // "tunnel" | "direct" | "blocked"
	SrcAddr     string `json:"srcAddr"`
	DstAddr     string `json:"dstAddr"`
	Host        string `json:"host,omitempty"`
	TunnelIndex int    `json:"tunnelIndex,omitempty"`
	OpenedMs    int64  `json:"openedMs"`
	ClosedMs    int64  `json:"closedMs,omitempty"` // 0 = still open
	DurationMs  int64  `json:"durationMs"`         // open → close, or open → now while active
	TxBytes     int64  `json:"txBytes"`
	RxBytes     int64  `json:"rxBytes"`
	FirstRxMs   int64  `json:"firstRxMs,omitempty"` // ms from open to the first downloaded byte
	LastRxMs    int64  `json:"lastRxMs,omitempty"`  // wall-clock ms of the last downloaded byte
	LastTxMs    int64  `json:"lastTxMs,omitempty"`  // wall-clock ms of the last uploaded byte
	Error       string `json:"error,omitempty"`     // why it ended / failed; empty = clean close
}

// ConnectionsResponse is the JSON body of GET /connections.
type ConnectionsResponse struct {
	NowMs  int64        `json:"nowMs"`
	Active []ConnRecord `json:"active"`
	Closed []ConnRecord `json:"closed"`
}

// connLogRing is a fixed-size ring of closed connection records. Guarded by
// Counters.mu.
type connLogRing struct {
	buf  []ConnRecord
	next int
}

func (r *connLogRing) add(rec ConnRecord) {
	if len(r.buf) < maxConnLog {
		r.buf = append(r.buf, rec)
		return
	}
	r.buf[r.next] = rec
	r.next = (r.next + 1) % maxConnLog
}

// each calls fn for every record, oldest first.
func (r *connLogRing) each(fn func(*ConnRecord)) {
	n := len(r.buf)
	for i := 0; i < n; i++ {
		fn(&r.buf[(r.next+i)%n])
	}
}

func msOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// record snapshots cs as a ConnRecord; DurationMs is measured up to now.
func (cs *connStats) record(id uint64, now time.Time) ConnRecord {
	rec := ConnRecord{
		ID:          id,
		Route:       string(cs.route),
		SrcAddr:     cs.srcAddr,
		DstAddr:     cs.dstAddr,
		Host:        cs.host,
		TunnelIndex: cs.tunnelIndex,
		OpenedMs:    cs.openedAt.UnixMilli(),
		DurationMs:  now.Sub(cs.openedAt).Milliseconds(),
		TxBytes:     cs.txBytes,
		RxBytes:     cs.rxBytes,
		LastRxMs:    msOrZero(cs.lastRxAt),
		LastTxMs:    msOrZero(cs.lastTxAt),
	}
	if rec.Route == "" {
		rec.Route = string(RouteTunnel)
	}
	if !cs.firstRxAt.IsZero() {
		rec.FirstRxMs = cs.firstRxAt.Sub(cs.openedAt).Milliseconds()
	}
	return rec
}

// ConnFailed records a connection that never carried data — blocked by a
// route rule, or the tunnel channel / direct dial could not be opened — so it
// still shows up in /connections. startedAt is when the client connection was
// accepted.
func (c *Counters) ConnFailed(srcAddr, dstAddr, host string, route RouteKind, startedAt time.Time, errMsg string) {
	now := time.Now()
	id := c.nextConnID.Add(1)
	c.mu.Lock()
	c.connLog.add(ConnRecord{
		ID:         id,
		Route:      string(route),
		SrcAddr:    srcAddr,
		DstAddr:    dstAddr,
		Host:       host,
		OpenedMs:   startedAt.UnixMilli(),
		ClosedMs:   now.UnixMilli(),
		DurationMs: now.Sub(startedAt).Milliseconds(),
		Error:      errMsg,
	})
	c.mu.Unlock()
}

// connFilter selects records for /connections.
type connFilter struct {
	host    string // case-insensitive substring of host or dstAddr
	sinceMs int64  // keep records still open at or after this time
	errOnly bool
}

func (f connFilter) match(r *ConnRecord) bool {
	if f.sinceMs > 0 && r.ClosedMs != 0 && r.ClosedMs < f.sinceMs {
		return false
	}
	if f.errOnly && r.Error == "" {
		return false
	}
	if f.host != "" &&
		!strings.Contains(strings.ToLower(r.Host), f.host) &&
		!strings.Contains(strings.ToLower(r.DstAddr), f.host) {
		return false
	}
	return true
}

// connections returns active and recently closed connections matching f,
// each sorted by open time and trimmed to the newest limit entries.
func (c *Counters) connections(f connFilter, limit int) ConnectionsResponse {
	now := time.Now()
	resp := ConnectionsResponse{NowMs: now.UnixMilli(), Active: []ConnRecord{}, Closed: []ConnRecord{}}
	c.mu.Lock()
	for id, cs := range c.conns {
		rec := cs.record(id, now)
		if f.match(&rec) {
			resp.Active = append(resp.Active, rec)
		}
	}
	c.connLog.each(func(r *ConnRecord) {
		if f.match(r) {
			resp.Closed = append(resp.Closed, *r)
		}
	})
	c.mu.Unlock()
	resp.Active = newestByOpen(resp.Active, limit)
	resp.Closed = newestByOpen(resp.Closed, limit)
	return resp
}

func newestByOpen(recs []ConnRecord, limit int) []ConnRecord {
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].OpenedMs < recs[j].OpenedMs })
	if limit > 0 && len(recs) > limit {
		recs = recs[len(recs)-limit:]
	}
	return recs
}

// parseSince accepts a Go duration ("10m", "90s" — meaning that long ago) or
// an absolute unix time in milliseconds.
func parseSince(s string, now time.Time) (int64, bool) {
	if s == "" {
		return 0, true
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d).UnixMilli(), true
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return ms, true
	}
	return 0, false
}

// handleConnections serves GET /connections.
//
// Query parameters (all optional):
//
//	host=<substr>   case-insensitive match on host or dstAddr
//	since=<10m|ms>  drop connections that closed before this point
//	errors=1        only connections that ended with an error
//	limit=<n>       newest n per list (default 500, 0 = all)
func (c *Counters) handleConnections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	sinceMs, ok := parseSince(q.Get("since"), time.Now())
	if !ok {
		http.Error(w, "since: want a duration like 10m or unix milliseconds", http.StatusBadRequest)
		return
	}
	limit := 500
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			http.Error(w, "limit: want a non-negative integer", http.StatusBadRequest)
			return
		}
		limit = n
	}
	f := connFilter{
		host:    strings.ToLower(q.Get("host")),
		sinceMs: sinceMs,
		errOnly: q.Get("errors") == "1" || q.Get("errors") == "true",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c.connections(f, limit))
}
