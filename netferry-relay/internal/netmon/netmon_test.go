package netmon

import "testing"

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
