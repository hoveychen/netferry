//go:build windows

package proxy

import (
	"net"
	"os"
	"strings"
)

func init() {
	// On Windows, QueryOrigDstFunc is not used — the SOCKS5 handshake carries
	// the destination explicitly. Leave it nil; listener.go handles nil gracefully.
	QueryOrigDstFunc = nil
}

func detectDNSPlatform() []string {
	return nil // use parseResolvConf fallback
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func splitLines(s string) []string {
	return strings.Split(s, "\n")
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}

// Ensure net is imported (used by parseResolvConf).
var _ = net.ParseIP
