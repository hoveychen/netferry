package netmon

import (
	"testing"
	"time"
)

// A Wi-Fi power-save flap on screen lock briefly removes then re-adds the same
// address on the same interface, and the OS may report interfaces in a
// different order afterwards. The fingerprint of the *settled* network must be
// identical before and after such a flap, so netmon does NOT tear down the
// tunnel for a network that never actually changed.
func TestComputeFingerprint_FlapSameNetworkIsStable(t *testing.T) {
	before := []ifaceSnapshot{
		{name: "lo0", up: true, loopback: true, addrs: []string{"127.0.0.1/8", "::1/128"}},
		{name: "en0", up: true, addrs: []string{"10.0.1.71/24", "fe80::1/64"}},
	}
	// Same network, but the OS re-ordered interfaces and re-ordered addrs
	// after the radio power-save flap settled.
	after := []ifaceSnapshot{
		{name: "en0", up: true, addrs: []string{"fe80::1/64", "10.0.1.71/24"}},
		{name: "lo0", up: true, loopback: true, addrs: []string{"::1/128", "127.0.0.1/8"}},
	}
	if computeFingerprint(before) != computeFingerprint(after) {
		t.Fatalf("flap on same network changed fingerprint:\n before=%q\n after =%q",
			computeFingerprint(before), computeFingerprint(after))
	}
}

// Actually switching networks (different Wi-Fi / cable / tether) changes the
// primary interface's address, which MUST change the fingerprint so netmon
// signals a reconnect.
func TestComputeFingerprint_RealSwitchChanges(t *testing.T) {
	before := []ifaceSnapshot{
		{name: "en0", up: true, addrs: []string{"10.0.1.71/24"}},
	}
	after := []ifaceSnapshot{
		{name: "en0", up: true, addrs: []string{"192.168.8.50/24"}},
	}
	if computeFingerprint(before) == computeFingerprint(after) {
		t.Fatalf("network switch did not change fingerprint: %q", computeFingerprint(before))
	}
}

// Loopback never carries the real network identity; toggling/altering it must
// not affect the fingerprint.
func TestComputeFingerprint_LoopbackIgnored(t *testing.T) {
	withLo := []ifaceSnapshot{
		{name: "lo0", up: true, loopback: true, addrs: []string{"127.0.0.1/8"}},
		{name: "en0", up: true, addrs: []string{"10.0.1.71/24"}},
	}
	withoutLo := []ifaceSnapshot{
		{name: "en0", up: true, addrs: []string{"10.0.1.71/24"}},
	}
	if computeFingerprint(withLo) != computeFingerprint(withoutLo) {
		t.Fatalf("loopback affected fingerprint")
	}
}

// A down interface (e.g. an unused Thunderbolt bridge) contributes no active
// network identity and must be excluded, even if it still lists an address.
func TestComputeFingerprint_DownInterfaceIgnored(t *testing.T) {
	base := []ifaceSnapshot{
		{name: "en0", up: true, addrs: []string{"10.0.1.71/24"}},
	}
	withDown := []ifaceSnapshot{
		{name: "en0", up: true, addrs: []string{"10.0.1.71/24"}},
		{name: "bridge0", up: false, addrs: []string{"192.168.99.1/24"}},
	}
	if computeFingerprint(base) != computeFingerprint(withDown) {
		t.Fatalf("down interface affected fingerprint")
	}
}

// A total loss of connectivity (interface goes down, no active addresses left)
// must produce a different fingerprint from the connected state so a genuine
// disconnect is still acted on.
func TestComputeFingerprint_ConnectivityLostChanges(t *testing.T) {
	up := []ifaceSnapshot{
		{name: "en0", up: true, addrs: []string{"10.0.1.71/24"}},
	}
	down := []ifaceSnapshot{
		{name: "en0", up: false, addrs: []string{"10.0.1.71/24"}},
	}
	if computeFingerprint(up) == computeFingerprint(down) {
		t.Fatalf("connectivity loss did not change fingerprint")
	}
}

// Interfaces carrying only link-local addresses (macOS awdl0/llw0, fe80::/10,
// 169.254/16) hold no routable network identity. They must not contribute to
// the fingerprint: during a screen-lock radio flap, en0 loses its address
// while awdl0/llw0 keep their fe80 ones — if they counted, the mid-flap state
// would read as a *different non-empty network* and defeat the flap guard.
func TestComputeFingerprint_LinkLocalOnlyIfaceIgnored(t *testing.T) {
	base := []ifaceSnapshot{
		{name: "en0", up: true, addrs: []string{"10.0.1.71/24"}},
	}
	withAux := []ifaceSnapshot{
		{name: "en0", up: true, addrs: []string{"10.0.1.71/24"}},
		{name: "awdl0", up: true, addrs: []string{"fe80::1c2d:3e4f:5a6b:7c8d/64"}},
		{name: "llw0", up: true, addrs: []string{"fe80::2/64"}},
		{name: "bridge1", up: true, addrs: []string{"169.254.10.20/16"}},
	}
	if computeFingerprint(base) != computeFingerprint(withAux) {
		t.Fatalf("link-local-only interfaces affected fingerprint:\n base=%q\n aux =%q",
			computeFingerprint(base), computeFingerprint(withAux))
	}
	// And the mid-flap state (en0 gone, aux ifaces remain) must read as
	// "no network", i.e. empty fingerprint — not as a new network.
	midFlap := []ifaceSnapshot{
		{name: "awdl0", up: true, addrs: []string{"fe80::1c2d:3e4f:5a6b:7c8d/64"}},
		{name: "llw0", up: true, addrs: []string{"fe80::2/64"}},
	}
	if fp := computeFingerprint(midFlap); fp != "" {
		t.Fatalf("mid-flap link-local-only state should be empty fingerprint, got %q", fp)
	}
}

// ── waitForStableFingerprint ──────────────────────────────────────────────────

// seqSampler returns a sample func that walks the given sequence and sticks
// on the last value once exhausted.
func seqSampler(seq []string) func() string {
	i := 0
	return func() string {
		if i < len(seq) {
			v := seq[i]
			i++
			return v
		}
		return seq[len(seq)-1]
	}
}

func noSleep(time.Duration) {}

// Screen-lock flap: the address vanishes for a while (empty fingerprint),
// then comes back identical. Must be reported as NOT changed, even when the
// degraded window is much longer than the old 2s settle delay.
func TestWaitForStable_FlapSettlesBack(t *testing.T) {
	base := "en0=10.0.1.71/24"
	seq := make([]string, 0, 16)
	for i := 0; i < 12; i++ { // 12s of degraded state — longer than old settle
		seq = append(seq, "")
	}
	seq = append(seq, base)
	if waitForStableFingerprint(base, seqSampler(seq), noSleep, nil) {
		t.Fatalf("flap that settled back to baseline was reported as changed")
	}
}

// Real switch: a different non-empty network shows up and stays. Must be
// reported as changed once it has been stable for a few samples.
func TestWaitForStable_RealSwitchConfirmed(t *testing.T) {
	base := "en0=10.0.1.71/24"
	seq := []string{"", "en0=192.168.8.50/24", "en0=192.168.8.50/24", "en0=192.168.8.50/24"}
	if !waitForStableFingerprint(base, seqSampler(seq), noSleep, nil) {
		t.Fatalf("stable new network was not reported as changed")
	}
}

// A degraded (empty) state alone must not be confirmed as a new network —
// it is a radio in power-save, not a network switch. Only the window running
// out ends the wait (reported changed, so a genuinely dead network still
// triggers the reconnect/backoff path).
func TestWaitForStable_DegradedAloneOnlyTimesOut(t *testing.T) {
	base := "en0=10.0.1.71/24"
	polls := 0
	sample := func() string { polls++; return "" }
	if !waitForStableFingerprint(base, sample, noSleep, nil) {
		t.Fatalf("permanently degraded network should time out as changed")
	}
	if polls < fingerprintMaxPolls {
		t.Fatalf("degraded state was confirmed early after %d polls; want full window %d",
			polls, fingerprintMaxPolls)
	}
}

// Closing done aborts the wait without signalling a change.
func TestWaitForStable_DoneAborts(t *testing.T) {
	done := make(chan struct{})
	close(done)
	sample := func() string { return "en0=192.168.8.50/24" }
	if waitForStableFingerprint("en0=10.0.1.71/24", sample, noSleep, done) {
		t.Fatalf("aborted wait must not report changed")
	}
}
