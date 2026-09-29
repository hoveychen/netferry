package stats

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConnectionsActiveAndClosed(t *testing.T) {
	c := NewCounters()
	a := c.ConnOpen("10.0.0.1:1000", "160.79.104.10:443", "api.anthropic.com", 2)
	c.ConnAddTx(a, 100)
	c.ConnAddRx(a, 200)
	c.ConnCloseErr(a, "10.0.0.1:1000", "160.79.104.10:443", "download: reset")

	b := c.ConnOpenRoute("10.0.0.1:1001", "1.2.3.4:443", "example.com", 0, RouteDirect)
	c.ConnFailed("10.0.0.1:1002", "5.6.7.8:443", "ads.example.net", RouteBlocked, time.Now(), "blocked by route rule")

	resp := c.connections(connFilter{}, "", 0)
	if len(resp.Active) != 1 || resp.Active[0].ID != b || resp.Active[0].Route != "direct" {
		t.Fatalf("active = %+v", resp.Active)
	}
	if len(resp.Closed) != 2 {
		t.Fatalf("closed = %+v", resp.Closed)
	}
	got := resp.Closed[0]
	if got.Host != "api.anthropic.com" || got.Route != "tunnel" || got.TunnelIndex != 2 ||
		got.TxBytes != 100 || got.RxBytes != 200 || got.Error != "download: reset" ||
		got.ClosedMs == 0 || got.LastRxMs == 0 || got.LastTxMs == 0 {
		t.Fatalf("closed[0] = %+v", got)
	}
	if resp.Closed[1].Route != "blocked" || resp.Closed[1].Error == "" {
		t.Fatalf("closed[1] = %+v", resp.Closed[1])
	}
}

func TestConnectionsFilter(t *testing.T) {
	c := NewCounters()
	for _, h := range []string{"api.anthropic.com", "example.com", "statsig.anthropic.com"} {
		id := c.ConnOpen("s", "9.9.9.9:443", h, 0)
		c.ConnClose(id, "s", "9.9.9.9:443")
	}
	resp := c.connections(connFilter{host: "anthropic"}, "", 0)
	if len(resp.Closed) != 2 {
		t.Fatalf("host filter: %+v", resp.Closed)
	}
	resp = c.connections(connFilter{host: "9.9.9.9"}, "", 1)
	if len(resp.Closed) != 1 || resp.Closed[0].Host != "statsig.anthropic.com" {
		t.Fatalf("dst filter + limit should keep newest: %+v", resp.Closed)
	}
	resp = c.connections(connFilter{sinceMs: time.Now().Add(time.Hour).UnixMilli()}, "", 0)
	if len(resp.Closed) != 0 {
		t.Fatalf("since filter: %+v", resp.Closed)
	}
	resp = c.connections(connFilter{errOnly: true}, "", 0)
	if len(resp.Closed) != 0 {
		t.Fatalf("errors filter: %+v", resp.Closed)
	}
}

func TestConnLogRingWraps(t *testing.T) {
	var r connLogRing
	for i := 0; i < maxConnLog+10; i++ {
		r.add(ConnRecord{ID: uint64(i)})
	}
	var ids []uint64
	r.each(func(rec *ConnRecord) { ids = append(ids, rec.ID) })
	if len(ids) != maxConnLog || ids[0] != 10 || ids[len(ids)-1] != maxConnLog+9 {
		t.Fatalf("len=%d first=%d last=%d", len(ids), ids[0], ids[len(ids)-1])
	}
}

func TestHandleConnections(t *testing.T) {
	c := NewCounters()
	id := c.ConnOpen("s", "160.79.104.10:443", "api.anthropic.com", 0)
	c.ConnClose(id, "s", "160.79.104.10:443")

	rec := httptest.NewRecorder()
	c.handleConnections(rec, httptest.NewRequest("GET", "/connections?host=ANTHROPIC&since=10m", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp ConnectionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Closed) != 1 || resp.Active == nil {
		t.Fatalf("resp = %+v", resp)
	}

	rec = httptest.NewRecorder()
	c.handleConnections(rec, httptest.NewRequest("GET", "/connections?since=yesterday", nil))
	if rec.Code != 400 {
		t.Fatalf("bad since: status %d", rec.Code)
	}
}

func TestConnectionsSortAndNewFilters(t *testing.T) {
	c := NewCounters()
	c.mu.Lock()
	now := time.Now().UnixMilli()
	for i, r := range []ConnRecord{
		{Host: "a.com", Route: "tunnel", OpenedMs: now - 3000, ClosedMs: now, DurationMs: 100, TxBytes: 10, RxBytes: 10, FirstRxMs: 50},
		{Host: "b.com", Route: "direct", OpenedMs: now - 2000, ClosedMs: now, DurationMs: 9000, TxBytes: 5, FirstRxMs: 900},
		{Host: "c.com", Route: "tunnel", OpenedMs: now - 1000, ClosedMs: now, DurationMs: 500, TxBytes: 4000, RxBytes: 1000},
	} {
		r.ID = uint64(i + 1)
		c.connLog.add(r)
	}
	c.mu.Unlock()

	hosts := func(recs []ConnRecord) (s []string) {
		for _, r := range recs {
			s = append(s, r.Host)
		}
		return s
	}
	check := func(name string, f connFilter, order string, limit int, want ...string) {
		t.Helper()
		got := hosts(c.connections(f, order, limit).Closed)
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", name, got, want)
			}
		}
	}
	check("sort dur top 2", connFilter{}, "dur", 2, "b.com", "c.com")
	check("sort bytes", connFilter{}, "bytes", 0, "c.com", "a.com", "b.com")
	check("sort first-byte", connFilter{}, "first-byte", 1, "b.com")
	check("route", connFilter{route: "tunnel"}, "", 0, "a.com", "c.com")
	check("min dur", connFilter{minDurMs: 500}, "", 0, "b.com", "c.com")
	check("min bytes", connFilter{minBytes: 20}, "", 0, "a.com", "c.com")
	check("until", connFilter{untilMs: now - 1500}, "", 0, "a.com", "b.com")
}

func TestConnectionsGroups(t *testing.T) {
	c := NewCounters()
	for i := 0; i < 3; i++ {
		id := c.ConnOpen("s", "1.1.1.1:443", "api.anthropic.com", 0)
		c.ConnAddTx(id, 10)
		if i < 2 {
			c.ConnAddRx(id, 100)
			c.ConnClose(id, "s", "1.1.1.1:443")
		} else {
			c.ConnCloseErr(id, "s", "1.1.1.1:443", "download: reset")
		}
	}
	c.ConnOpen("s", "2.2.2.2:443", "", 0) // still open, no host
	c.ConnFailed("s", "3.3.3.3:443", "ads.net", RouteBlocked, time.Now(), "blocked by route rule")

	resp := c.groups(connFilter{}, "host", 0)
	if resp.Total != 5 || len(resp.Groups) != 3 {
		t.Fatalf("host groups = %+v", resp)
	}
	g := resp.Groups[0]
	if g.Key != "api.anthropic.com" || g.Conns != 3 || g.Errors != 1 || g.NoReply != 1 ||
		g.TxBytes != 30 || g.RxBytes != 200 || g.TopError != "download: reset" || g.FirstByteP50 < 0 {
		t.Fatalf("anthropic group = %+v", g)
	}
	var open *ConnGroup
	for i := range resp.Groups {
		if resp.Groups[i].Key == "2.2.2.2:443" {
			open = &resp.Groups[i]
		}
	}
	if open == nil || open.Active != 1 {
		t.Fatalf("hostless active conn should group by dst: %+v", resp.Groups)
	}

	resp = c.groups(connFilter{}, "route", 1)
	if len(resp.Groups) != 1 || resp.Groups[0].Key != "tunnel" || resp.Groups[0].Conns != 4 || resp.Omitted != 1 {
		t.Fatalf("route groups = %+v", resp)
	}
	resp = c.groups(connFilter{}, "error", 0)
	if resp.Groups[0].Key != "(clean)" || resp.Groups[0].Conns != 3 {
		t.Fatalf("error groups = %+v", resp)
	}
}

func TestConnectionsHandlerGroupAndBadParams(t *testing.T) {
	c := NewCounters()
	id := c.ConnOpen("s", "1.1.1.1:443", "example.com", 0)
	c.ConnClose(id, "s", "1.1.1.1:443")

	rec := httptest.NewRecorder()
	c.handleConnections(rec, httptest.NewRequest("GET", "/connections?group=host&route=tunnel&until=0s", nil))
	var resp ConnGroupsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.GroupBy != "host" || len(resp.Groups) != 1 {
		t.Fatalf("group response %d %s", rec.Code, rec.Body.String())
	}
	for _, q := range []string{"group=port", "sort=size", "route=vpn", "min_dur=5", "min_bytes=-1", "until=soon"} {
		rec := httptest.NewRecorder()
		c.handleConnections(rec, httptest.NewRequest("GET", "/connections?"+q, nil))
		if rec.Code != 400 {
			t.Fatalf("%s: code %d", q, rec.Code)
		}
	}
}
