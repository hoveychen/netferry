// Package netmon monitors the OS for network interface/route changes.
// When a change is detected (e.g. WiFi switch), it signals via a channel
// so the tunnel can tear down the stale connection and reconnect.
package netmon

import (
	"net"
	"sort"
	"strings"
	"time"
)

// settleDelay is how long we wait after a routing-socket event before sampling
// the network fingerprint. macOS/Linux emit interface- and address-change
// events in a burst (and, on macOS screen lock, briefly drop then re-add the
// same address as the Wi-Fi radio enters power-save); sampling immediately
// would catch the mid-flap state. Waiting a beat lets the network settle so we
// compare the fingerprint of the *stable* state.
const settleDelay = 2 * time.Second

// ifaceSnapshot is a minimal, OS-agnostic view of one network interface used
// to compute the network fingerprint. Keeping it a plain struct lets the
// fingerprint logic be unit-tested without touching real OS interfaces.
type ifaceSnapshot struct {
	name     string
	up       bool
	loopback bool
	addrs    []string // CIDR strings, e.g. "10.0.1.71/24"
}

// computeFingerprint returns a stable string summarizing the machine's active
// network identity: for each UP, non-loopback interface that has at least one
// address, its name plus its sorted addresses. Interface and address ordering
// is normalized so the result depends only on *which* interface holds *which*
// addresses — not on the order the OS happens to report them.
//
// Two samples taken on the same network yield the same fingerprint even if
// interface/address events fired in between (e.g. a Wi-Fi power-save flap on
// screen lock removes and re-adds the same address). A genuine network switch
// — different Wi-Fi, cable, tether, VPN — changes an interface's address and
// therefore the fingerprint, as does a total loss of connectivity (the active
// interface drops out entirely).
func computeFingerprint(ifaces []ifaceSnapshot) string {
	entries := make([]string, 0, len(ifaces))
	for _, if_ := range ifaces {
		if !if_.up || if_.loopback || len(if_.addrs) == 0 {
			continue
		}
		addrs := append([]string(nil), if_.addrs...)
		sort.Strings(addrs)
		entries = append(entries, if_.name+"="+strings.Join(addrs, ","))
	}
	sort.Strings(entries)
	return strings.Join(entries, ";")
}

// currentFingerprint samples the machine's live interfaces and returns their
// fingerprint. An error reading interfaces yields "" — treated as "unknown",
// which differs from any real connected state and so errs toward reconnecting.
func currentFingerprint() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	snaps := make([]ifaceSnapshot, 0, len(ifaces))
	for _, iface := range ifaces {
		snap := ifaceSnapshot{
			name:     iface.Name,
			up:       iface.Flags&net.FlagUp != 0,
			loopback: iface.Flags&net.FlagLoopback != 0,
		}
		if addrs, err := iface.Addrs(); err == nil {
			for _, a := range addrs {
				snap.addrs = append(snap.addrs, a.String())
			}
		}
		snaps = append(snaps, snap)
	}
	return computeFingerprint(snaps)
}
