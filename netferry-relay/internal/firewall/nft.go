//go:build linux

package firewall

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

func newDefault() Method {
	if _, err := exec.LookPath("nft"); err == nil {
		return &nftMethod{}
	}
	return &iptMethod{}
}

func newNamed(name string) (Method, error) {
	switch name {
	case "nft", "auto":
		return &nftMethod{}, nil
	case "nat", "ipt":
		return &iptMethod{}, nil
	case "tproxy":
		_, nftErr := exec.LookPath("nft")
		return &tproxyMethod{useNft: nftErr == nil, cfg: DefaultTProxyConfig()}, nil
	}
	return nil, fmt.Errorf("firewall method %q not supported on Linux", name)
}

func listMethodFeatures() map[string][]Feature {
	m := map[string][]Feature{
		"nft":    (&nftMethod{}).SupportedFeatures(),
		"ipt":    (&iptMethod{}).SupportedFeatures(),
		"tproxy": (&tproxyMethod{}).SupportedFeatures(),
	}
	return m
}

// SO_ORIGINAL_DST retrieves the original destination of a redirected TCP
// connection from the kernel's conntrack table (requires iptables/nftables NAT).
const SO_ORIGINAL_DST = 80

// IP6T_SO_ORIGINAL_DST is the IPv6 equivalent of SO_ORIGINAL_DST.
const IP6T_SO_ORIGINAL_DST = 80

// QueryOrigDst returns the original destination IP and port of a conn that was
// redirected by iptables/nftables REDIRECT. Supports both IPv4 and IPv6.
func QueryOrigDst(sock net.Conn) (dstIP string, dstPort int, err error) {
	tc, ok := sock.(*net.TCPConn)
	if !ok {
		return "", 0, fmt.Errorf("not a TCP conn")
	}
	rawConn, err := tc.SyscallConn()
	if err != nil {
		return "", 0, err
	}

	// Detect address family from the remote address.
	remoteAddr := sock.RemoteAddr().(*net.TCPAddr)
	isIPv6 := remoteAddr.IP.To4() == nil

	if isIPv6 {
		var origDst syscall.RawSockaddrInet6
		var callErr error
		rawConn.Control(func(fd uintptr) {
			size := uint32(unsafe.Sizeof(origDst))
			_, _, e := syscall.Syscall6(
				syscall.SYS_GETSOCKOPT,
				fd,
				syscall.SOL_IPV6,
				IP6T_SO_ORIGINAL_DST,
				uintptr(unsafe.Pointer(&origDst)),
				uintptr(unsafe.Pointer(&size)),
				0,
			)
			if e != 0 {
				callErr = e
			}
		})
		if callErr != nil {
			la := sock.LocalAddr().(*net.TCPAddr)
			return la.IP.String(), la.Port, nil
		}
		ip := net.IP(origDst.Addr[:]).String()
		port := int(origDst.Port>>8 | origDst.Port<<8) // ntohs
		return ip, port, nil
	}

	// IPv4 path.
	var origDst syscall.RawSockaddrInet4
	var callErr error
	rawConn.Control(func(fd uintptr) {
		size := uint32(unsafe.Sizeof(origDst))
		_, _, e := syscall.Syscall6(
			syscall.SYS_GETSOCKOPT,
			fd,
			syscall.IPPROTO_IP,
			SO_ORIGINAL_DST,
			uintptr(unsafe.Pointer(&origDst)),
			uintptr(unsafe.Pointer(&size)),
			0,
		)
		if e != 0 {
			callErr = e
		}
	})
	if callErr != nil {
		la := sock.LocalAddr().(*net.TCPAddr)
		return la.IP.String(), la.Port, nil
	}

	ip := net.IP(origDst.Addr[:]).String()
	port := int(origDst.Port>>8 | origDst.Port<<8) // ntohs
	return ip, port, nil
}

// CleanStaleAnchors removes any leftover firewall rules from a previous
// crashed run. Handles both nftables and iptables (IPv4 and IPv6).
func CleanStaleAnchors() {
	// nftables cleanup (inet covers both v4 and v6).
	exec.Command("nft", "delete", "table", "inet", "netferry").Run()
	// Also clean legacy ip-only table in case of upgrade.
	exec.Command("nft", "delete", "table", "ip", "netferry").Run()

	// iptables NAT cleanup (used by iptMethod).
	exec.Command("iptables", "-t", "nat", "-D", "OUTPUT", "-j", "NETFERRY").Run()
	exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-j", "NETFERRY").Run()
	exec.Command("iptables", "-t", "nat", "-F", "NETFERRY").Run()
	exec.Command("iptables", "-t", "nat", "-X", "NETFERRY").Run()

	// ip6tables NAT cleanup (used by iptMethod IPv6).
	exec.Command("ip6tables", "-t", "nat", "-D", "OUTPUT", "-j", "NETFERRY6").Run()
	exec.Command("ip6tables", "-t", "nat", "-D", "PREROUTING", "-j", "NETFERRY6").Run()
	exec.Command("ip6tables", "-t", "nat", "-F", "NETFERRY6").Run()
	exec.Command("ip6tables", "-t", "nat", "-X", "NETFERRY6").Run()

	// iptables mangle cleanup (used by tproxyMethod with iptables).
	exec.Command("iptables", "-t", "mangle", "-D", "OUTPUT", "-j", "NETFERRY_OUTPUT").Run()
	exec.Command("iptables", "-t", "mangle", "-F", "NETFERRY_OUTPUT").Run()
	exec.Command("iptables", "-t", "mangle", "-X", "NETFERRY_OUTPUT").Run()
	exec.Command("iptables", "-t", "mangle", "-D", "PREROUTING", "-j", "NETFERRY").Run()
	exec.Command("iptables", "-t", "mangle", "-F", "NETFERRY").Run()
	exec.Command("iptables", "-t", "mangle", "-X", "NETFERRY").Run()

	// ip6tables mangle cleanup (used by tproxyMethod with ip6tables).
	exec.Command("ip6tables", "-t", "mangle", "-D", "OUTPUT", "-j", "NETFERRY_OUTPUT").Run()
	exec.Command("ip6tables", "-t", "mangle", "-F", "NETFERRY_OUTPUT").Run()
	exec.Command("ip6tables", "-t", "mangle", "-X", "NETFERRY_OUTPUT").Run()
	exec.Command("ip6tables", "-t", "mangle", "-D", "PREROUTING", "-j", "NETFERRY").Run()
	exec.Command("ip6tables", "-t", "mangle", "-F", "NETFERRY").Run()
	exec.Command("ip6tables", "-t", "mangle", "-X", "NETFERRY").Run()

	// TPROXY policy routing cleanup (IPv4 and IPv6).
	exec.Command("ip", "rule", "del", "fwmark", "1", "lookup", "100").Run()
	exec.Command("ip", "route", "del", "local", "0.0.0.0/0", "dev", "lo", "table", "100").Run()
	exec.Command("ip", "-6", "rule", "del", "fwmark", "1", "lookup", "100").Run()
	exec.Command("ip", "-6", "route", "del", "local", "::/0", "dev", "lo", "table", "100").Run()
}

// --- nftMethod ---

// nftMethod implements firewall.Method using nftables.
type nftMethod struct{}

func (n *nftMethod) Name() string { return "nft" }

func (n *nftMethod) SupportedFeatures() []Feature {
	return []Feature{FeatureDNS, FeaturePortRange, FeatureIPv6}
}

func (n *nftMethod) Setup(subnets []SubnetRule, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	// Remove any leftover table first.
	exec.Command("nft", "delete", "table", "inet", "netferry").Run()
	exec.Command("nft", "delete", "table", "ip", "netferry").Run()

	v4Subnets, v6Subnets := SplitByFamily(subnets)
	v4Excludes, v6Excludes := SplitExcludesByFamily(excludes)
	v4DNS, v6DNS := SplitDNSByFamily(dnsServers)

	var b bytes.Buffer
	fmt.Fprintf(&b, "table inet netferry {\n")

	// DNS sets.
	if dnsPort > 0 && len(v4DNS) > 0 {
		fmt.Fprintf(&b, "  set dns_servers {\n    type ipv4_addr\n    elements = {%s}\n  }\n",
			joinQuoted(v4DNS))
	}
	if dnsPort > 0 && len(v6DNS) > 0 {
		fmt.Fprintf(&b, "  set dns6_servers {\n    type ipv6_addr\n    elements = {%s}\n  }\n",
			joinQuoted(v6DNS))
	}

	// --- prerouting chain ---
	fmt.Fprintf(&b, "  chain prerouting {\n    type nat hook prerouting priority -100;\n")
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
			fmt.Fprintf(&b, "    ip daddr %s %s redirect to :%d\n",
				subnet.CIDR, portExpr, proxyPort)
		} else {
			fmt.Fprintf(&b, "    ip daddr %s tcp dport != %d redirect to :%d\n",
				subnet.CIDR, proxyPort, proxyPort)
		}
	}
	// IPv6 subnets.
	for _, subnet := range v6Subnets {
		portExpr := subnet.NftPortExpr()
		if portExpr != "" {
			fmt.Fprintf(&b, "    ip6 daddr %s %s redirect to :%d\n",
				subnet.CIDR, portExpr, proxyPort)
		} else {
			fmt.Fprintf(&b, "    ip6 daddr %s tcp dport != %d redirect to :%d\n",
				subnet.CIDR, proxyPort, proxyPort)
		}
	}
	// DNS redirects.
	if dnsPort > 0 && len(v4DNS) > 0 {
		fmt.Fprintf(&b, "    ip daddr @dns_servers udp dport 53 redirect to :%d\n", dnsPort)
	}
	if dnsPort > 0 && len(v6DNS) > 0 {
		fmt.Fprintf(&b, "    ip6 daddr @dns6_servers udp dport 53 redirect to :%d\n", dnsPort)
	}
	fmt.Fprintf(&b, "  }\n")

	// --- output chain ---
	fmt.Fprintf(&b, "  chain output {\n    type nat hook output priority -100;\n")
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
			fmt.Fprintf(&b, "    ip daddr %s %s redirect to :%d\n",
				subnet.CIDR, portExpr, proxyPort)
		} else {
			fmt.Fprintf(&b, "    ip daddr %s tcp dport != %d redirect to :%d\n",
				subnet.CIDR, proxyPort, proxyPort)
		}
	}
	// IPv6 subnets.
	for _, subnet := range v6Subnets {
		portExpr := subnet.NftPortExpr()
		if portExpr != "" {
			fmt.Fprintf(&b, "    ip6 daddr %s %s redirect to :%d\n",
				subnet.CIDR, portExpr, proxyPort)
		} else {
			fmt.Fprintf(&b, "    ip6 daddr %s tcp dport != %d redirect to :%d\n",
				subnet.CIDR, proxyPort, proxyPort)
		}
	}
	// DNS redirects.
	if dnsPort > 0 && len(v4DNS) > 0 {
		fmt.Fprintf(&b, "    ip daddr @dns_servers udp dport 53 redirect to :%d\n", dnsPort)
	}
	if dnsPort > 0 && len(v6DNS) > 0 {
		fmt.Fprintf(&b, "    ip6 daddr @dns6_servers udp dport 53 redirect to :%d\n", dnsPort)
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

func (n *nftMethod) Restore() error {
	exec.Command("nft", "delete", "table", "inet", "netferry").Run()
	return nil
}

func joinQuoted(ss []string) string {
	var b bytes.Buffer
	for i, s := range ss {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(s)
	}
	return b.String()
}

// --- iptMethod ---

// iptMethod implements firewall.Method using iptables.
type iptMethod struct{}

func (p *iptMethod) Name() string { return "iptables" }

func (p *iptMethod) SupportedFeatures() []Feature {
	return []Feature{FeatureDNS, FeaturePortRange, FeatureIPv6}
}

func (p *iptMethod) Setup(subnets []SubnetRule, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	v4Subnets, v6Subnets := SplitByFamily(subnets)
	v4Excludes, v6Excludes := SplitExcludesByFamily(excludes)
	v4DNS, v6DNS := SplitDNSByFamily(dnsServers)

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
	// Create NETFERRY chain.
	ipt("-t", "nat", "-N", "NETFERRY")
	ipt("-t", "nat", "-A", "OUTPUT", "-j", "NETFERRY")
	ipt("-t", "nat", "-A", "PREROUTING", "-j", "NETFERRY")

	// Local traffic protection.
	ipt("-t", "nat", "-A", "NETFERRY", "-m", "addrtype", "--dst-type", "LOCAL", "-j", "RETURN")

	// Exclude specified subnets.
	for _, excl := range v4Excludes {
		ipt("-t", "nat", "-A", "NETFERRY", "-d", excl, "-j", "RETURN")
	}

	// Redirect target subnets.
	for _, subnet := range v4Subnets {
		args := []string{"-t", "nat", "-A", "NETFERRY", "-d", subnet.CIDR, "-p", "tcp", "-m", "tcp"}
		portArgs := subnet.IptPortArgs()
		if portArgs != nil {
			args = append(args, portArgs...)
		} else {
			args = append(args, "!", "--dport", strconv.Itoa(proxyPort))
		}
		args = append(args, "-j", "REDIRECT", "--to-ports", strconv.Itoa(proxyPort))
		if err := ipt(args...); err != nil {
			return err
		}
	}

	// DNS redirect.
	if dnsPort > 0 {
		for _, ns := range v4DNS {
			ipt("-t", "nat", "-A", "NETFERRY", "-d", ns,
				"-p", "udp", "-m", "udp", "--dport", "53",
				"-j", "REDIRECT", "--to-ports", strconv.Itoa(dnsPort))
		}
	}

	// --- IPv6 ---
	if len(v6Subnets) > 0 || (dnsPort > 0 && len(v6DNS) > 0) {
		// Create NETFERRY6 chain.
		ip6t("-t", "nat", "-N", "NETFERRY6")
		ip6t("-t", "nat", "-A", "OUTPUT", "-j", "NETFERRY6")
		ip6t("-t", "nat", "-A", "PREROUTING", "-j", "NETFERRY6")

		// Local traffic protection.
		ip6t("-t", "nat", "-A", "NETFERRY6", "-m", "addrtype", "--dst-type", "LOCAL", "-j", "RETURN")

		// Exclude specified subnets.
		for _, excl := range v6Excludes {
			ip6t("-t", "nat", "-A", "NETFERRY6", "-d", excl, "-j", "RETURN")
		}

		// Redirect target subnets.
		for _, subnet := range v6Subnets {
			args := []string{"-t", "nat", "-A", "NETFERRY6", "-d", subnet.CIDR, "-p", "tcp", "-m", "tcp"}
			portArgs := subnet.IptPortArgs()
			if portArgs != nil {
				args = append(args, portArgs...)
			} else {
				args = append(args, "!", "--dport", strconv.Itoa(proxyPort))
			}
			args = append(args, "-j", "REDIRECT", "--to-ports", strconv.Itoa(proxyPort))
			if err := ip6t(args...); err != nil {
				return err
			}
		}

		// DNS redirect.
		if dnsPort > 0 {
			for _, ns := range v6DNS {
				ip6t("-t", "nat", "-A", "NETFERRY6", "-d", ns,
					"-p", "udp", "-m", "udp", "--dport", "53",
					"-j", "REDIRECT", "--to-ports", strconv.Itoa(dnsPort))
			}
		}
	}

	return nil
}

func (p *iptMethod) Restore() error {
	// IPv4 cleanup.
	exec.Command("iptables", "-t", "nat", "-D", "OUTPUT", "-j", "NETFERRY").Run()
	exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-j", "NETFERRY").Run()
	exec.Command("iptables", "-t", "nat", "-F", "NETFERRY").Run()
	exec.Command("iptables", "-t", "nat", "-X", "NETFERRY").Run()

	// IPv6 cleanup.
	exec.Command("ip6tables", "-t", "nat", "-D", "OUTPUT", "-j", "NETFERRY6").Run()
	exec.Command("ip6tables", "-t", "nat", "-D", "PREROUTING", "-j", "NETFERRY6").Run()
	exec.Command("ip6tables", "-t", "nat", "-F", "NETFERRY6").Run()
	exec.Command("ip6tables", "-t", "nat", "-X", "NETFERRY6").Run()
	return nil
}
