//go:build !windows

package stats

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"time"
)

// lookupProcesses resolves process names for the given set of local TCP
// source addresses (e.g. "127.0.0.1:56789" or "192.168.1.100:56789").
// Returns addr -> process name. Uses lsof on macOS and Linux.
//
// With transparent proxy (pf/nft redirect), the client's socket uses the
// machine's real interface IP (not 127.0.0.1), so we must query lsof for
// all unique source IPs present in the address set.
func lookupProcesses(addrs map[string]struct{}) map[string]string {
	if len(addrs) == 0 {
		return nil
	}

	// Extract unique source IPs so a single lsof call covers both SOCKS5
	// (127.0.0.1) and transparent proxy (real interface IPs).
	ips := make(map[string]struct{})
	for addr := range addrs {
		if host, _, err := net.SplitHostPort(addr); err == nil {
			ips[host] = struct{}{}
		}
	}
	if len(ips) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// -i TCP@ip: filter by host; multiple -i flags are OR'd.
	// -n: no DNS resolution
	// -P: no port-name resolution
	// -Fcn: machine-parseable output with command and name fields
	args := make([]string, 0, len(ips)*2+3)
	for ip := range ips {
		args = append(args, "-i", "TCP@"+ip)
	}
	args = append(args, "-n", "-P", "-Fcn")

	out, err := exec.CommandContext(ctx, "lsof", args...).Output()
	if err != nil {
		return nil
	}

	return parseLsofOutput(out, addrs)
}

// parseLsofOutput parses the machine-readable output of `lsof -Fcn` and
// returns a map from local TCP address to process (command) name, filtered
// to only include addresses present in the addrs set.
//
// lsof -Fcn output format (one field per line, prefixed by field letter):
//
//	p<PID>
//	c<command>
//	f<fd>
//	n<localIP:port>-><remoteIP:port>   (ESTABLISHED)
//	n<localIP:port>                      (LISTEN — no arrow)
func parseLsofOutput(out []byte, addrs map[string]struct{}) map[string]string {
	result := make(map[string]string)
	var cmd string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case 'p':
			cmd = "" // new process record — reset
		case 'c':
			cmd = line[1:]
		case 'n':
			// Established: "127.0.0.1:56789->127.0.0.1:12300"
			// Listen:       "*:12300" or "127.0.0.1:12300"
			name := line[1:]
			arrow := strings.Index(name, "->")
			if arrow < 0 {
				continue // LISTEN socket, skip
			}
			localAddr := name[:arrow]
			if _, ok := addrs[localAddr]; ok && cmd != "" {
				result[localAddr] = cmd
			}
		}
	}
	return result
}
