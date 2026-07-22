package stats

import (
	"testing"
	"time"
)

// TestEffectiveKeepaliveRTT_PrefersCtrlPath verifies that the RTT driving the
// "keepalive RTT is high" warning reflects the ctrl-path RTT when available,
// not the data-path RTT. The data connection carries bulk traffic and its
// keepalive RTT balloons under load (expected bufferbloat); the warning must
// not fire on that. When a ctrl-path measurement exists it wins.
func TestEffectiveKeepaliveRTT_PrefersCtrlPath(t *testing.T) {
	c := &Counters{}

	// Non-split mode: no ctrl measurement, so the effective RTT falls back to
	// the data-path RTT (original single-connection behavior).
	c.ObserveKeepaliveRTT(3 * time.Second)
	if got := c.EffectiveKeepaliveRTT(); got != 3*time.Second {
		t.Fatalf("fallback: EffectiveKeepaliveRTT() = %s, want 3s (data-path RTT)", got)
	}

	// Split mode: the data path is bufferbloated (3s) but the ctrl path is
	// healthy (50ms). The effective RTT must reflect the ctrl path, so the
	// warning does NOT cry wolf on expected data-path queuing.
	c.ObserveCtrlKeepaliveRTT(50 * time.Millisecond)
	if got := c.EffectiveKeepaliveRTT(); got != 50*time.Millisecond {
		t.Fatalf("split: EffectiveKeepaliveRTT() = %s, want 50ms (ctrl-path RTT, not the 3s data-path bufferbloat)", got)
	}

	// The data-path RTT itself is preserved for its other consumers (TUI /
	// pool selection) — it must still read 3s.
	if got := c.LastKeepaliveRTT(); got != 3*time.Second {
		t.Fatalf("data-path RTT clobbered: LastKeepaliveRTT() = %s, want 3s", got)
	}
}
