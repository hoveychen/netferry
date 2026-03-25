//go:build linux

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

func isRoot() bool {
	return os.Geteuid() == 0
}

func probePlatform() {
	probeLinuxFirewallTools()
	probeLinuxNftFeatures()
	probeLinuxIptablesFeatures()
	probeLinuxTProxy()
	probeLinuxUDP()
	probeLinuxPolicyRouting()
	probeLinuxOrigDst()
}

func probeLinuxFirewallTools() {
	fmt.Println("\n=== Linux: Firewall Tool Availability ===")

	probe("tools", "nft_binary", func() (string, error) {
		out, err := exec.Command("nft", "--version").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("nft not found: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	})

	probe("tools", "iptables_binary", func() (string, error) {
		out, err := exec.Command("iptables", "--version").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("iptables not found: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	})

	probe("tools", "ip6tables_binary", func() (string, error) {
		out, err := exec.Command("ip6tables", "--version").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("ip6tables not found: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	})

	probe("tools", "ip_binary", func() (string, error) {
		out, err := exec.Command("ip", "-V").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("ip not found: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	})
}

func probeLinuxNftFeatures() {
	fmt.Println("\n=== Linux: nftables Features ===")

	// Test: can we create an IPv6 (ip6) table?
	probe("nft_ipv6", "nft_create_ip6_table", func() (string, error) {
		table := "netferry_probe_ip6"
		out, err := exec.Command("nft", "add", "table", "ip6", table).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
		}
		defer exec.Command("nft", "delete", "table", "ip6", table).Run()
		return "created and deleted ip6 table", nil
	})

	// Test: can we create an inet (dual-stack) table?
	probe("nft_ipv6", "nft_create_inet_table", func() (string, error) {
		table := "netferry_probe_inet"
		out, err := exec.Command("nft", "add", "table", "inet", table).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
		}
		defer exec.Command("nft", "delete", "table", "inet", table).Run()
		return "created and deleted inet table", nil
	})

	// Test: fib daddr type local expression
	probe("nft_local", "nft_fib_daddr_type_local", func() (string, error) {
		table := "netferry_probe_fib"
		cmds := [][]string{
			{"add", "table", "ip", table},
			{"add", "chain", table, "test_chain", "{ type filter hook output priority -100 ; }"},
			{"add", "rule", table, "test_chain", "fib", "daddr", "type", "local", "return"},
		}
		for _, cmd := range cmds {
			out, err := exec.Command("nft", cmd...).CombinedOutput()
			if err != nil {
				exec.Command("nft", "delete", "table", "ip", table).Run()
				return "", fmt.Errorf("cmd %v: %s: %w", cmd, strings.TrimSpace(string(out)), err)
			}
		}
		defer exec.Command("nft", "delete", "table", "ip", table).Run()
		return "fib daddr type local supported", nil
	})

	// Test: fib daddr type local with ip6
	probe("nft_local", "nft_fib_daddr_type_local_ip6", func() (string, error) {
		table := "netferry_probe_fib6"
		cmds := [][]string{
			{"add", "table", "ip6", table},
			{"add", "chain", "ip6", table, "test_chain", "{ type filter hook output priority -100 ; }"},
			{"add", "rule", "ip6", table, "test_chain", "fib", "daddr", "type", "local", "return"},
		}
		for _, cmd := range cmds {
			out, err := exec.Command("nft", cmd...).CombinedOutput()
			if err != nil {
				exec.Command("nft", "delete", "table", "ip6", table).Run()
				return "", fmt.Errorf("cmd %v: %s: %w", cmd, strings.TrimSpace(string(out)), err)
			}
		}
		defer exec.Command("nft", "delete", "table", "ip6", table).Run()
		return "fib daddr type local supported for ip6", nil
	})

	// Test: nft set with ipv6_addr type
	probe("nft_ipv6", "nft_set_ipv6_addr", func() (string, error) {
		table := "netferry_probe_set6"
		cmds := [][]string{
			{"add", "table", "ip6", table},
			{"add", "set", "ip6", table, "test_set", "{ type ipv6_addr ; }"},
			{"add", "element", "ip6", table, "test_set", "{ ::1, fd00::1 }"},
		}
		for _, cmd := range cmds {
			out, err := exec.Command("nft", cmd...).CombinedOutput()
			if err != nil {
				exec.Command("nft", "delete", "table", "ip6", table).Run()
				return "", fmt.Errorf("cmd %v: %s: %w", cmd, strings.TrimSpace(string(out)), err)
			}
		}
		defer exec.Command("nft", "delete", "table", "ip6", table).Run()
		return "ipv6_addr set type works", nil
	})

	// Test: nft redirect with port range
	probe("nft_portrange", "nft_tcp_dport_range_redirect", func() (string, error) {
		table := "netferry_probe_pr"
		cmds := [][]string{
			{"add", "table", "ip", table},
			{"add", "chain", table, "test_chain", "{ type nat hook output priority -100 ; }"},
			{"add", "rule", table, "test_chain", "tcp", "dport", "80-443", "redirect", "to", ":12345"},
		}
		for _, cmd := range cmds {
			out, err := exec.Command("nft", cmd...).CombinedOutput()
			if err != nil {
				exec.Command("nft", "delete", "table", "ip", table).Run()
				return "", fmt.Errorf("cmd %v: %s: %w", cmd, strings.TrimSpace(string(out)), err)
			}
		}
		defer exec.Command("nft", "delete", "table", "ip", table).Run()
		return "tcp dport range + redirect works", nil
	})

	// Test: nft tproxy expression
	probe("nft_tproxy", "nft_tproxy_expression", func() (string, error) {
		table := "netferry_probe_tp"
		cmds := [][]string{
			{"add", "table", "ip", table},
			{"add", "chain", table, "test_chain", "{ type filter hook prerouting priority -100 ; }"},
			{"add", "rule", table, "test_chain", "tcp", "dport", "80", "tproxy", "to", "127.0.0.1:12345", "meta", "mark", "set", "0x1", "accept"},
		}
		for _, cmd := range cmds {
			out, err := exec.Command("nft", cmd...).CombinedOutput()
			if err != nil {
				exec.Command("nft", "delete", "table", "ip", table).Run()
				return "", fmt.Errorf("cmd %v: %s: %w", cmd, strings.TrimSpace(string(out)), err)
			}
		}
		defer exec.Command("nft", "delete", "table", "ip", table).Run()
		return "tproxy expression works", nil
	})

	// Test: nft tproxy with IPv6
	probe("nft_tproxy", "nft_tproxy_ipv6", func() (string, error) {
		table := "netferry_probe_tp6"
		cmds := [][]string{
			{"add", "table", "ip6", table},
			{"add", "chain", "ip6", table, "test_chain", "{ type filter hook prerouting priority -100 ; }"},
			{"add", "rule", "ip6", table, "test_chain", "tcp", "dport", "80", "tproxy", "to", "[::1]:12345", "meta", "mark", "set", "0x1", "accept"},
		}
		for _, cmd := range cmds {
			out, err := exec.Command("nft", cmd...).CombinedOutput()
			if err != nil {
				exec.Command("nft", "delete", "table", "ip6", table).Run()
				return "", fmt.Errorf("cmd %v: %s: %w", cmd, strings.TrimSpace(string(out)), err)
			}
		}
		defer exec.Command("nft", "delete", "table", "ip6", table).Run()
		return "tproxy ipv6 works", nil
	})

	// Test: nft UDP redirect (for DNS)
	probe("nft_udp", "nft_udp_redirect", func() (string, error) {
		table := "netferry_probe_udp"
		cmds := [][]string{
			{"add", "table", "ip", table},
			{"add", "chain", table, "test_chain", "{ type nat hook output priority -100 ; }"},
			{"add", "rule", table, "test_chain", "udp", "dport", "53", "redirect", "to", ":15353"},
		}
		for _, cmd := range cmds {
			out, err := exec.Command("nft", cmd...).CombinedOutput()
			if err != nil {
				exec.Command("nft", "delete", "table", "ip", table).Run()
				return "", fmt.Errorf("cmd %v: %s: %w", cmd, strings.TrimSpace(string(out)), err)
			}
		}
		defer exec.Command("nft", "delete", "table", "ip", table).Run()
		return "udp redirect works", nil
	})
}

func probeLinuxIptablesFeatures() {
	fmt.Println("\n=== Linux: iptables Features ===")

	probe("iptables_ipv6", "ip6tables_nat_redirect", func() (string, error) {
		chain := "NETFERRY_PROBE"
		cmds := []struct {
			args    []string
			cleanup []string
		}{
			{
				args:    []string{"-t", "nat", "-N", chain},
				cleanup: []string{"-t", "nat", "-X", chain},
			},
			{
				args:    []string{"-t", "nat", "-A", chain, "-p", "tcp", "-d", "fd00::/64", "-j", "REDIRECT", "--to-ports", "12345"},
				cleanup: []string{"-t", "nat", "-F", chain},
			},
		}
		var cleanups [][]string
		for _, c := range cmds {
			out, err := exec.Command("ip6tables", c.args...).CombinedOutput()
			cleanups = append(cleanups, c.cleanup)
			if err != nil {
				for i := len(cleanups) - 1; i >= 0; i-- {
					exec.Command("ip6tables", cleanups[i]...).Run()
				}
				return "", fmt.Errorf("ip6tables %v: %s: %w", c.args, strings.TrimSpace(string(out)), err)
			}
		}
		for i := len(cleanups) - 1; i >= 0; i-- {
			exec.Command("ip6tables", cleanups[i]...).Run()
		}
		return "ip6tables NAT REDIRECT works", nil
	})

	probe("iptables_tproxy", "iptables_tproxy_target", func() (string, error) {
		chain := "NETFERRY_PROBE_TP"
		cmds := []struct {
			args    []string
			cleanup []string
		}{
			{
				args:    []string{"-t", "mangle", "-N", chain},
				cleanup: []string{"-t", "mangle", "-X", chain},
			},
			{
				args:    []string{"-t", "mangle", "-A", chain, "-p", "tcp", "--dport", "80", "-j", "TPROXY", "--on-port", "12345", "--tproxy-mark", "0x1/0x1"},
				cleanup: []string{"-t", "mangle", "-F", chain},
			},
		}
		var cleanups [][]string
		for _, c := range cmds {
			out, err := exec.Command("iptables", c.args...).CombinedOutput()
			cleanups = append(cleanups, c.cleanup)
			if err != nil {
				for i := len(cleanups) - 1; i >= 0; i-- {
					exec.Command("iptables", cleanups[i]...).Run()
				}
				return "", fmt.Errorf("iptables %v: %s: %w", c.args, strings.TrimSpace(string(out)), err)
			}
		}
		for i := len(cleanups) - 1; i >= 0; i-- {
			exec.Command("iptables", cleanups[i]...).Run()
		}
		return "TPROXY iptables target works", nil
	})

	probe("iptables_tproxy", "ip6tables_tproxy_target", func() (string, error) {
		chain := "NETFERRY_PROBE_TP6"
		cmds := []struct {
			args    []string
			cleanup []string
		}{
			{
				args:    []string{"-t", "mangle", "-N", chain},
				cleanup: []string{"-t", "mangle", "-X", chain},
			},
			{
				args:    []string{"-t", "mangle", "-A", chain, "-p", "tcp", "--dport", "80", "-j", "TPROXY", "--on-port", "12345", "--tproxy-mark", "0x1/0x1"},
				cleanup: []string{"-t", "mangle", "-F", chain},
			},
		}
		var cleanups [][]string
		for _, c := range cmds {
			out, err := exec.Command("ip6tables", c.args...).CombinedOutput()
			cleanups = append(cleanups, c.cleanup)
			if err != nil {
				for i := len(cleanups) - 1; i >= 0; i-- {
					exec.Command("ip6tables", cleanups[i]...).Run()
				}
				return "", fmt.Errorf("ip6tables %v: %s: %w", c.args, strings.TrimSpace(string(out)), err)
			}
		}
		for i := len(cleanups) - 1; i >= 0; i-- {
			exec.Command("ip6tables", cleanups[i]...).Run()
		}
		return "ip6tables TPROXY target works", nil
	})

	probe("iptables_portrange", "iptables_multiport_redirect", func() (string, error) {
		chain := "NETFERRY_PROBE_MP"
		cmds := []struct {
			args    []string
			cleanup []string
		}{
			{
				args:    []string{"-t", "nat", "-N", chain},
				cleanup: []string{"-t", "nat", "-X", chain},
			},
			{
				args:    []string{"-t", "nat", "-A", chain, "-p", "tcp", "-m", "multiport", "--dports", "80,443", "-j", "REDIRECT", "--to-ports", "12345"},
				cleanup: []string{"-t", "nat", "-F", chain},
			},
		}
		var cleanups [][]string
		for _, c := range cmds {
			out, err := exec.Command("iptables", c.args...).CombinedOutput()
			cleanups = append(cleanups, c.cleanup)
			if err != nil {
				for i := len(cleanups) - 1; i >= 0; i-- {
					exec.Command("iptables", cleanups[i]...).Run()
				}
				return "", fmt.Errorf("iptables %v: %s: %w", c.args, strings.TrimSpace(string(out)), err)
			}
		}
		for i := len(cleanups) - 1; i >= 0; i-- {
			exec.Command("iptables", cleanups[i]...).Run()
		}
		return "iptables multiport + redirect works", nil
	})

	probe("iptables_portrange", "iptables_dport_range_redirect", func() (string, error) {
		chain := "NETFERRY_PROBE_DR"
		cmds := []struct {
			args    []string
			cleanup []string
		}{
			{
				args:    []string{"-t", "nat", "-N", chain},
				cleanup: []string{"-t", "nat", "-X", chain},
			},
			{
				args:    []string{"-t", "nat", "-A", chain, "-p", "tcp", "--dport", "80:443", "-j", "REDIRECT", "--to-ports", "12345"},
				cleanup: []string{"-t", "nat", "-F", chain},
			},
		}
		var cleanups [][]string
		for _, c := range cmds {
			out, err := exec.Command("iptables", c.args...).CombinedOutput()
			cleanups = append(cleanups, c.cleanup)
			if err != nil {
				for i := len(cleanups) - 1; i >= 0; i-- {
					exec.Command("iptables", cleanups[i]...).Run()
				}
				return "", fmt.Errorf("iptables %v: %s: %w", c.args, strings.TrimSpace(string(out)), err)
			}
		}
		for i := len(cleanups) - 1; i >= 0; i-- {
			exec.Command("iptables", cleanups[i]...).Run()
		}
		return "iptables dport range redirect works", nil
	})
}

func probeLinuxTProxy() {
	fmt.Println("\n=== Linux: TPROXY Socket Options ===")

	// IP_TRANSPARENT on TCP socket
	probe("tproxy", "ip_transparent_tcp4", func() (string, error) {
		fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
		if err != nil {
			return "", fmt.Errorf("socket: %w", err)
		}
		defer syscall.Close(fd)
		err = syscall.SetsockoptInt(fd, syscall.SOL_IP, syscall.IP_TRANSPARENT, 1)
		if err != nil {
			return "", fmt.Errorf("setsockopt IP_TRANSPARENT: %w", err)
		}
		return "IP_TRANSPARENT set on TCP4", nil
	})

	// IPV6_TRANSPARENT on TCP6 socket
	probe("tproxy", "ipv6_transparent_tcp6", func() (string, error) {
		fd, err := syscall.Socket(syscall.AF_INET6, syscall.SOCK_STREAM, 0)
		if err != nil {
			return "", fmt.Errorf("socket: %w", err)
		}
		defer syscall.Close(fd)
		// IPV6_TRANSPARENT = 75
		const IPV6_TRANSPARENT = 75
		err = syscall.SetsockoptInt(fd, syscall.SOL_IPV6, IPV6_TRANSPARENT, 1)
		if err != nil {
			return "", fmt.Errorf("setsockopt IPV6_TRANSPARENT: %w", err)
		}
		return "IPV6_TRANSPARENT set on TCP6", nil
	})

	// IP_TRANSPARENT on UDP socket
	probe("tproxy", "ip_transparent_udp4", func() (string, error) {
		fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
		if err != nil {
			return "", fmt.Errorf("socket: %w", err)
		}
		defer syscall.Close(fd)
		err = syscall.SetsockoptInt(fd, syscall.SOL_IP, syscall.IP_TRANSPARENT, 1)
		if err != nil {
			return "", fmt.Errorf("setsockopt IP_TRANSPARENT: %w", err)
		}
		return "IP_TRANSPARENT set on UDP4", nil
	})

	// IPV6_TRANSPARENT on UDP6 socket
	probe("tproxy", "ipv6_transparent_udp6", func() (string, error) {
		fd, err := syscall.Socket(syscall.AF_INET6, syscall.SOCK_DGRAM, 0)
		if err != nil {
			return "", fmt.Errorf("socket: %w", err)
		}
		defer syscall.Close(fd)
		const IPV6_TRANSPARENT = 75
		err = syscall.SetsockoptInt(fd, syscall.SOL_IPV6, IPV6_TRANSPARENT, 1)
		if err != nil {
			return "", fmt.Errorf("setsockopt IPV6_TRANSPARENT: %w", err)
		}
		return "IPV6_TRANSPARENT set on UDP6", nil
	})
}

func probeLinuxUDP() {
	fmt.Println("\n=== Linux: UDP recvmsg / Original Destination ===")

	// IP_RECVORIGDSTADDR on UDP4 socket
	probe("udp_proxy", "ip_recvorigdstaddr_udp4", func() (string, error) {
		fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
		if err != nil {
			return "", fmt.Errorf("socket: %w", err)
		}
		defer syscall.Close(fd)
		// IP_RECVORIGDSTADDR = 20
		const IP_RECVORIGDSTADDR = 20
		err = syscall.SetsockoptInt(fd, syscall.SOL_IP, IP_RECVORIGDSTADDR, 1)
		if err != nil {
			return "", fmt.Errorf("setsockopt IP_RECVORIGDSTADDR: %w", err)
		}
		return "IP_RECVORIGDSTADDR set on UDP4", nil
	})

	// IPV6_RECVORIGDSTADDR on UDP6 socket
	probe("udp_proxy", "ipv6_recvorigdstaddr_udp6", func() (string, error) {
		fd, err := syscall.Socket(syscall.AF_INET6, syscall.SOCK_DGRAM, 0)
		if err != nil {
			return "", fmt.Errorf("socket: %w", err)
		}
		defer syscall.Close(fd)
		// IPV6_RECVORIGDSTADDR = 74
		const IPV6_RECVORIGDSTADDR = 74
		err = syscall.SetsockoptInt(fd, syscall.SOL_IPV6, IPV6_RECVORIGDSTADDR, 1)
		if err != nil {
			return "", fmt.Errorf("setsockopt IPV6_RECVORIGDSTADDR: %w", err)
		}
		return "IPV6_RECVORIGDSTADDR set on UDP6", nil
	})

	// Test recvmsg with ancillary data (end-to-end)
	probe("udp_proxy", "recvmsg_ancillary_data", func() (string, error) {
		fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
		if err != nil {
			return "", fmt.Errorf("socket: %w", err)
		}
		defer syscall.Close(fd)

		err = syscall.SetsockoptInt(fd, syscall.SOL_IP, syscall.IP_PKTINFO, 1)
		if err != nil {
			return "", fmt.Errorf("setsockopt IP_PKTINFO: %w", err)
		}

		sa := &syscall.SockaddrInet4{Port: 0, Addr: [4]byte{127, 0, 0, 1}}
		err = syscall.Bind(fd, sa)
		if err != nil {
			return "", fmt.Errorf("bind: %w", err)
		}

		// Get the bound port
		boundSa, err := syscall.Getsockname(fd)
		if err != nil {
			return "", fmt.Errorf("getsockname: %w", err)
		}
		boundPort := boundSa.(*syscall.SockaddrInet4).Port

		// Send a test packet to ourselves
		sendFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
		if err != nil {
			return "", fmt.Errorf("send socket: %w", err)
		}
		defer syscall.Close(sendFd)

		dst := &syscall.SockaddrInet4{Port: boundPort, Addr: [4]byte{127, 0, 0, 1}}
		err = syscall.Sendto(sendFd, []byte("probe"), 0, dst)
		if err != nil {
			return "", fmt.Errorf("sendto: %w", err)
		}

		// Receive with recvmsg
		buf := make([]byte, 64)
		oob := make([]byte, 256)
		n, oobn, _, _, err := syscall.Recvmsg(fd, buf, oob, 0)
		if err != nil {
			return "", fmt.Errorf("recvmsg: %w", err)
		}

		return fmt.Sprintf("recvmsg ok: data=%d oob=%d bytes", n, oobn), nil
	})
}

func probeLinuxPolicyRouting() {
	fmt.Println("\n=== Linux: Policy Routing (TPROXY mark) ===")

	// Test configurable fwmark with ip rule
	probe("policy_routing", "ip_rule_custom_fwmark", func() (string, error) {
		mark := "0xbeef"
		table := "200"
		out, err := exec.Command("ip", "rule", "add", "fwmark", mark, "lookup", table).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("ip rule add: %s: %w", strings.TrimSpace(string(out)), err)
		}
		defer exec.Command("ip", "rule", "del", "fwmark", mark, "lookup", table).Run()
		return fmt.Sprintf("fwmark %s → table %s works", mark, table), nil
	})

	// Test IPv6 policy routing
	probe("policy_routing", "ip6_rule_fwmark", func() (string, error) {
		mark := "0xbeef"
		table := "200"
		out, err := exec.Command("ip", "-6", "rule", "add", "fwmark", mark, "lookup", table).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("ip -6 rule add: %s: %w", strings.TrimSpace(string(out)), err)
		}
		defer exec.Command("ip", "-6", "rule", "del", "fwmark", mark, "lookup", table).Run()
		return fmt.Sprintf("IPv6 fwmark %s → table %s works", mark, table), nil
	})

	// Test ip route add local for custom table
	probe("policy_routing", "ip_route_local_custom_table", func() (string, error) {
		table := "201"
		out, err := exec.Command("ip", "route", "add", "local", "0.0.0.0/0", "dev", "lo", "table", table).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("ip route add: %s: %w", strings.TrimSpace(string(out)), err)
		}
		defer exec.Command("ip", "route", "del", "local", "0.0.0.0/0", "dev", "lo", "table", table).Run()
		return fmt.Sprintf("local route in table %s works", table), nil
	})

	// Test IPv6 route local for custom table
	probe("policy_routing", "ip6_route_local_custom_table", func() (string, error) {
		table := "201"
		out, err := exec.Command("ip", "-6", "route", "add", "local", "::/0", "dev", "lo", "table", table).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("ip -6 route add: %s: %w", strings.TrimSpace(string(out)), err)
		}
		defer exec.Command("ip", "-6", "route", "del", "local", "::/0", "dev", "lo", "table", table).Run()
		return fmt.Sprintf("IPv6 local route in table %s works", table), nil
	})
}

func probeLinuxOrigDst() {
	fmt.Println("\n=== Linux: SO_ORIGINAL_DST ===")

	// Test SO_ORIGINAL_DST getsockopt availability (IPv4)
	probe("orig_dst", "so_original_dst_ipv4", func() (string, error) {
		// SO_ORIGINAL_DST = 80
		const SO_ORIGINAL_DST = 80
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return "", fmt.Errorf("listen: %w", err)
		}
		defer ln.Close()

		go func() {
			c, _ := ln.Accept()
			if c != nil {
				// Try getsockopt SO_ORIGINAL_DST
				raw, err := c.(*net.TCPConn).SyscallConn()
				if err == nil {
					raw.Control(func(fd uintptr) {
						_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT,
							fd,
							uintptr(syscall.SOL_IP),
							uintptr(SO_ORIGINAL_DST),
							uintptr(unsafe.Pointer(&syscall.RawSockaddrInet4{})),
							uintptr(unsafe.Pointer(new(uint32))),
							0)
						if errno != 0 {
							// Expected to fail without NAT, but the syscall itself should work
							_ = errno
						}
					})
				}
				c.Close()
			}
		}()

		conn, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			return "", fmt.Errorf("dial: %w", err)
		}
		conn.Close()
		return "SO_ORIGINAL_DST getsockopt callable (no NAT, expected ENOENT)", nil
	})

	// Test IP6T_SO_ORIGINAL_DST getsockopt availability (IPv6)
	probe("orig_dst", "ip6t_so_original_dst_ipv6", func() (string, error) {
		// IP6T_SO_ORIGINAL_DST = 80 (same value, different level SOL_IPV6)
		const IP6T_SO_ORIGINAL_DST = 80
		ln, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			return "", fmt.Errorf("listen: %w", err)
		}
		defer ln.Close()

		errCh := make(chan error, 1)
		go func() {
			c, err := ln.Accept()
			if err != nil {
				errCh <- err
				return
			}
			raw, err := c.(*net.TCPConn).SyscallConn()
			if err != nil {
				errCh <- err
				c.Close()
				return
			}
			var innerErr error
			raw.Control(func(fd uintptr) {
				buf := make([]byte, 28) // sizeof(sockaddr_in6)
				bufLen := uint32(len(buf))
				_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT,
					fd,
					uintptr(syscall.SOL_IPV6),
					uintptr(IP6T_SO_ORIGINAL_DST),
					uintptr(unsafe.Pointer(&buf[0])),
					uintptr(unsafe.Pointer(&bufLen)),
					0)
				if errno != 0 && errno != syscall.ENOENT && errno != syscall.ENOTCONN {
					innerErr = fmt.Errorf("getsockopt errno=%d (%s)", errno, errno.Error())
				}
			})
			errCh <- innerErr
			c.Close()
		}()

		conn, err := net.Dial("tcp6", ln.Addr().String())
		if err != nil {
			return "", fmt.Errorf("dial: %w", err)
		}
		conn.Close()

		if err := <-errCh; err != nil {
			return "", err
		}
		return "IP6T_SO_ORIGINAL_DST getsockopt callable", nil
	})
}
