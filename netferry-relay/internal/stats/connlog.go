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
	NowMs         int64        `json:"nowMs"`
	HistoryFromMs int64        `json:"historyFromMs,omitempty"` // open time of the oldest remembered closed connection
	ActiveMatched int          `json:"activeMatched"`           // matches before limit
	ClosedMatched int          `json:"closedMatched"`
	Active        []ConnRecord `json:"active"`
	Closed        []ConnRecord `json:"closed"`
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

// oldestOpenMs is the smallest OpenedMs in the ring, 0 when empty.
func (r *connLogRing) oldestOpenMs() int64 {
	var oldest int64
	for i := range r.buf {
		if ms := r.buf[i].OpenedMs; oldest == 0 || ms < oldest {
			oldest = ms
		}
	}
	return oldest
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
	host     string // case-insensitive substring of host or dstAddr
	sinceMs  int64  // keep records still open at or after this time
	untilMs  int64  // keep records opened at or before this time
	route    string // "tunnel" | "direct" | "blocked"; empty = any
	minDurMs int64  // keep records that lasted at least this long
	minBytes int64  // keep records that moved at least this many bytes (up + down)
	errOnly  bool
}

func (f connFilter) match(r *ConnRecord) bool {
	if f.sinceMs > 0 && r.ClosedMs != 0 && r.ClosedMs < f.sinceMs {
		return false
	}
	if f.untilMs > 0 && r.OpenedMs > f.untilMs {
		return false
	}
	if f.route != "" && r.Route != f.route {
		return false
	}
	if f.minDurMs > 0 && r.DurationMs < f.minDurMs {
		return false
	}
	if f.minBytes > 0 && r.TxBytes+r.RxBytes < f.minBytes {
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

// matching returns the active and closed connections matching f. Active
// records are snapshotted with DurationMs measured up to now.
func (c *Counters) matching(f connFilter, now time.Time) (active, closed []ConnRecord, historyFromMs int64) {
	active, closed = []ConnRecord{}, []ConnRecord{}
	c.mu.Lock()
	historyFromMs = c.connLog.oldestOpenMs()
	for id, cs := range c.conns {
		rec := cs.record(id, now)
		if f.match(&rec) {
			active = append(active, rec)
		}
	}
	c.connLog.each(func(r *ConnRecord) {
		if f.match(r) {
			closed = append(closed, *r)
		}
	})
	c.mu.Unlock()
	return active, closed, historyFromMs
}

// connections returns active and recently closed connections matching f.
// With order "" / "open" each list is the newest limit entries, oldest first;
// with "dur", "bytes" or "first-byte" it is the top limit entries by that key,
// largest first.
func (c *Counters) connections(f connFilter, order string, limit int) ConnectionsResponse {
	now := time.Now()
	active, closed, from := c.matching(f, now)
	return ConnectionsResponse{
		NowMs:         now.UnixMilli(),
		HistoryFromMs: from,
		ActiveMatched: len(active),
		ClosedMatched: len(closed),
		Active:        sortConns(active, order, limit),
		Closed:        sortConns(closed, order, limit),
	}
}

// connSortKeys maps a /connections sort= value to its descending key.
var connSortKeys = map[string]func(*ConnRecord) int64{
	"dur":        func(r *ConnRecord) int64 { return r.DurationMs },
	"bytes":      func(r *ConnRecord) int64 { return r.TxBytes + r.RxBytes },
	"first-byte": func(r *ConnRecord) int64 { return r.FirstRxMs },
}

func sortConns(recs []ConnRecord, order string, limit int) []ConnRecord {
	key, ok := connSortKeys[order]
	if !ok {
		sort.SliceStable(recs, func(i, j int) bool { return recs[i].OpenedMs < recs[j].OpenedMs })
		if limit > 0 && len(recs) > limit {
			recs = recs[len(recs)-limit:]
		}
		return recs
	}
	sort.SliceStable(recs, func(i, j int) bool { return key(&recs[i]) > key(&recs[j]) })
	if limit > 0 && len(recs) > limit {
		recs = recs[:limit]
	}
	return recs
}

// ConnGroup aggregates the connections sharing one host / route / error in
// the /connections?group= response.
type ConnGroup struct {
	Key          string `json:"key"`
	Conns        int    `json:"conns"`  // active + closed
	Active       int    `json:"active"` // still open
	Errors       int    `json:"errors"` // closed with an error
	TxBytes      int64  `json:"txBytes"`
	RxBytes      int64  `json:"rxBytes"`
	NoReply      int    `json:"noReply"`                  // sent bytes, got none back
	FirstByteP50 int64  `json:"firstByteP50Ms,omitempty"` // over connections that got a first byte
	FirstByteP95 int64  `json:"firstByteP95Ms,omitempty"`
	MaxDurMs     int64  `json:"maxDurMs"`
	FirstSeenMs  int64  `json:"firstSeenMs"`        // earliest open
	LastSeenMs   int64  `json:"lastSeenMs"`         // latest open
	TopError     string `json:"topError,omitempty"` // most frequent error, when Errors > 0
}

// ConnGroupsResponse is the JSON body of GET /connections?group=...
type ConnGroupsResponse struct {
	NowMs         int64       `json:"nowMs"`
	HistoryFromMs int64       `json:"historyFromMs,omitempty"`
	GroupBy       string      `json:"groupBy"`
	Total         int         `json:"total"`             // connections matched before grouping
	Groups        []ConnGroup `json:"groups"`            // most connections first
	Omitted       int         `json:"omitted,omitempty"` // groups dropped by limit
}

// connGroupKeys maps a /connections group= value to the grouping key.
var connGroupKeys = map[string]func(*ConnRecord) string{
	"host": func(r *ConnRecord) string {
		if r.Host != "" {
			return r.Host
		}
		return r.DstAddr
	},
	"route": func(r *ConnRecord) string { return r.Route },
	"error": func(r *ConnRecord) string {
		if r.Error == "" {
			return "(clean)"
		}
		return r.Error
	},
}

// groups aggregates the connections matching f by by ("host", "route" or
// "error") and keeps the limit groups with the most connections.
func (c *Counters) groups(f connFilter, by string, limit int) ConnGroupsResponse {
	now := time.Now()
	active, closed, from := c.matching(f, now)
	keyOf := connGroupKeys[by]

	type acc struct {
		g          ConnGroup
		firstBytes []int64
		errs       map[string]int
	}
	byKey := map[string]*acc{}
	add := func(r *ConnRecord, open bool) {
		k := keyOf(r)
		a := byKey[k]
		if a == nil {
			a = &acc{g: ConnGroup{Key: k, FirstSeenMs: r.OpenedMs}, errs: map[string]int{}}
			byKey[k] = a
		}
		g := &a.g
		g.Conns++
		if open {
			g.Active++
		} else if r.Error != "" {
			g.Errors++
			a.errs[r.Error]++
		}
		g.TxBytes += r.TxBytes
		g.RxBytes += r.RxBytes
		if !open && r.TxBytes > 0 && r.RxBytes == 0 {
			g.NoReply++
		}
		if r.FirstRxMs > 0 {
			a.firstBytes = append(a.firstBytes, r.FirstRxMs)
		}
		g.MaxDurMs = max(g.MaxDurMs, r.DurationMs)
		g.FirstSeenMs = min(g.FirstSeenMs, r.OpenedMs)
		g.LastSeenMs = max(g.LastSeenMs, r.OpenedMs)
	}
	for i := range active {
		add(&active[i], true)
	}
	for i := range closed {
		add(&closed[i], false)
	}

	resp := ConnGroupsResponse{NowMs: now.UnixMilli(), HistoryFromMs: from, GroupBy: by, Total: len(active) + len(closed), Groups: []ConnGroup{}}
	for _, a := range byKey {
		if n := len(a.firstBytes); n > 0 {
			sort.Slice(a.firstBytes, func(i, j int) bool { return a.firstBytes[i] < a.firstBytes[j] })
			a.g.FirstByteP50 = a.firstBytes[(n-1)*50/100]
			a.g.FirstByteP95 = a.firstBytes[(n-1)*95/100]
		}
		best := 0
		for e, n := range a.errs {
			if n > best || (n == best && e < a.g.TopError) {
				a.g.TopError, best = e, n
			}
		}
		resp.Groups = append(resp.Groups, a.g)
	}
	sort.Slice(resp.Groups, func(i, j int) bool {
		gi, gj := &resp.Groups[i], &resp.Groups[j]
		if gi.Conns != gj.Conns {
			return gi.Conns > gj.Conns
		}
		return gi.Key < gj.Key
	})
	if limit > 0 && len(resp.Groups) > limit {
		resp.Omitted = len(resp.Groups) - limit
		resp.Groups = resp.Groups[:limit]
	}
	return resp
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
//	host=<substr>     case-insensitive match on host or dstAddr
//	since=<10m|ms>    drop connections that closed before this point
//	until=<10m|ms>    drop connections opened after this point
//	route=<r>         tunnel | direct | blocked
//	min_dur=<5s>      drop connections shorter than this
//	min_bytes=<n>     drop connections that moved fewer bytes (up + down)
//	errors=1          only connections that ended with an error
//	sort=<k>          open (default, newest last) | dur | bytes | first-byte (largest first)
//	group=<k>         host | route | error: return per-group aggregates instead of connections
//	limit=<n>         n per list, or n groups (default 500, 0 = all)
func (c *Counters) handleConnections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	now := time.Now()
	sinceMs, ok := parseSince(q.Get("since"), now)
	if !ok {
		http.Error(w, "since: want a duration like 10m or unix milliseconds", http.StatusBadRequest)
		return
	}
	untilMs, ok := parseSince(q.Get("until"), now)
	if !ok {
		http.Error(w, "until: want a duration like 10m or unix milliseconds", http.StatusBadRequest)
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
		untilMs: untilMs,
		route:   q.Get("route"),
		errOnly: q.Get("errors") == "1" || q.Get("errors") == "true",
	}
	switch f.route {
	case "", string(RouteTunnel), string(RouteDirect), string(RouteBlocked):
	default:
		http.Error(w, "route: want tunnel, direct or blocked", http.StatusBadRequest)
		return
	}
	if s := q.Get("min_dur"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			http.Error(w, "min_dur: want a duration like 5s", http.StatusBadRequest)
			return
		}
		f.minDurMs = d.Milliseconds()
	}
	if s := q.Get("min_bytes"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "min_bytes: want a non-negative integer", http.StatusBadRequest)
			return
		}
		f.minBytes = n
	}
	order := q.Get("sort")
	if _, ok := connSortKeys[order]; !ok && order != "" && order != "open" {
		http.Error(w, "sort: want open, dur, bytes or first-byte", http.StatusBadRequest)
		return
	}
	by := q.Get("group")
	if _, ok := connGroupKeys[by]; !ok && by != "" {
		http.Error(w, "group: want host, route or error", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if by != "" {
		json.NewEncoder(w).Encode(c.groups(f, by, limit))
		return
	}
	json.NewEncoder(w).Encode(c.connections(f, order, limit))
}
