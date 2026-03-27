//go:build linux

package firewall

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
)

// tproxyMethod implements firewall.Method using TPROXY (nftables or iptables).
// Unlike NAT-based REDIRECT, TPROXY preserves the original destination address
// in the socket so the proxy can read it directly from conn.LocalAddr().
type tproxyMethod struct {
	useNft bool
	cfg    TProxyConfig
}

func (t *tproxyMethod) Name() string { return "tproxy" }

func (t *tproxyMethod) SetConfig(cfg TProxyConfig) { t.cfg = cfg }

func (t *tproxyMethod) SupportedFeatures() []Feature {
	return []Feature{FeatureDNS, FeatureUDP, FeaturePortRange, FeatureIPv6}
}

func (t *tproxyMethod) Setup(subnets []SubnetRule, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	_, v6Subnets := SplitByFamily(subnets)

	mark := strconv.Itoa(t.cfg.FWMark)
	table := strconv.Itoa(t.cfg.RouteTable)

	// IPv4 policy routing: packets marked with fwmark are routed to loopback,
	// which delivers them to the TPROXY listener.
	if err := run("ip", "rule", "add", "fwmark", mark, "lookup", table); err != nil {
		return fmt.Errorf("ip rule add: %w", err)
	}
	if err := run("ip", "route", "add", "local", "0.0.0.0/0", "dev", "lo", "table", table); err != nil {
		run("ip", "rule", "del", "fwmark", mark, "lookup", table)
		return fmt.Errorf("ip route add: %w", err)
	}

	// IPv6 policy routing (only if there are IPv6 subnets).
	if len(v6Subnets) > 0 {
		if err := run("ip", "-6", "rule", "add", "fwmark", mark, "lookup", table); err != nil {
			return fmt.Errorf("ip -6 rule add: %w", err)
		}
		if err := run("ip", "-6", "route", "add", "local", "::/0", "dev", "lo", "table", table); err != nil {
			run("ip", "-6", "rule", "del", "fwmark", mark, "lookup", table)
			return fmt.Errorf("ip -6 route add: %w", err)
		}
	}

	var setupErr error
	if t.useNft {
		setupErr = t.setupNft(subnets, excludes, proxyPort, dnsPort, dnsServers)
	} else {
		setupErr = t.setupIpt(subnets, excludes, proxyPort, dnsPort, dnsServers)
	}
	if setupErr != nil {
		return setupErr
	}

	// Flush conntrack entries for intercepted subnets. Without this, packets
	// from existing connections bypass TPROXY and are not intercepted.
	flushConntrack(subnets)
	return nil
}

func (t *tproxyMethod) setupNft(subnets []SubnetRule, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	// Remove any leftover table first.
	exec.Command("nft", "delete", "table", "inet", "netferry").Run()
	exec.Command("nft", "delete", "table", "ip", "netferry").Run()

	v4Subnets, v6Subnets := SplitByFamily(subnets)
	v4Excludes, v6Excludes := SplitExcludesByFamily(excludes)
	v4DNS, v6DNS := SplitDNSByFamily(dnsServers)

	markHex := fmt.Sprintf("0x%x", t.cfg.FWMark)

	var b bytes.Buffer
	fmt.Fprintf(&b, "table inet netferry {\n")

	// DNS server sets.
	if dnsPort > 0 && len(v4DNS) > 0 {
		fmt.Fprintf(&b, "  set dns_servers {\n    type ipv4_addr\n    elements = {%s}\n  }\n",
			joinQuoted(v4DNS))
	}
	if dnsPort > 0 && len(v6DNS) > 0 {
		fmt.Fprintf(&b, "  set dns6_servers {\n    type ipv6_addr\n    elements = {%s}\n  }\n",
			joinQuoted(v6DNS))
	}

	// --- prerouting chain: TPROXY intercepts packets arriving via policy routing ---
	fmt.Fprintf(&b, "  chain prerouting {\n    type filter hook prerouting priority mangle;\n")
	// Excludes.
	for _, excl := range v4Excludes {
		fmt.Fprintf(&b, "    ip daddr %s return\n", excl)
	}
	for _, excl := range v6Excludes {
		fmt.Fprintf(&b, "    ip6 daddr %s return\n", excl)
	}
	// IPv4 subnets.
	for _, subnet := range v4Subnets {
		portExpr := subnet.NftPortExpr()
		if portExpr != "" {
			fmt.Fprintf(&b, "    ip daddr %s %s tproxy to 127.0.0.1:%d meta mark set %s accept\n",
				subnet.CIDR, portExpr, proxyPort, markHex)
		} else {
			fmt.Fprintf(&b, "    ip daddr %s tcp dport != %d tproxy to 127.0.0.1:%d meta mark set %s accept\n",
				subnet.CIDR, proxyPort, proxyPort, markHex)
		}
	}
	// IPv6 subnets.
	for _, subnet := range v6Subnets {
		portExpr := subnet.NftPortExpr()
		if portExpr != "" {
			fmt.Fprintf(&b, "    ip6 daddr %s %s tproxy to [::1]:%d meta mark set %s accept\n",
				subnet.CIDR, portExpr, proxyPort, markHex)
		} else {
			fmt.Fprintf(&b, "    ip6 daddr %s tcp dport != %d tproxy to [::1]:%d meta mark set %s accept\n",
				subnet.CIDR, proxyPort, proxyPort, markHex)
		}
	}
	// DNS TPROXY.
	if dnsPort > 0 && len(v4DNS) > 0 {
		fmt.Fprintf(&b, "    ip daddr @dns_servers udp dport 53 tproxy to 127.0.0.1:%d meta mark set %s accept\n", dnsPort, markHex)
	}
	if dnsPort > 0 && len(v6DNS) > 0 {
		fmt.Fprintf(&b, "    ip6 daddr @dns6_servers udp dport 53 tproxy to [::1]:%d meta mark set %s accept\n", dnsPort, markHex)
	}
	fmt.Fprintf(&b, "  }\n")

	// --- output chain: marks locally-generated packets so policy routing sends
	// them to loopback, where prerouting TPROXY catches them ---
	fmt.Fprintf(&b, "  chain output {\n    type route hook output priority mangle;\n")
	// Local traffic protection first.
	fmt.Fprintf(&b, "    fib daddr type local return\n")
	// Excludes.
	for _, excl := range v4Excludes {
		fmt.Fprintf(&b, "    ip daddr %s return\n", excl)
	}
	for _, excl := range v6Excludes {
		fmt.Fprintf(&b, "    ip6 daddr %s return\n", excl)
	}
	// IPv4 subnets.
	for _, subnet := range v4Subnets {
		portExpr := subnet.NftPortExpr()
		if portExpr != "" {
			fmt.Fprintf(&b, "    ip daddr %s %s meta mark set %s\n",
				subnet.CIDR, portExpr, markHex)
		} else {
			fmt.Fprintf(&b, "    ip daddr %s tcp dport != %d meta mark set %s\n",
				subnet.CIDR, proxyPort, markHex)
		}
	}
	// IPv6 subnets.
	for _, subnet := range v6Subnets {
		portExpr := subnet.NftPortExpr()
		if portExpr != "" {
			fmt.Fprintf(&b, "    ip6 daddr %s %s meta mark set %s\n",
				subnet.CIDR, portExpr, markHex)
		} else {
			fmt.Fprintf(&b, "    ip6 daddr %s tcp dport != %d meta mark set %s\n",
				subnet.CIDR, proxyPort, markHex)
		}
	}
	// DNS marks.
	if dnsPort > 0 && len(v4DNS) > 0 {
		fmt.Fprintf(&b, "    ip daddr @dns_servers udp dport 53 meta mark set %s\n", markHex)
	}
	if dnsPort > 0 && len(v6DNS) > 0 {
		fmt.Fprintf(&b, "    ip6 daddr @dns6_servers udp dport 53 meta mark set %s\n", markHex)
	}
	fmt.Fprintf(&b, "  }\n}\n")

	cmd := exec.Command("nft", "-f", "/dev/stdin")
	cmd.Stdin = &b
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft: %w\n%s", err, out)
	}
	return nil
}

func (t *tproxyMethod) setupIpt(subnets []SubnetRule, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	v4Subnets, v6Subnets := SplitByFamily(subnets)
	v4Excludes, v6Excludes := SplitExcludesByFamily(excludes)
	v4DNS, v6DNS := SplitDNSByFamily(dnsServers)

	markStr := strconv.Itoa(t.cfg.FWMark)

	ipt := func(args ...string) error {
		cmd := exec.Command("iptables", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("iptables %v: %w\n%s", args, err, out)
		}
		return nil
	}

	ip6t := func(args ...string) error {
		cmd := exec.Command("ip6tables", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("ip6tables %v: %w\n%s", args, err, out)
		}
		return nil
	}

	// --- IPv4 ---
	// Prerouting chain: TPROXY intercept.
	ipt("-t", "mangle", "-N", "NETFERRY")
	ipt("-t", "mangle", "-A", "PREROUTING", "-j", "NETFERRY")

	for _, excl := range v4Excludes {
		ipt("-t", "mangle", "-A", "NETFERRY", "-d", excl, "-j", "RETURN")
	}
	for _, subnet := range v4Subnets {
		args := []string{"-t", "mangle", "-A", "NETFERRY", "-d", subnet.CIDR, "-p", "tcp", "-m", "tcp"}
		portArgs := subnet.IptPortArgs()
		if portArgs != nil {
			args = append(args, portArgs...)
		} else {
			args = append(args, "!", "--dport", strconv.Itoa(proxyPort))
		}
		args = append(args, "-j", "TPROXY", "--on-port", strconv.Itoa(proxyPort),
			"--tproxy-mark", markStr)
		if err := ipt(args...); err != nil {
			return err
		}
	}
	if dnsPort > 0 {
		for _, ns := range v4DNS {
			ipt("-t", "mangle", "-A", "NETFERRY", "-d", ns,
				"-p", "udp", "-m", "udp", "--dport", "53",
				"-j", "TPROXY", "--on-port", strconv.Itoa(dnsPort),
				"--tproxy-mark", markStr)
		}
	}

	// Output chain: mark locally-generated packets for policy routing.
	ipt("-t", "mangle", "-N", "NETFERRY_OUTPUT")
	ipt("-t", "mangle", "-A", "OUTPUT", "-j", "NETFERRY_OUTPUT")

	// Local traffic protection.
	ipt("-t", "mangle", "-A", "NETFERRY_OUTPUT", "-m", "addrtype", "--dst-type", "LOCAL", "-j", "RETURN")

	for _, excl := range v4Excludes {
		ipt("-t", "mangle", "-A", "NETFERRY_OUTPUT", "-d", excl, "-j", "RETURN")
	}
	for _, subnet := range v4Subnets {
		args := []string{"-t", "mangle", "-A", "NETFERRY_OUTPUT", "-d", subnet.CIDR, "-p", "tcp", "-m", "tcp"}
		portArgs := subnet.IptPortArgs()
		if portArgs != nil {
			args = append(args, portArgs...)
		} else {
			args = append(args, "!", "--dport", strconv.Itoa(proxyPort))
		}
		args = append(args, "-j", "MARK", "--set-mark", markStr)
		if err := ipt(args...); err != nil {
			return err
		}
	}
	if dnsPort > 0 {
		for _, ns := range v4DNS {
			ipt("-t", "mangle", "-A", "NETFERRY_OUTPUT", "-d", ns,
				"-p", "udp", "-m", "udp", "--dport", "53",
				"-j", "MARK", "--set-mark", markStr)
		}
	}

	// --- IPv6 ---
	if len(v6Subnets) > 0 || (dnsPort > 0 && len(v6DNS) > 0) {
		// Prerouting chain: TPROXY intercept.
		ip6t("-t", "mangle", "-N", "NETFERRY")
		ip6t("-t", "mangle", "-A", "PREROUTING", "-j", "NETFERRY")

		for _, excl := range v6Excludes {
			ip6t("-t", "mangle", "-A", "NETFERRY", "-d", excl, "-j", "RETURN")
		}
		for _, subnet := range v6Subnets {
			args := []string{"-t", "mangle", "-A", "NETFERRY", "-d", subnet.CIDR, "-p", "tcp", "-m", "tcp"}
			portArgs := subnet.IptPortArgs()
			if portArgs != nil {
				args = append(args, portArgs...)
			} else {
				args = append(args, "!", "--dport", strconv.Itoa(proxyPort))
			}
			args = append(args, "-j", "TPROXY", "--on-port", strconv.Itoa(proxyPort),
				"--tproxy-mark", markStr)
			if err := ip6t(args...); err != nil {
				return err
			}
		}
		if dnsPort > 0 {
			for _, ns := range v6DNS {
				ip6t("-t", "mangle", "-A", "NETFERRY", "-d", ns,
					"-p", "udp", "-m", "udp", "--dport", "53",
					"-j", "TPROXY", "--on-port", strconv.Itoa(dnsPort),
					"--tproxy-mark", markStr)
			}
		}

		// Output chain: mark locally-generated packets.
		ip6t("-t", "mangle", "-N", "NETFERRY_OUTPUT")
		ip6t("-t", "mangle", "-A", "OUTPUT", "-j", "NETFERRY_OUTPUT")

		// Local traffic protection.
		ip6t("-t", "mangle", "-A", "NETFERRY_OUTPUT", "-m", "addrtype", "--dst-type", "LOCAL", "-j", "RETURN")

		for _, excl := range v6Excludes {
			ip6t("-t", "mangle", "-A", "NETFERRY_OUTPUT", "-d", excl, "-j", "RETURN")
		}
		for _, subnet := range v6Subnets {
			args := []string{"-t", "mangle", "-A", "NETFERRY_OUTPUT", "-d", subnet.CIDR, "-p", "tcp", "-m", "tcp"}
			portArgs := subnet.IptPortArgs()
			if portArgs != nil {
				args = append(args, portArgs...)
			} else {
				args = append(args, "!", "--dport", strconv.Itoa(proxyPort))
			}
			args = append(args, "-j", "MARK", "--set-mark", markStr)
			if err := ip6t(args...); err != nil {
				return err
			}
		}
		if dnsPort > 0 {
			for _, ns := range v6DNS {
				ip6t("-t", "mangle", "-A", "NETFERRY_OUTPUT", "-d", ns,
					"-p", "udp", "-m", "udp", "--dport", "53",
					"-j", "MARK", "--set-mark", markStr)
			}
		}
	}

	return nil
}

func (t *tproxyMethod) Restore() error {
	mark := strconv.Itoa(t.cfg.FWMark)
	table := strconv.Itoa(t.cfg.RouteTable)

	// Remove IPv4 policy routing rules.
	run("ip", "rule", "del", "fwmark", mark, "lookup", table)
	run("ip", "route", "del", "local", "0.0.0.0/0", "dev", "lo", "table", table)

	// Remove IPv6 policy routing rules.
	run("ip", "-6", "rule", "del", "fwmark", mark, "lookup", table)
	run("ip", "-6", "route", "del", "local", "::/0", "dev", "lo", "table", table)

	if t.useNft {
		exec.Command("nft", "delete", "table", "inet", "netferry").Run()
	} else {
		// IPv4 mangle cleanup.
		exec.Command("iptables", "-t", "mangle", "-D", "OUTPUT", "-j", "NETFERRY_OUTPUT").Run()
		exec.Command("iptables", "-t", "mangle", "-F", "NETFERRY_OUTPUT").Run()
		exec.Command("iptables", "-t", "mangle", "-X", "NETFERRY_OUTPUT").Run()
		exec.Command("iptables", "-t", "mangle", "-D", "PREROUTING", "-j", "NETFERRY").Run()
		exec.Command("iptables", "-t", "mangle", "-F", "NETFERRY").Run()
		exec.Command("iptables", "-t", "mangle", "-X", "NETFERRY").Run()

		// IPv6 mangle cleanup.
		exec.Command("ip6tables", "-t", "mangle", "-D", "OUTPUT", "-j", "NETFERRY_OUTPUT").Run()
		exec.Command("ip6tables", "-t", "mangle", "-F", "NETFERRY_OUTPUT").Run()
		exec.Command("ip6tables", "-t", "mangle", "-X", "NETFERRY_OUTPUT").Run()
		exec.Command("ip6tables", "-t", "mangle", "-D", "PREROUTING", "-j", "NETFERRY").Run()
		exec.Command("ip6tables", "-t", "mangle", "-F", "NETFERRY").Run()
		exec.Command("ip6tables", "-t", "mangle", "-X", "NETFERRY").Run()
	}
	return nil
}

// run executes a command and returns an error if it fails.
func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w\n%s", name, args, err, out)
	}
	return nil
}
