//go:build linux

package firewall

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
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
	}
	return nil, fmt.Errorf("firewall method %q not supported on Linux", name)
}

// SO_ORIGINAL_DST retrieves the original destination of a redirected TCP
// connection from the kernel's conntrack table (requires iptables/nftables NAT).
const SO_ORIGINAL_DST = 80

// QueryOrigDst returns the original destination IP and port of a conn that was
// redirected by iptables/nftables REDIRECT.
func QueryOrigDst(sock net.Conn) (dstIP string, dstPort int, err error) {
	tc, ok := sock.(*net.TCPConn)
	if !ok {
		return "", 0, fmt.Errorf("not a TCP conn")
	}
	rawConn, err := tc.SyscallConn()
	if err != nil {
		return "", 0, err
	}

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
		// Fallback: use local addr.
		la := sock.LocalAddr().(*net.TCPAddr)
		return la.IP.String(), la.Port, nil
	}

	ip := net.IP(origDst.Addr[:]).String()
	port := int(origDst.Port>>8 | origDst.Port<<8) // ntohs
	return ip, port, nil
}

// CleanStaleAnchors is a no-op on Linux (nft/ipt use named tables, cleaned on Restore).
func CleanStaleAnchors() {
	exec.Command("nft", "delete", "table", "ip", "netferry").Run()
}

// nftMethod implements firewall.Method using nftables.
type nftMethod struct{}

func (n *nftMethod) Name() string { return "nft" }

func (n *nftMethod) Setup(subnets, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	// Remove any leftover table first.
	exec.Command("nft", "delete", "table", "ip", "netferry").Run()

	var b bytes.Buffer
	fmt.Fprintf(&b, "table ip netferry {\n")

	// DNS set.
	if dnsPort > 0 && len(dnsServers) > 0 {
		fmt.Fprintf(&b, "  set dns_servers {\n    type ipv4_addr\n    elements = {%s}\n  }\n",
			joinQuoted(dnsServers))
	}

	fmt.Fprintf(&b, "  chain prerouting {\n    type nat hook prerouting priority -100;\n")
	for _, excl := range excludes {
		fmt.Fprintf(&b, "    ip daddr %s return\n", excl)
	}
	for _, subnet := range subnets {
		fmt.Fprintf(&b, "    ip daddr %s tcp dport != %d redirect to :%d\n",
			subnet, proxyPort, proxyPort)
	}
	if dnsPort > 0 && len(dnsServers) > 0 {
		fmt.Fprintf(&b, "    ip daddr @dns_servers udp dport 53 redirect to :%d\n", dnsPort)
	}
	fmt.Fprintf(&b, "  }\n")

	fmt.Fprintf(&b, "  chain output {\n    type nat hook output priority -100;\n")
	for _, excl := range excludes {
		fmt.Fprintf(&b, "    ip daddr %s return\n", excl)
	}
	for _, subnet := range subnets {
		fmt.Fprintf(&b, "    ip daddr %s tcp dport != %d redirect to :%d\n",
			subnet, proxyPort, proxyPort)
	}
	if dnsPort > 0 && len(dnsServers) > 0 {
		fmt.Fprintf(&b, "    ip daddr @dns_servers udp dport 53 redirect to :%d\n", dnsPort)
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
	exec.Command("nft", "delete", "table", "ip", "netferry").Run()
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

// iptMethod implements firewall.Method using iptables.
type iptMethod struct{}

func (p *iptMethod) Name() string { return "iptables" }

func (p *iptMethod) Setup(subnets, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	ipt := func(args ...string) error {
		cmd := exec.Command("iptables", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("iptables %v: %w\n%s", args, err, out)
		}
		return nil
	}

	// Create NETFERRY chain.
	ipt("-t", "nat", "-N", "NETFERRY")
	ipt("-t", "nat", "-A", "OUTPUT", "-j", "NETFERRY")
	ipt("-t", "nat", "-A", "PREROUTING", "-j", "NETFERRY")

	// Exclude local traffic.
	ipt("-t", "nat", "-A", "NETFERRY", "-m", "addrtype", "--dst-type", "LOCAL", "-j", "RETURN")

	// Exclude specified subnets.
	for _, excl := range excludes {
		ipt("-t", "nat", "-A", "NETFERRY", "-d", excl, "-j", "RETURN")
	}

	// Redirect target subnets.
	for _, subnet := range subnets {
		if err := ipt("-t", "nat", "-A", "NETFERRY", "-d", subnet,
			"-p", "tcp", "-j", "REDIRECT", "--to-ports", fmt.Sprintf("%d", proxyPort)); err != nil {
			return err
		}
	}

	// DNS redirect.
	if dnsPort > 0 {
		for _, ns := range dnsServers {
			ipt("-t", "nat", "-A", "NETFERRY", "-d", ns,
				"-p", "udp", "--dport", "53",
				"-j", "REDIRECT", "--to-ports", fmt.Sprintf("%d", dnsPort))
		}
	}

	return nil
}

func (p *iptMethod) Restore() error {
	exec.Command("iptables", "-t", "nat", "-D", "OUTPUT", "-j", "NETFERRY").Run()
	exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-j", "NETFERRY").Run()
	exec.Command("iptables", "-t", "nat", "-F", "NETFERRY").Run()
	exec.Command("iptables", "-t", "nat", "-X", "NETFERRY").Run()
	return nil
}

