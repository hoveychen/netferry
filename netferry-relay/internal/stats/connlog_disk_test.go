package stats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConnLogPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conns.jsonl")
	c := NewCounters()
	if err := c.PersistConnLog(path); err != nil {
		t.Fatal(err)
	}
	id := c.ConnOpen("s", "1.1.1.1:443", "api.anthropic.com", 0)
	c.ConnAddTx(id, 10)
	c.ConnCloseErr(id, "s", "1.1.1.1:443", "download: reset")
	c.ConnFailed("s", "2.2.2.2:443", "ads.net", RouteBlocked, time.Now(), "blocked by route rule")

	// A new process (fresh Counters, empty ring) sees the old history.
	c2 := NewCounters()
	if err := c2.PersistConnLog(path); err != nil {
		t.Fatal(err)
	}
	live := c2.ConnOpen("s", "3.3.3.3:443", "example.com", 0)
	resp := c2.connections(connFilter{}, "", 0)
	if len(resp.Closed) != 2 || resp.Closed[0].Host != "api.anthropic.com" || resp.Closed[0].Error != "download: reset" ||
		resp.Closed[1].Route != "blocked" || len(resp.Active) != 1 || resp.Active[0].ID != live {
		t.Fatalf("after restart: %+v", resp)
	}
	if resp.HistoryFromMs == 0 || resp.HistoryFromMs > resp.Closed[0].OpenedMs {
		t.Fatalf("historyFromMs = %d, oldest open %d", resp.HistoryFromMs, resp.Closed[0].OpenedMs)
	}
	if got := c2.connections(connFilter{host: "anthropic"}, "", 0).Closed; len(got) != 1 {
		t.Fatalf("host filter on disk: %+v", got)
	}
	if got := c2.connections(connFilter{errOnly: true, route: "tunnel"}, "", 0).Closed; len(got) != 1 {
		t.Fatalf("errors filter on disk: %+v", got)
	}
	g := c2.groups(connFilter{}, "route", 0)
	if g.Total != 3 || len(g.Groups) != 2 {
		t.Fatalf("groups on disk: %+v", g)
	}
}

func TestConnLogReadsRotatedFilesOldestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conns.jsonl")
	write := func(p, line string, mtime time.Time) {
		if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, mtime, mtime)
	}
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	// logfile prefixes lines with a timestamp; the reader must skip it.
	write(path+".2", "2026-09-29 10:00:00.000 {\"host\":\"a.com\",\"route\":\"tunnel\",\"openedMs\":1000,\"closedMs\":2000}\n", old)
	write(path+".1", "2026-09-29 11:00:00.000 {\"host\":\"b.com\",\"route\":\"tunnel\",\"openedMs\":3000,\"closedMs\":4000}\nnot json\n", now)
	write(path, "{\"host\":\"c.com\",\"route\":\"direct\",\"openedMs\":5000,\"closedMs\":6000}\n", now)

	c := NewCounters()
	if err := c.PersistConnLog(path); err != nil {
		t.Fatal(err)
	}
	resp := c.connections(connFilter{}, "", 0)
	if len(resp.Closed) != 3 || resp.Closed[0].Host != "a.com" || resp.Closed[2].Host != "c.com" || resp.HistoryFromMs != 1000 {
		t.Fatalf("rotated read: %+v", resp)
	}
	// since newer than .2's mtime skips that file entirely — even though its
	// record's closedMs would (artificially) pass the filter.
	resp = c.connections(connFilter{sinceMs: now.Add(-time.Hour).UnixMilli()}, "", 0)
	if len(resp.Closed) != 0 {
		t.Fatalf("since: %+v", resp.Closed)
	}
	resp = c.connections(connFilter{sinceMs: 3500}, "", 1)
	if resp.ClosedMatched != 2 || len(resp.Closed) != 1 || resp.Closed[0].Host != "c.com" || resp.HistoryFromMs != 1000 {
		t.Fatalf("since+limit: %+v", resp)
	}
}

func TestConnListTrimsWhileCollecting(t *testing.T) {
	l := connList{order: "bytes", limit: 3}
	for i := 0; i < 1000; i++ {
		l.add(&ConnRecord{TxBytes: int64(i)})
	}
	got := l.result()
	if l.matched != 1000 || len(got) != 3 || got[0].TxBytes != 999 || got[2].TxBytes != 997 || cap(l.recs) > 200 {
		t.Fatalf("matched=%d got=%+v cap=%d", l.matched, got, cap(l.recs))
	}
	l = connList{limit: 2}
	for i := 0; i < 500; i++ {
		l.add(&ConnRecord{OpenedMs: int64(i)})
	}
	if got := l.result(); len(got) != 2 || got[0].OpenedMs != 498 || got[1].OpenedMs != 499 {
		t.Fatalf("open order: %+v", got)
	}
}
