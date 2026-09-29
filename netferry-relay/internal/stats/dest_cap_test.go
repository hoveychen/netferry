package stats

import (
	"fmt"
	"testing"
	"time"
)

// TestDestSnapshotTruncatedToTopN verifies buildDestSnapshotLocked caps the
// emitted snapshot at the top-N destinations by total bytes, so a long browsing
// session can't push a multi-thousand-entry payload to every SSE client.
func TestDestSnapshotTruncatedToTopN(t *testing.T) {
	const wantCap = 500
	c := NewCounters()
	now := time.Now()
	// 600 destinations, rxBytes == index so higher index = more traffic.
	for i := 0; i < 600; i++ {
		key := fmt.Sprintf("host-%03d.example.com", i)
		c.dests[key] = &destStats{
			host:        key,
			rxBytes:     int64(i),
			firstSeenAt: now,
			lastSeenAt:  now,
		}
	}

	snaps := c.buildDestSnapshotLocked(nil, nil, 0)

	if len(snaps) != wantCap {
		t.Fatalf("snapshot length = %d, want %d (top-N truncation)", len(snaps), wantCap)
	}
	// Snapshot is sorted by bytes desc; the survivors must be the heaviest 500,
	// i.e. indices 100..599. The lightest survivor is index 100.
	last := snaps[len(snaps)-1]
	if last.RxBytes+last.TxBytes < 100 {
		t.Fatalf("truncation dropped the wrong entries: lightest survivor has %d bytes, want the top-500 by bytes (>=100)", last.RxBytes+last.TxBytes)
	}
}

// TestDestMapEvictionBounded verifies the per-destination map does not grow
// without bound as ephemeral connections churn, and that a destination with a
// live connection is never evicted out from under an active flow.
func TestDestMapEvictionBounded(t *testing.T) {
	const wantMax = 2048
	c := NewCounters()

	// A long-lived active connection whose destination must survive eviction.
	keepHost := "keep.example.com"
	keepID := c.ConnOpen("10.0.0.1:5555", "1.1.1.1:443", keepHost, 0)

	// Churn many ephemeral destinations (open then immediately close).
	for i := 0; i < 3000; i++ {
		host := fmt.Sprintf("eph-%04d.example.com", i)
		id := c.ConnOpen("10.0.0.1:6000", "2.2.2.2:443", host, 0)
		c.ConnClose(id, "10.0.0.1:6000", "2.2.2.2:443")
	}

	c.mu.Lock()
	n := len(c.dests)
	_, keepPresent := c.dests[keepHost]
	c.mu.Unlock()

	if n > wantMax {
		t.Fatalf("dests map size = %d, want <= %d (LRU eviction)", n, wantMax)
	}
	if !keepPresent {
		t.Fatalf("destination %q with an active connection was evicted", keepHost)
	}

	// Clean up the active connection.
	c.ConnClose(keepID, "10.0.0.1:5555", "1.1.1.1:443")
}
