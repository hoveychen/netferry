// Package netmon monitors the OS for network interface/route changes.
// When a change is detected (e.g. WiFi switch), it signals via a channel
// so the tunnel can tear down the stale connection and reconnect.
package netmon

import (
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// Tuning for waitForStableFingerprint. A macOS screen-lock radio flap removes
// the interface address for anywhere from a few seconds to ~15s before
// restoring it, so the wait window must comfortably exceed that.
const (
	// fingerprintPollInterval is the delay between fingerprint samples while
	// waiting for the network to settle after a routing event.
	fingerprintPollInterval = 1 * time.Second
	// fingerprintMaxPolls bounds the settle wait (polls × interval ≈ 30s).
	// If the network never returns to baseline within the window, it is
	// treated as changed and the reconnect/backoff path takes over.
	fingerprintMaxPolls = 30
	// fingerprintStableCount is how many consecutive identical samples of a
	// *different, non-empty* network confirm a real switch.
	fingerprintStableCount = 3
)

// waitForStableFingerprint polls the network fingerprint after a routing
// event and decides whether the network really changed. sample and sleep are
// injected so the decision logic is unit-testable without real interfaces.
//
// Decision rules, in order, per sample:
//   - sample == baseline → the flap settled back to the same network: NOT
//     changed (this is the macOS screen-lock case: the radio drops the
//     address for several seconds, then restores it unchanged).
//   - sample is empty → degraded mid-flap state (no routable network). Not
//     evidence of a switch; keep waiting.
//   - sample is a different non-empty network, identical for
//     fingerprintStableCount consecutive polls → a real switch: changed.
//   - window exhausted (fingerprintMaxPolls) without returning to baseline →
//     changed. A genuinely dead network ends up here; the reconnect/backoff
//     path then retries until connectivity returns.
//   - done closed → abort, NOT changed (caller is shutting down).
func waitForStableFingerprint(baseline string, sample func() string, sleep func(time.Duration), done <-chan struct{}) bool {
	var last string
	streak := 0
	for i := 0; i < fingerprintMaxPolls; i++ {
		select {
		case <-done:
			return false
		default:
		}
		sleep(fingerprintPollInterval)
		cur := sample()
		if cur == baseline {
			return false
		}
		if cur == "" {
			streak, last = 0, ""
			continue
		}
		if cur == last {
			streak++
		} else {
			streak, last = 1, cur
		}
		if streak >= fingerprintStableCount {
			return true
		}
	}
	return true
}

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
// routable address, its name plus its sorted addresses. Interface and address
// ordering is normalized so the result depends only on *which* interface holds
// *which* addresses — not on the order the OS happens to report them.
//
// Link-local addresses (fe80::/10, 169.254/16) are excluded: they carry no
// routable network identity, and macOS auxiliary interfaces (awdl0, llw0)
// hold fe80 addresses permanently. If they counted, the mid-flap state of a
// screen-lock radio power-save (en0 address gone, awdl0/llw0 still up) would
// read as a different non-empty network instead of "no network".
//
// Two samples taken on the same network yield the same fingerprint even if
// interface/address events fired in between. A genuine network switch —
// different Wi-Fi, cable, tether, VPN — changes an interface's routable
// address and therefore the fingerprint, as does a total loss of connectivity
// (empty fingerprint).
func computeFingerprint(ifaces []ifaceSnapshot) string {
	entries := make([]string, 0, len(ifaces))
	for _, if_ := range ifaces {
		if !if_.up || if_.loopback {
			continue
		}
		addrs := make([]string, 0, len(if_.addrs))
		for _, a := range if_.addrs {
			if isLinkLocalCIDR(a) {
				continue
			}
			addrs = append(addrs, a)
		}
		if len(addrs) == 0 {
			continue
		}
		sort.Strings(addrs)
		entries = append(entries, if_.name+"="+strings.Join(addrs, ","))
	}
	sort.Strings(entries)
	return strings.Join(entries, ";")
}

// isLinkLocalCIDR reports whether the CIDR string (e.g. "fe80::1/64") is a
// link-local unicast address. Unparseable strings are kept (returns false) so
// an unexpected format errs toward counting as network identity.
func isLinkLocalCIDR(cidr string) bool {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return false
	}
	return p.Addr().IsLinkLocalUnicast()
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
