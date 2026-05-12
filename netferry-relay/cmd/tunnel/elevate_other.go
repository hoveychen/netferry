//go:build !darwin && !linux

package main

// requireRoot is a no-op on platforms where the privilege model differs
// (Windows uses UAC/admin token for WinDivert, handled by the firewall
// method itself). Kept as a function so callers compile cross-platform.
func requireRoot(string) {}
