// probe tests platform capabilities needed for netferry-relay features.
// Run with root/admin privileges for complete results.
//
// Features being probed:
//   1. IPv6 support (sockets, firewall rules, original-dst lookup)
//   2. UDP proxy support (recvmsg, IP_RECVORIGDSTADDR, TPROXY UDP)
//   3. Port range filtering (firewall rule syntax)
//   4. Configurable TPROXY mark (policy routing with custom marks)
//   5. Local traffic protection (fib daddr type local)
//   6. Feature declaration (platform capability reporting)
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"time"
)

// Result represents a single probe result.
type Result struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	Pass     bool   `json:"pass"`
	Detail   string `json:"detail,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Report is the full probe report.
type Report struct {
	Platform  string    `json:"platform"`
	Arch      string    `json:"arch"`
	Timestamp string    `json:"timestamp"`
	IsRoot    bool      `json:"is_root"`
	Results   []Result  `json:"results"`
	Summary   Summary   `json:"summary"`
}

type Summary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

var results []Result

func probe(category, name string, fn func() (string, error)) {
	detail, err := fn()
	r := Result{
		Category: category,
		Name:     name,
		Pass:     err == nil,
		Detail:   detail,
	}
	if err != nil {
		r.Error = err.Error()
	}

	status := "PASS"
	if !r.Pass {
		status = "FAIL"
	}
	fmt.Printf("  [%s] %-50s", status, name)
	if detail != "" {
		fmt.Printf(" (%s)", detail)
	}
	if err != nil {
		fmt.Printf(" err=%s", err)
	}
	fmt.Println()

	results = append(results, r)
}

// --- Common probes (all platforms) ---

func probeCommon() {
	fmt.Println("\n=== Common: IPv6 Socket Support ===")

	probe("ipv6", "tcp6_listen_loopback", func() (string, error) {
		ln, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			return "", err
		}
		addr := ln.Addr().String()
		ln.Close()
		return "bound " + addr, nil
	})

	probe("ipv6", "udp6_listen_loopback", func() (string, error) {
		conn, err := net.ListenPacket("udp6", "[::1]:0")
		if err != nil {
			return "", err
		}
		addr := conn.LocalAddr().String()
		conn.Close()
		return "bound " + addr, nil
	})

	probe("ipv6", "tcp6_connect_loopback", func() (string, error) {
		ln, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			return "", fmt.Errorf("listen: %w", err)
		}
		defer ln.Close()

		go func() {
			c, _ := ln.Accept()
			if c != nil {
				c.Close()
			}
		}()

		conn, err := net.DialTimeout("tcp6", ln.Addr().String(), 2*time.Second)
		if err != nil {
			return "", fmt.Errorf("dial: %w", err)
		}
		conn.Close()
		return "connected to " + ln.Addr().String(), nil
	})

	probe("ipv6", "tcp6_listen_wildcard", func() (string, error) {
		ln, err := net.Listen("tcp6", "[::]:0")
		if err != nil {
			return "", err
		}
		addr := ln.Addr().String()
		ln.Close()
		return "bound " + addr, nil
	})

	probe("ipv6", "dual_stack_tcp_listen", func() (string, error) {
		// Try listening on tcp (not tcp4/tcp6) which should accept both
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			return "", err
		}
		addr := ln.Addr().String()
		ln.Close()
		return "bound " + addr, nil
	})

	probe("ipv6", "ipv6_interfaces", func() (string, error) {
		ifaces, err := net.Interfaces()
		if err != nil {
			return "", err
		}
		var v6Addrs []string
		for _, iface := range ifaces {
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() == nil && ipnet.IP.To16() != nil {
					v6Addrs = append(v6Addrs, fmt.Sprintf("%s@%s", ipnet.IP, iface.Name))
				}
			}
		}
		if len(v6Addrs) == 0 {
			return "", fmt.Errorf("no IPv6 addresses found on any interface")
		}
		detail := fmt.Sprintf("%d addrs: %s", len(v6Addrs), strings.Join(v6Addrs, ", "))
		if len(detail) > 120 {
			detail = detail[:120] + "..."
		}
		return detail, nil
	})

	probe("ipv6", "dns_resolve_ipv6", func() (string, error) {
		addrs, err := net.LookupIP("localhost")
		if err != nil {
			return "", err
		}
		var v4, v6 []string
		for _, a := range addrs {
			if a.To4() != nil {
				v4 = append(v4, a.String())
			} else {
				v6 = append(v6, a.String())
			}
		}
		return fmt.Sprintf("v4=%v v6=%v", v4, v6), nil
	})
}

func main() {
	fmt.Printf("NetFerry Probe Tool\n")
	fmt.Printf("Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Time:     %s\n", time.Now().Format(time.RFC3339))
	fmt.Printf("Root:     %v\n", isRoot())

	results = nil

	probeCommon()
	probePlatform() // defined in platform-specific files

	passed := 0
	failed := 0
	for _, r := range results {
		if r.Pass {
			passed++
		} else {
			failed++
		}
	}

	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Total: %d  Passed: %d  Failed: %d\n", len(results), passed, failed)

	report := Report{
		Platform:  runtime.GOOS,
		Arch:      runtime.GOARCH,
		Timestamp: time.Now().Format(time.RFC3339),
		IsRoot:    isRoot(),
		Results:   results,
		Summary: Summary{
			Total:  len(results),
			Passed: passed,
			Failed: failed,
		},
	}

	f, err := os.Create("probe_report.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot write report: %v\n", err)
		return
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.Encode(report)
	fmt.Printf("\nReport written to probe_report.json\n")
}
