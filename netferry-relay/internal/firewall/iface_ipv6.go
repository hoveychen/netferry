package firewall

import (
	"os"
	"path/filepath"
)

// DisableSystemIPv6 reconfigures the OS so network interfaces no longer expose
// a global IPv6 unicast address to applications. This is a layer above the
// firewall blanket block: L3 drop stops *connections*, but applications can
// still enumerate the real GUA from local interfaces and leak it via
// application-layer payloads (WebRTC ICE candidates, P2P DHT announces,
// SDP offers, STUN bindings, telemetry logs). Removing the address at the
// OS level is the only way to make those leaks impossible.
//
// Pairs with firewall.SetIPv6Block — both should be applied under --no-ipv6.
// State is persisted to a file so a crashed process can still be cleaned up
// by the next run (call RestoreSystemIPv6 once at startup).
func DisableSystemIPv6() error { return disableSystemIPv6() }

// RestoreSystemIPv6 reverts whatever DisableSystemIPv6 installed and clears
// the on-disk state file. No-op if no state exists (safe to call even when
// Disable was never invoked — e.g. for startup crash-recovery).
func RestoreSystemIPv6() error { return restoreSystemIPv6() }

// ifaceIPv6StateFile is the path where per-platform state (saved original
// config) is persisted so a crashed/killed process can still be cleaned up
// on next launch. Lives next to the pf token file for the same reason.
func ifaceIPv6StateFile() string {
	return filepath.Join(os.TempDir(), "netferry-iface-ipv6.json")
}
