package profile

import (
	"fmt"
	"net"
)

// AutoExcludeLANCIDRs returns a list of /16 CIDRs covering the local IPv4
// networks attached to this host, mirroring the desktop behavior in
// netferry-desktop/src-tauri/src/sidecar.rs (local_ip_address enumeration).
// Loopback and link-local interfaces are skipped.
func AutoExcludeLANCIDRs() []string {
	var cidrs []string
	seen := map[string]bool{}

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			if v4.IsLoopback() || v4.IsLinkLocalUnicast() {
				continue
			}
			cidr := fmt.Sprintf("%d.%d.0.0/16", v4[0], v4[1])
			if !seen[cidr] {
				seen[cidr] = true
				cidrs = append(cidrs, cidr)
			}
		}
	}
	return cidrs
}
