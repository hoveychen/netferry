//go:build !darwin && !linux && !windows

package firewall

import "log"

func disableSystemIPv6() error {
	log.Printf("iface_ipv6: interface-level disable not supported on this platform; application-layer leaks not prevented")
	return nil
}

func restoreSystemIPv6() error { return nil }
