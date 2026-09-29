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

	resp := c.connections(connFilter{}, 0)
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
	resp := c.connections(connFilter{host: "anthropic"}, 0)
	if len(resp.Closed) != 2 {
		t.Fatalf("host filter: %+v", resp.Closed)
	}
	resp = c.connections(connFilter{host: "9.9.9.9"}, 1)
	if len(resp.Closed) != 1 || resp.Closed[0].Host != "statsig.anthropic.com" {
		t.Fatalf("dst filter + limit should keep newest: %+v", resp.Closed)
	}
	resp = c.connections(connFilter{sinceMs: time.Now().Add(time.Hour).UnixMilli()}, 0)
	if len(resp.Closed) != 0 {
		t.Fatalf("since filter: %+v", resp.Closed)
	}
	resp = c.connections(connFilter{errOnly: true}, 0)
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
