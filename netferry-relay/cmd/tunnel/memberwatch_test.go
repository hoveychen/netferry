package main

import (
	"testing"
	"time"

	"github.com/hoveychen/netferry/relay/internal/stats"
)

type countCloser struct{ n int }

func (c *countCloser) Close() error { c.n++; return nil }

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newTestWatch() (*memberWatch, *countCloser, *fakeClock, *stats.TunnelCounters) {
	cl := &countCloser{}
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	tc := &stats.TunnelCounters{}
	w := newMemberWatch(1, 4, tc, cl)
	w.now = clk.now
	w.lastReply = clk.now()
	return w, cl, clk, tc
}

func TestMemberWatchRecyclesIdleHighRTT(t *testing.T) {
	w, cl, clk, _ := newTestWatch()
	for i := 0; i < degradedSamples; i++ {
		clk.advance(30 * time.Second)
		w.ObserveRTT(5 * time.Second)
	}
	if cl.n != 1 {
		t.Fatalf("closed %d times, want 1", cl.n)
	}
	clk.advance(30 * time.Second)
	w.ObserveRTT(5 * time.Second)
	if cl.n != 1 {
		t.Fatalf("recycled again: closed %d times", cl.n)
	}
}

func TestMemberWatchSparesBusyMember(t *testing.T) {
	w, cl, clk, tc := newTestWatch()
	for i := 0; i < degradedSamples+2; i++ {
		clk.advance(30 * time.Second)
		tc.AddRx(30 * 1 << 20) // 1 MiB/s
		w.ObserveRTT(5 * time.Second)
	}
	if cl.n != 0 {
		t.Fatalf("busy member recycled")
	}
}

func TestMemberWatchGoodSampleResetsStreak(t *testing.T) {
	w, cl, clk, _ := newTestWatch()
	for i := 0; i < 3*degradedSamples; i++ {
		clk.advance(30 * time.Second)
		if i%degradedSamples == degradedSamples-1 {
			w.ObserveRTT(70 * time.Millisecond)
		} else {
			w.ObserveRTT(5 * time.Second)
		}
	}
	if cl.n != 0 {
		t.Fatalf("recycled despite good samples breaking every streak")
	}
}

func TestMemberWatchRecyclesStalled(t *testing.T) {
	w, cl, clk, tc := newTestWatch()
	clk.advance(stalledAfter - time.Second)
	w.Check()
	if cl.n != 0 {
		t.Fatalf("recycled before stalledAfter")
	}
	tc.AddRx(1 << 30) // traffic does not exempt a stalled keepalive
	clk.advance(2 * time.Second)
	w.Check()
	if cl.n != 1 {
		t.Fatalf("closed %d times, want 1", cl.n)
	}
}
