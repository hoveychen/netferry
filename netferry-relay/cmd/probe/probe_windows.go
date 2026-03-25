//go:build windows

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func isRoot() bool {
	// Check if running as administrator by trying to open a privileged file
	_, err := os.Open(`\\.\PHYSICALDRIVE0`)
	return err == nil
}

func probePlatform() {
	probeWindowsTools()

	// Extract embedded WinDivert DLL + driver to temp dir before probing
	fmt.Println("\n=== Windows: Extracting Embedded WinDivert ===")
	wdDir, err := extractWinDivert()
	if err != nil {
		fmt.Printf("  [WARN] Failed to extract WinDivert: %v\n", err)
	} else {
		fmt.Printf("  [OK]   Extracted to %s\n", wdDir)
		defer os.RemoveAll(wdDir)
	}

	probeWindowsWinDivert()
	probeWindowsIPv6()
	probeWindowsDNS()
	probeWindowsRegistry()
	probeWindowsUDP()
}

func probeWindowsTools() {
	fmt.Println("\n=== Windows: Tool Availability ===")

	probe("tools", "netsh_binary", func() (string, error) {
		out, err := exec.Command("netsh", "interface", "show", "interface").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("netsh: %w", err)
		}
		lines := strings.Split(string(out), "\n")
		count := 0
		for _, line := range lines {
			if strings.Contains(line, "Connected") || strings.Contains(line, "Disconnected") {
				count++
			}
		}
		return fmt.Sprintf("%d interfaces found", count), nil
	})

	probe("tools", "powershell_available", func() (string, error) {
		out, err := exec.Command("powershell", "-Command", "$PSVersionTable.PSVersion.ToString()").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("powershell: %w", err)
		}
		return "PowerShell " + strings.TrimSpace(string(out)), nil
	})
}

func probeWindowsWinDivert() {
	fmt.Println("\n=== Windows: WinDivert Driver ===")

	probe("windivert", "windivert_dll_exists", func() (string, error) {
		// LoadDLL searches PATH (which now includes our extracted temp dir)
		dll, err := syscall.LoadDLL("WinDivert.dll")
		if err != nil {
			// Fallback: check common locations
			paths := []string{
				"WinDivert.dll",
				filepath.Join(os.Getenv("SYSTEMROOT"), "System32", "WinDivert.dll"),
			}
			if exe, err2 := os.Executable(); err2 == nil {
				paths = append(paths, filepath.Join(filepath.Dir(exe), "WinDivert.dll"))
			}
			for _, p := range paths {
				if info, err2 := os.Stat(p); err2 == nil && info.Size() > 0 {
					return "found at " + p, nil
				}
			}
			return "", fmt.Errorf("WinDivert.dll not loadable: %w", err)
		}
		dll.Release()
		return "WinDivert.dll loaded from PATH (embedded extract)", nil
	})

	// Test: can we load the WinDivert DLL
	probe("windivert", "windivert_loadable", func() (string, error) {
		dll, err := syscall.LoadDLL("WinDivert.dll")
		if err != nil {
			return "", fmt.Errorf("LoadDLL: %w", err)
		}
		defer dll.Release()

		// Check for key functions
		funcs := []string{"WinDivertOpen", "WinDivertSend", "WinDivertRecv", "WinDivertClose",
			"WinDivertHelperCalcChecksums"}
		var found []string
		for _, name := range funcs {
			if _, err := dll.FindProc(name); err == nil {
				found = append(found, name)
			}
		}
		return fmt.Sprintf("loaded, %d/%d functions found: %v", len(found), len(funcs), found), nil
	})

	// Test: WinDivert IPv6 filter syntax via WinDivertOpen + immediate close.
	// WinDivertHelperCheckFilter is a header-only inline in 2.2, not a DLL export.
	// So we validate filters by actually opening a handle (requires admin).
	// We use WINDIVERT_FLAG_RECV_ONLY|WINDIVERT_FLAG_SNIFF to avoid disrupting traffic.
	probe("windivert", "windivert_ipv6_filter_syntax", func() (string, error) {
		dll, err := syscall.LoadDLL("WinDivert.dll")
		if err != nil {
			return "", fmt.Errorf("LoadDLL: %w", err)
		}
		defer dll.Release()

		openProc, err := dll.FindProc("WinDivertOpen")
		if err != nil {
			return "", fmt.Errorf("FindProc WinDivertOpen: %w", err)
		}
		closeProc, err := dll.FindProc("WinDivertClose")
		if err != nil {
			return "", fmt.Errorf("FindProc WinDivertClose: %w", err)
		}

		filters := []struct {
			name   string
			filter string
		}{
			{"basic_ipv6", "outbound and ipv6"},
			{"ipv6_tcp", "outbound and ipv6 and tcp"},
			{"ipv6_udp_53", "outbound and ipv6 and udp.DstPort == 53"},
			{"mixed_ip_ipv6", "outbound and (ip or ipv6) and (tcp or udp)"},
		}

		// WINDIVERT_FLAG_SNIFF (1) | WINDIVERT_FLAG_RECV_ONLY (4) = 5
		const flags = 5

		var passed []string
		var lastErr string
		for _, f := range filters {
			filterBytes := append([]byte(f.filter), 0)
			handle, _, callErr := openProc.Call(
				uintptr(unsafe.Pointer(&filterBytes[0])),
				0,     // WINDIVERT_LAYER_NETWORK
				0,     // priority
				flags, // sniff + recv-only
			)
			if handle == 0 || handle == ^uintptr(0) { // INVALID_HANDLE_VALUE
				lastErr = fmt.Sprintf("%s: %v", f.name, callErr)
				continue
			}
			closeProc.Call(handle)
			passed = append(passed, f.name)
		}

		if len(passed) == len(filters) {
			return fmt.Sprintf("all %d IPv6 filter variants valid", len(filters)), nil
		}
		if len(passed) == 0 {
			return "", fmt.Errorf("no filters passed (need admin?): %s", lastErr)
		}
		return fmt.Sprintf("%d/%d filters valid: %v", len(passed), len(filters), passed),
			fmt.Errorf("some IPv6 filters rejected: %s", lastErr)
	})
}

func probeWindowsIPv6() {
	fmt.Println("\n=== Windows: IPv6 Network Support ===")

	probe("ipv6", "ipv6_enabled_interfaces", func() (string, error) {
		out, err := exec.Command("netsh", "interface", "ipv6", "show", "address").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("netsh ipv6: %w", err)
		}
		lines := strings.Split(string(out), "\n")
		var addrs []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "::") && !strings.HasPrefix(line, "---") {
				addrs = append(addrs, line)
			}
		}
		if len(addrs) == 0 {
			return "", fmt.Errorf("no IPv6 addresses found")
		}
		detail := fmt.Sprintf("%d IPv6 address lines found", len(addrs))
		return detail, nil
	})

	probe("ipv6", "tcp6_listen_connect_windows", func() (string, error) {
		ln, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			return "", fmt.Errorf("listen: %w", err)
		}
		defer ln.Close()

		go func() {
			c, _ := ln.Accept()
			if c != nil {
				c.Write([]byte("pong"))
				c.Close()
			}
		}()

		conn, err := net.DialTimeout("tcp6", ln.Addr().String(), 2*time.Second)
		if err != nil {
			return "", fmt.Errorf("dial: %w", err)
		}
		buf := make([]byte, 4)
		n, _ := conn.Read(buf)
		conn.Close()
		return fmt.Sprintf("TCP6 loopback works, received %d bytes", n), nil
	})

	probe("ipv6", "udp6_send_recv_windows", func() (string, error) {
		conn, err := net.ListenPacket("udp6", "[::1]:0")
		if err != nil {
			return "", fmt.Errorf("listen: %w", err)
		}
		defer conn.Close()

		addr := conn.LocalAddr()
		_, err = conn.WriteTo([]byte("probe"), addr)
		if err != nil {
			return "", fmt.Errorf("write: %w", err)
		}

		buf := make([]byte, 64)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return "", fmt.Errorf("read: %w", err)
		}
		return fmt.Sprintf("UDP6 loopback works, echoed %d bytes", n), nil
	})
}

func probeWindowsDNS() {
	fmt.Println("\n=== Windows: DNS Configuration ===")

	probe("dns", "system_dns_servers", func() (string, error) {
		out, err := exec.Command("netsh", "interface", "ip", "show", "dns").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("netsh dns: %w", err)
		}
		lines := strings.Split(string(out), "\n")
		var servers []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			// Look for IP addresses in the output
			if ip := net.ParseIP(line); ip != nil {
				servers = append(servers, ip.String())
			}
			// Also check for "DNS Servers:" lines
			if strings.Contains(line, "DNS") && strings.Contains(line, ":") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					if ip := net.ParseIP(strings.TrimSpace(parts[1])); ip != nil {
						servers = append(servers, ip.String())
					}
				}
			}
		}
		return fmt.Sprintf("DNS servers: %v", servers), nil
	})

	probe("dns", "ipv6_dns_servers", func() (string, error) {
		out, err := exec.Command("netsh", "interface", "ipv6", "show", "dns").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("netsh ipv6 dns: %w", err)
		}
		return "output: " + strings.TrimSpace(string(out)), nil
	})

	probe("dns", "powershell_dns_detection", func() (string, error) {
		cmd := `Get-DnsClientServerAddress | Where-Object {$_.AddressFamily -eq 23} | Select-Object -ExpandProperty ServerAddresses | Select-Object -First 5`
		out, err := exec.Command("powershell", "-Command", cmd).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("powershell: %w", err)
		}
		servers := strings.TrimSpace(string(out))
		if servers == "" {
			return "no IPv6 DNS servers configured", nil
		}
		return "IPv6 DNS: " + servers, nil
	})
}

func probeWindowsRegistry() {
	fmt.Println("\n=== Windows: Registry / System Proxy ===")

	probe("registry", "internet_settings_readable", func() (string, error) {
		out, err := exec.Command("reg", "query",
			`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
			"/v", "ProxyEnable").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("reg query: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return strings.TrimSpace(string(out)), nil
	})

	probe("registry", "interface_dns_settable", func() (string, error) {
		// Just check if we can query the current DNS (don't actually change it)
		out, err := exec.Command("netsh", "interface", "ip", "show", "dns").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("netsh: %w", err)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 {
			return lines[0], nil
		}
		return "readable", nil
	})
}

func probeWindowsUDP() {
	fmt.Println("\n=== Windows: UDP Proxy Capabilities ===")

	// Windows doesn't have recvmsg with ancillary data like Linux
	// Test what's available for UDP original destination recovery
	probe("udp_proxy", "wsa_recvmsg_available", func() (string, error) {
		// WSARecvMsg is the Windows equivalent of recvmsg
		// It requires loading the function pointer via WSAIoctl
		ws2, err := syscall.LoadDLL("ws2_32.dll")
		if err != nil {
			return "", fmt.Errorf("LoadDLL ws2_32: %w", err)
		}
		defer ws2.Release()

		// Check for WSARecvMsg via GUID
		_, err = ws2.FindProc("WSARecvMsg")
		if err != nil {
			// WSARecvMsg is not directly exported, must be obtained via WSAIoctl
			// This is expected — it's loaded via GUID at runtime
			return "WSARecvMsg not directly exported (normal, loaded via WSAIoctl GUID)", nil
		}
		return "WSARecvMsg directly available", nil
	})

	// Test IP_PKTINFO socket option (for recovering destination address)
	probe("udp_proxy", "ip_pktinfo_socket_option", func() (string, error) {
		conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			return "", fmt.Errorf("listen: %w", err)
		}
		defer conn.Close()

		raw, err := conn.(*net.UDPConn).SyscallConn()
		if err != nil {
			return "", fmt.Errorf("syscallconn: %w", err)
		}

		var setErr error
		raw.Control(func(fd uintptr) {
			// IP_PKTINFO = 19 on Windows
			const IP_PKTINFO = 19
			setErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, IP_PKTINFO, 1)
		})
		if setErr != nil {
			return "", fmt.Errorf("setsockopt IP_PKTINFO: %w", setErr)
		}
		return "IP_PKTINFO set on UDP4 socket", nil
	})

	// Test IPV6_PKTINFO socket option
	probe("udp_proxy", "ipv6_pktinfo_socket_option", func() (string, error) {
		conn, err := net.ListenPacket("udp6", "[::1]:0")
		if err != nil {
			return "", fmt.Errorf("listen: %w", err)
		}
		defer conn.Close()

		raw, err := conn.(*net.UDPConn).SyscallConn()
		if err != nil {
			return "", fmt.Errorf("syscallconn: %w", err)
		}

		var setErr error
		raw.Control(func(fd uintptr) {
			// IPV6_PKTINFO = 19 on Windows
			const IPV6_PKTINFO = 19
			setErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IPV6, IPV6_PKTINFO, 1)
		})
		if setErr != nil {
			return "", fmt.Errorf("setsockopt IPV6_PKTINFO: %w", setErr)
		}
		return "IPV6_PKTINFO set on UDP6 socket", nil
	})

	// Note: On Windows with WinDivert, UDP original destination is tracked
	// via the WinDivert conntrack map, not via socket options.
	// This is fundamentally different from Linux's recvmsg approach.
	probe("udp_proxy", "windivert_udp_conntrack_note", func() (string, error) {
		return "WinDivert UDP: original dst tracked via packet interception conntrack map (no socket option needed)", nil
	})
}
