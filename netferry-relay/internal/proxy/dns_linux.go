//go:build linux

package proxy

import (
	"net"
	"os"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/firewall"
)

func init() {
	QueryOrigDstFunc = func(conn net.Conn) (string, int, error) {
		return firewall.QueryOrigDst(conn)
	}
}

func detectDNSPlatform() []string {
	return nil // use parseResolvConf on Linux
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
