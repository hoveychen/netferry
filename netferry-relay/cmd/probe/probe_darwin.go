//go:build darwin

package main

import (
	"encoding/binary"
	"fmt"
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
	probeDarwinPfTools()
	probeDarwinPfIPv6()
	probeDarwinPfPortRange()
	probeDarwinDIOCNATLOOK()
	probeDarwinDNS()
}

func probeDarwinPfTools() {
	fmt.Println("\n=== macOS: PF Tool Availability ===")

	probe("tools", "pfctl_binary", func() (string, error) {
		out, err := exec.Command("pfctl", "-s", "info").CombinedOutput()
		if err != nil {
			// pfctl often returns non-zero but still works
			if strings.Contains(string(out), "Status") {
				return "pfctl available (pf status accessible)", nil
			}
			return "", fmt.Errorf("pfctl: %s: %w", strings.TrimSpace(string(out)), err)
		}
		lines := strings.Split(string(out), "\n")
		if len(lines) > 0 {
			return strings.TrimSpace(lines[0]), nil
		}
		return "pfctl available", nil
	})

	probe("tools", "dev_pf_accessible", func() (string, error) {
		f, err := os.OpenFile("/dev/pf", os.O_RDWR, 0)
		if err != nil {
			return "", fmt.Errorf("cannot open /dev/pf: %w (need root?)", err)
		}
		f.Close()
		return "/dev/pf opened successfully", nil
	})
}

func probeDarwinPfIPv6() {
	fmt.Println("\n=== macOS: PF IPv6 Rules ===")

	// Test: pf can parse inet6 rdr rules
	probe("pf_ipv6", "pf_parse_inet6_rdr_rule", func() (string, error) {
		rule := `rdr pass on lo0 inet6 proto tcp from ! ::1 to fd00::/64 -> ::1 port 12345`
		cmd := exec.Command("pfctl", "-n", "-f", "-")
		cmd.Stdin = strings.NewReader(rule + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("pfctl parse: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return "inet6 rdr rule parsed ok", nil
	})

	// Test: pf can parse inet6 pass route-to rules
	probe("pf_ipv6", "pf_parse_inet6_pass_route_to", func() (string, error) {
		rule := `pass out route-to lo0 inet6 proto tcp to fd00::/64 keep state`
		cmd := exec.Command("pfctl", "-n", "-f", "-")
		cmd.Stdin = strings.NewReader(rule + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("pfctl parse: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return "inet6 pass route-to rule parsed ok", nil
	})

	// Test: pf can parse table with inet6 addresses
	probe("pf_ipv6", "pf_parse_inet6_table", func() (string, error) {
		rules := `table <dns6_servers> { ::1, fd00::53 }
rdr pass on lo0 inet6 proto udp to <dns6_servers> port 53 -> ::1 port 15353
pass out route-to lo0 inet6 proto udp to <dns6_servers> port 53 keep state`
		cmd := exec.Command("pfctl", "-n", "-f", "-")
		cmd.Stdin = strings.NewReader(rules + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("pfctl parse: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return "inet6 table + DNS rules parsed ok", nil
	})

	// Test: pf can load inet6 rules into an anchor (dry-run on anchor)
	probe("pf_ipv6", "pf_parse_anchor_inet6", func() (string, error) {
		rules := `rdr pass on lo0 inet6 proto tcp from ! ::1 to fd00::/64 -> ::1 port 12345
pass out route-to lo0 inet6 proto tcp to fd00::/64 keep state`
		cmd := exec.Command("pfctl", "-n", "-a", "netferry-probe-v6", "-f", "-")
		cmd.Stdin = strings.NewReader(rules + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("pfctl anchor parse: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return "inet6 anchor rules parsed ok", nil
	})

	// Test: mixed inet + inet6 in same anchor
	probe("pf_ipv6", "pf_parse_mixed_inet_inet6", func() (string, error) {
		rules := `rdr pass on lo0 inet proto tcp from ! 127.0.0.1 to 10.0.0.0/8 -> 127.0.0.1 port 12345
rdr pass on lo0 inet6 proto tcp from ! ::1 to fd00::/64 -> ::1 port 12345
pass out route-to lo0 inet proto tcp to 10.0.0.0/8 keep state
pass out route-to lo0 inet6 proto tcp to fd00::/64 keep state`
		cmd := exec.Command("pfctl", "-n", "-a", "netferry-probe-mixed", "-f", "-")
		cmd.Stdin = strings.NewReader(rules + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("pfctl mixed parse: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return "mixed inet + inet6 in one anchor parsed ok", nil
	})
}

func probeDarwinPfPortRange() {
	fmt.Println("\n=== macOS: PF Port Range Filtering ===")

	probe("pf_portrange", "pf_parse_port_range_rdr", func() (string, error) {
		rule := `rdr pass on lo0 inet proto tcp from ! 127.0.0.1 to 10.0.0.0/8 port 80:443 -> 127.0.0.1 port 12345`
		cmd := exec.Command("pfctl", "-n", "-f", "-")
		cmd.Stdin = strings.NewReader(rule + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("pfctl parse: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return "port range 80:443 in rdr parsed ok", nil
	})

	probe("pf_portrange", "pf_parse_port_list_rdr", func() (string, error) {
		rule := `rdr pass on lo0 inet proto tcp from ! 127.0.0.1 to 10.0.0.0/8 port { 80, 443, 8080 } -> 127.0.0.1 port 12345`
		cmd := exec.Command("pfctl", "-n", "-f", "-")
		cmd.Stdin = strings.NewReader(rule + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("pfctl parse: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return "port list {80,443,8080} in rdr parsed ok", nil
	})

	probe("pf_portrange", "pf_parse_port_range_pass", func() (string, error) {
		rule := `pass out route-to lo0 inet proto tcp to 10.0.0.0/8 port 80:443 keep state`
		cmd := exec.Command("pfctl", "-n", "-f", "-")
		cmd.Stdin = strings.NewReader(rule + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("pfctl parse: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return "port range in pass route-to parsed ok", nil
	})

	probe("pf_portrange", "pf_parse_port_range_inet6", func() (string, error) {
		rule := `rdr pass on lo0 inet6 proto tcp from ! ::1 to fd00::/64 port 80:443 -> ::1 port 12345`
		cmd := exec.Command("pfctl", "-n", "-f", "-")
		cmd.Stdin = strings.NewReader(rule + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("pfctl parse: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return "inet6 port range in rdr parsed ok", nil
	})
}

func probeDarwinDIOCNATLOOK() {
	fmt.Println("\n=== macOS: DIOCNATLOOK IPv6 Struct Layout ===")

	// DIOCNATLOOK ioctl number for Darwin
	// _IOWR('D', 23, struct pfioc_natlook) = 0xC0544417
	const DIOCNATLOOK = 0xC0544417

	// Test struct sizes and alignment for IPv6 DIOCNATLOOK
	probe("pf_ipv6", "diocnatlook_struct_size", func() (string, error) {
		// pfioc_natlook on Darwin is 84 bytes:
		//   pf_addr saddr (16 bytes)
		//   pf_addr daddr (16 bytes)
		//   pf_addr rsaddr (16 bytes)
		//   pf_addr rdaddr (16 bytes)
		//   union pf_state_xport {
		//     u_int16_t port[2] sxport (4 bytes)
		//     u_int16_t port[2] dxport (4 bytes)
		//     u_int16_t port[2] rsxport (4 bytes)
		//     u_int16_t port[2] rdxport (4 bytes)
		//   }
		//   sa_family_t af (1 byte)
		//   u_int8_t proto (1 byte)
		//   u_int8_t direction (1 byte)
		//   padding (1 byte)
		// Total: 84 bytes
		type pfAddr [16]byte // Can hold IPv4 (in first 4 bytes) or full IPv6

		type pfiocNatlook struct {
			Saddr   pfAddr
			Daddr   pfAddr
			Rsaddr  pfAddr
			Rdaddr  pfAddr
			Sxport  [4]byte
			Dxport  [4]byte
			Rsxport [4]byte
			Rdxport [4]byte
			Af      uint8
			Proto   uint8
			Dir     uint8
			Pad     uint8
		}

		size := unsafe.Sizeof(pfiocNatlook{})
		if size != 84 {
			return "", fmt.Errorf("pfioc_natlook size=%d, expected 84", size)
		}
		return fmt.Sprintf("pfioc_natlook size=%d (correct)", size), nil
	})

	// Test that we can issue DIOCNATLOOK ioctl (will fail without matching state, but syscall should work)
	probe("pf_ipv6", "diocnatlook_ioctl_callable", func() (string, error) {
		f, err := os.OpenFile("/dev/pf", os.O_RDWR, 0)
		if err != nil {
			return "", fmt.Errorf("cannot open /dev/pf: %w (need root)", err)
		}
		defer f.Close()

		// Build a minimal pfioc_natlook for IPv4 (AF_INET=2)
		buf := make([]byte, 84)
		// Set saddr = 127.0.0.1
		buf[0] = 127
		buf[3] = 1
		// Set daddr = 127.0.0.1
		buf[16] = 127
		buf[19] = 1
		// Set sport = 12345 (network byte order)
		binary.BigEndian.PutUint16(buf[64:66], 12345)
		// Set dport = 80 (network byte order)
		binary.BigEndian.PutUint16(buf[68:70], 80)
		// af = AF_INET (2)
		buf[80] = 2
		// proto = TCP (6)
		buf[81] = 6
		// direction = PF_OUT (2)
		buf[82] = 2

		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(DIOCNATLOOK), uintptr(unsafe.Pointer(&buf[0])))
		// We expect ENOENT (no matching state) — that's fine, it means the ioctl works
		if errno != 0 && errno != syscall.ENOENT {
			return "", fmt.Errorf("ioctl DIOCNATLOOK errno=%d (%s)", errno, errno.Error())
		}
		return fmt.Sprintf("DIOCNATLOOK ioctl callable (errno=%d, expected ENOENT)", errno), nil
	})

	// Test DIOCNATLOOK with AF_INET6
	probe("pf_ipv6", "diocnatlook_inet6_callable", func() (string, error) {
		f, err := os.OpenFile("/dev/pf", os.O_RDWR, 0)
		if err != nil {
			return "", fmt.Errorf("cannot open /dev/pf: %w (need root)", err)
		}
		defer f.Close()

		buf := make([]byte, 84)
		// Set saddr = ::1 (last byte = 1)
		buf[15] = 1
		// Set daddr = ::1
		buf[31] = 1
		// Set sport = 12345 (network byte order)
		binary.BigEndian.PutUint16(buf[64:66], 12345)
		// Set dport = 80
		binary.BigEndian.PutUint16(buf[68:70], 80)
		// af = AF_INET6 (30 on Darwin)
		buf[80] = 30
		// proto = TCP (6)
		buf[81] = 6
		// direction = PF_OUT (2)
		buf[82] = 2

		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(DIOCNATLOOK), uintptr(unsafe.Pointer(&buf[0])))
		if errno != 0 && errno != syscall.ENOENT {
			return "", fmt.Errorf("ioctl DIOCNATLOOK AF_INET6 errno=%d (%s)", errno, errno.Error())
		}
		return fmt.Sprintf("DIOCNATLOOK AF_INET6 callable (errno=%d)", errno), nil
	})
}

func probeDarwinDNS() {
	fmt.Println("\n=== macOS: DNS Detection ===")

	probe("dns", "scutil_dns_available", func() (string, error) {
		out, err := exec.Command("scutil", "--dns").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("scutil --dns: %w", err)
		}
		lines := strings.Split(string(out), "\n")
		var v4Servers, v6Servers []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "nameserver[") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					ip := strings.TrimSpace(parts[1])
					if strings.Contains(ip, ":") {
						v6Servers = append(v6Servers, ip)
					} else {
						v4Servers = append(v4Servers, ip)
					}
				}
			}
		}
		return fmt.Sprintf("v4=%v v6=%v", v4Servers, v6Servers), nil
	})

	probe("dns", "resolv_conf_nameservers", func() (string, error) {
		data, err := os.ReadFile("/etc/resolv.conf")
		if err != nil {
			return "", fmt.Errorf("read /etc/resolv.conf: %w", err)
		}
		var servers []string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "nameserver") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					servers = append(servers, parts[1])
				}
			}
		}
		return fmt.Sprintf("nameservers=%v", servers), nil
	})
}
