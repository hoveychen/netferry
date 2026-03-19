//go:build darwin

package proxy

import (
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/firewall"
)

func init() {
	QueryOrigDstFunc = func(conn net.Conn) (string, int, error) {
		return firewall.QueryNATLook(conn)
	}
}

func detectDNSPlatform() []string {
	out, err := exec.Command("scutil", "--dns").Output()
	if err != nil {
		return nil
	}
	var servers []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nameserver[") {
			parts := strings.SplitN(line, " : ", 2)
			if len(parts) == 2 {
				ip := strings.TrimSpace(parts[1])
				if net.ParseIP(ip) != nil && !seen[ip] {
					servers = append(servers, ip)
					seen[ip] = true
				}
			}
		}
	}
	return servers
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
