// Package netmon monitors the OS for network interface/route changes.
// When a change is detected (e.g. WiFi switch), it signals via a channel
// so the tunnel can tear down the stale connection and reconnect.
package netmon
