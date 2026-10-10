package main

import (
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/hoveychen/netferry/relay/internal/stats"
)

// Thresholds for recycling a pool member whose SSH connection is still alive
// but has degraded. Such a connection never returns EOF, so without this the
// member keeps carrying traffic at multi-second latency until the user
// reconnects by hand: in the field, an SSH connection that had been fine for
// ~22h jumped from ~70ms to 3–15s keepalive RTT with almost no traffic, while
// a fresh connection over the same path was immediately back at ~70ms.
const (
	// degradedRTT is the keepalive RTT at or above which a sample counts as bad.
	degradedRTT = 2 * time.Second
	// degradedSamples is how many bad samples in a row trigger a recycle.
	degradedSamples = 3
	// busyBytesPerSec exempts a member moving at least this much traffic
	// (rx + tx, averaged since the previous sample): keepalives share the TCP
	// connection with data, so a bulk transfer legitimately queues them for
	// seconds and must not be cut off.
	busyBytesPerSec = 64 << 10
	// stalledAfter recycles a member that has had no keepalive reply for this
	// long, whatever its traffic: the request is stuck, not merely slow.
	stalledAfter = 90 * time.Second
)

// memberWatch decides when a pool member's SSH connection has degraded enough
// to be replaced. It is fed keepalive RTT samples and closes the connection
// (via recycle) once; the member's Run then returns and reconnectPoolMember
// dials a fresh one.
type memberWatch struct {
	member, total int
	tc            *stats.TunnelCounters // traffic source; nil = treat as idle
	recycle       io.Closer
	now           func() time.Time

	mu        sync.Mutex
	lastReply time.Time
	lastBytes int64
	bad       int
	recycled  bool
}

func newMemberWatch(member, total int, tc *stats.TunnelCounters, recycle io.Closer) *memberWatch {
	w := &memberWatch{member: member, total: total, tc: tc, recycle: recycle, now: time.Now}
	w.lastReply = w.now()
	w.lastBytes = w.bytes()
	return w
}

func (w *memberWatch) bytes() int64 {
	if w.tc == nil {
		return 0
	}
	return w.tc.RxTotal.Load() + w.tc.TxTotal.Load()
}

// ObserveRTT records one keepalive reply.
func (w *memberWatch) ObserveRTT(rtt time.Duration) {
	w.mu.Lock()
	now := w.now()
	elapsed := now.Sub(w.lastReply)
	w.lastReply = now
	cur := w.bytes()
	moved := cur - w.lastBytes
	w.lastBytes = cur

	if rtt < degradedRTT {
		w.bad = 0
		w.mu.Unlock()
		return
	}
	var rate int64
	if elapsed > 0 {
		rate = int64(float64(moved) / elapsed.Seconds())
	}
	if rate >= busyBytesPerSec {
		w.bad = 0
		w.mu.Unlock()
		return
	}
	w.bad++
	bad := w.bad
	w.mu.Unlock()

	log.Printf("mux pool member %d/%d: keepalive rtt=%s with little traffic (%s/s), degraded %d/%d", w.member, w.total, rtt.Round(time.Millisecond), formatRate(rate), bad, degradedSamples)
	if bad >= degradedSamples {
		w.doRecycle("keepalive RTT stayed high")
	}
}

// Check recycles the member if no keepalive reply has arrived for stalledAfter.
// Called periodically.
func (w *memberWatch) Check() {
	w.mu.Lock()
	silent := w.now().Sub(w.lastReply)
	w.mu.Unlock()
	if silent >= stalledAfter {
		log.Printf("mux pool member %d/%d: no keepalive reply for %s", w.member, w.total, silent.Round(time.Second))
		w.doRecycle("keepalive stalled")
	}
}

func (w *memberWatch) doRecycle(reason string) {
	w.mu.Lock()
	if w.recycled {
		w.mu.Unlock()
		return
	}
	w.recycled = true
	w.mu.Unlock()
	log.Printf("mux pool member %d/%d: %s, recycling the SSH connection", w.member, w.total, reason)
	w.recycle.Close()
}

// start runs Check every interval until the returned stop is called.
func (w *memberWatch) start(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				w.Check()
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

func formatRate(bps int64) string {
	switch {
	case bps >= 1<<20:
		return fmt.Sprintf("%.1fMiB", float64(bps)/(1<<20))
	case bps >= 1<<10:
		return fmt.Sprintf("%.1fKiB", float64(bps)/(1<<10))
	default:
		return fmt.Sprintf("%dB", bps)
	}
}
