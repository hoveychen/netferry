//go:build linux

package firewall

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// linuxIPv6State captures the original sysctl values for each interface we
// touched. The "all" and "default" pseudo-interfaces are included so that
// newly-appearing interfaces inherit the same lock-down — but their original
// values must still be saved and restored.
type linuxIPv6State struct {
	// Keyed by interface name. Each inner map is sysctl-leaf → original value
	// as a decimal string (sysctls we touch here are all ints).
	Ifaces map[string]map[string]string `json:"ifaces"`
}

// sysctls that jointly ensure the interface cannot acquire or retain any
// global IPv6 address. disable_ipv6=1 flushes existing v6 addresses and blocks
// new ones; accept_ra=0 stops router advertisements from re-configuring an
// address even if disable_ipv6 is later flipped back (defense in depth).
var linuxIPv6SysctlLeaves = []string{"disable_ipv6", "accept_ra"}

func disableSystemIPv6() error {
	ifaces, err := listLinuxIPv6Ifaces()
	if err != nil {
		return fmt.Errorf("enumerate interfaces: %w", err)
	}
	if len(ifaces) == 0 {
		return nil
	}

	state := linuxIPv6State{Ifaces: make(map[string]map[string]string, len(ifaces))}
	for _, ifname := range ifaces {
		saved := make(map[string]string, len(linuxIPv6SysctlLeaves))
		for _, leaf := range linuxIPv6SysctlLeaves {
			v, err := readIPv6Sysctl(ifname, leaf)
			if err != nil {
				// Leaf might not exist for some pseudo-devices; skip.
				continue
			}
			saved[leaf] = v
		}
		if len(saved) > 0 {
			state.Ifaces[ifname] = saved
		}
	}

	if err := writeLinuxIPv6State(state); err != nil {
		return fmt.Errorf("persist state: %w", err)
	}

	// Apply lock-down. disable_ipv6=1 first, then accept_ra=0.
	// Order matters slightly: flipping disable_ipv6 drops v6 addresses
	// immediately; clearing accept_ra after is sufficient.
	for ifname := range state.Ifaces {
		if err := writeIPv6Sysctl(ifname, "disable_ipv6", "1"); err != nil {
			log.Printf("iface_ipv6: disable_ipv6 %q: %v", ifname, err)
		}
		if err := writeIPv6Sysctl(ifname, "accept_ra", "0"); err != nil {
			// accept_ra may not exist for every iface (e.g. loopback-like).
			_ = err
		}
	}
	log.Printf("iface_ipv6: disabled IPv6 on %d interface(s)", len(state.Ifaces))
	return nil
}

func restoreSystemIPv6() error {
	raw, err := os.ReadFile(ifaceIPv6StateFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read state: %w", err)
	}
	var state linuxIPv6State
	if err := json.Unmarshal(raw, &state); err != nil {
		os.Remove(ifaceIPv6StateFile())
		return fmt.Errorf("parse state: %w", err)
	}

	for ifname, saved := range state.Ifaces {
		for leaf, val := range saved {
			if err := writeIPv6Sysctl(ifname, leaf, val); err != nil {
				log.Printf("iface_ipv6: restore %s/%s=%s: %v", ifname, leaf, val, err)
			}
		}
	}
	os.Remove(ifaceIPv6StateFile())
	log.Printf("iface_ipv6: restored IPv6 on %d interface(s)", len(state.Ifaces))
	return nil
}

// listLinuxIPv6Ifaces returns the interface names we want to lock down.
// Includes "all" and "default" (kernel pseudo-entries that affect newly-
// appearing interfaces) plus every real interface under /sys/class/net,
// skipping loopback.
func listLinuxIPv6Ifaces() ([]string, error) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil, err
	}
	out := []string{"all", "default"}
	for _, e := range entries {
		name := e.Name()
		if name == "lo" {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func ipv6SysctlPath(ifname, leaf string) string {
	return filepath.Join("/proc/sys/net/ipv6/conf", ifname, leaf)
}

func readIPv6Sysctl(ifname, leaf string) (string, error) {
	b, err := os.ReadFile(ipv6SysctlPath(ifname, leaf))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func writeIPv6Sysctl(ifname, leaf, value string) error {
	return os.WriteFile(ipv6SysctlPath(ifname, leaf), []byte(value), 0o644)
}

func writeLinuxIPv6State(state linuxIPv6State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(ifaceIPv6StateFile(), data, 0o600)
}
