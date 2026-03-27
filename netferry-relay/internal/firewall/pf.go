//go:build darwin

package firewall

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

func newDefault() Method { return &pfMethod{} }
func newNamed(name string) (Method, error) {
	if name == "pf" || name == "auto" {
		return &pfMethod{}, nil
	}
	return nil, fmt.Errorf("firewall method %q not supported on macOS (only pf)", name)
}

func listMethodFeatures() map[string][]Feature {
	return map[string][]Feature{
		"pf": (&pfMethod{}).SupportedFeatures(),
	}
}

// DIOCNATLOOK ioctl constant for Darwin.
// Computed from: 0xC0000000 | (sizeof(pfioc_natlook) << 16) | ('D' << 8) | 23
// sizeof(Darwin pfioc_natlook) = 84 bytes = 0x54
const DIOCNATLOOK = uintptr(0xC0544417)

// pfioc_natlook matches the Darwin kernel struct exactly (84 bytes).
// Layout verified against XNU source / pf.py Darwin class.
type pfStateXport struct {
	port uint16
	_    [2]byte // union padding (call_id/spi share the 4 bytes)
}

type pfAddr struct {
	addr [16]byte // union: v4 in first 4 bytes, v6 in all 16
}

type pfioc_natlook struct {
	saddr    pfAddr
	daddr    pfAddr
	rsaddr   pfAddr
	rdaddr   pfAddr
	sxport   pfStateXport
	dxport   pfStateXport
	rsxport  pfStateXport
	rdxport  pfStateXport
	af       uint8
	proto    uint8
	protoVar uint8
	dir      uint8
}

const (
	PF_OUT      = 2
	IPPROTO_TCP = 6

	// Darwin AF_INET6 = 30 (NOT 10 like Linux).
	_AF_INET6_DARWIN = 30

	// ioctl constants for DIOCCHANGERULE / DIOCBEGINADDRS on Darwin.
	// Computed from: 0xC0000000 | ((sizeof(struct) & 0x1FFF) << 16) | ('D' << 8) | cmd
	// sizeof(pfioc_rule)     = 3104 = 0xC20  → DIOCCHANGERULE
	// sizeof(pfioc_pooladdr) = 1136 = 0x470  → DIOCBEGINADDRS
	_DIOCCHANGERULE = uintptr(0xCC20441A)
	_DIOCBEGINADDRS = uintptr(0xC4704433)

	// Offsets into the pfioc_rule struct (Darwin / XNU).
	_PFIOC_RULE_SIZE     = 3104
	_PFIOC_POOLADDR_SIZE = 1136
	_ACTION_OFFSET       = 0
	_POOL_TICKET_OFFSET  = 8
	_ANCHOR_CALL_OFFSET  = 1040
	_RULE_ACTION_OFFSET  = 3068 // Darwin-specific

	// pf rule actions / change commands.
	_PF_CHANGE_ADD_TAIL   = 2
	_PF_CHANGE_GET_TICKET = 6
	_PF_PASS              = 0
	_PF_RDR               = 8
)

// QueryNATLook resolves the original destination of a redirected TCP connection
// by calling DIOCNATLOOK on /dev/pf.
//
// sock is the accepted net.Conn from the local proxy listener.
// The kernel records the pre-redirect (src, dst) in the pf state table;
// this function retrieves the original dst.
func QueryNATLook(sock net.Conn) (dstIP string, dstPort int, err error) {
	peerAddr := sock.RemoteAddr().(*net.TCPAddr)
	proxyAddr := sock.LocalAddr().(*net.TCPAddr)

	fd, err := openPFDev()
	if err != nil {
		return "", 0, err
	}

	var pnl pfioc_natlook
	pnl.proto = IPPROTO_TCP
	pnl.dir = PF_OUT

	isV6 := peerAddr.IP.To4() == nil

	if isV6 {
		pnl.af = _AF_INET6_DARWIN
		copy(pnl.saddr.addr[:], peerAddr.IP.To16())
		copy(pnl.daddr.addr[:], proxyAddr.IP.To16())
	} else {
		pnl.af = syscall.AF_INET
		copy(pnl.saddr.addr[:], peerAddr.IP.To4())
		copy(pnl.daddr.addr[:], proxyAddr.IP.To4())
	}

	pnl.sxport.port = htons(uint16(peerAddr.Port))
	pnl.dxport.port = htons(uint16(proxyAddr.Port))

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		DIOCNATLOOK,
		uintptr(unsafe.Pointer(&pnl)),
	)
	if errno != 0 {
		// Fall back to using the local addr as the destination
		// (happens for connections that weren't NATed, e.g. direct localhost).
		return proxyAddr.IP.String(), proxyAddr.Port, nil
	}

	var ip string
	if isV6 {
		ip = net.IP(pnl.rdaddr.addr[:16]).String()
	} else {
		ip = net.IP(pnl.rdaddr.addr[:4]).String()
	}
	port := int(ntohs(pnl.rdxport.port))
	return ip, port, nil
}

var (
	pfDev     *os.File
	pfDevOnce sync.Once
	pfDevErr  error
)

func openPFDev() (int, error) {
	pfDevOnce.Do(func() {
		f, err := os.OpenFile("/dev/pf", os.O_RDWR, 0)
		if err != nil {
			pfDevErr = fmt.Errorf("open /dev/pf: %w (running as root?)", err)
			return
		}
		pfDev = f
	})
	if pfDevErr != nil {
		return -1, pfDevErr
	}
	return int(pfDev.Fd()), nil
}

func htons(v uint16) uint16 { return (v >> 8) | (v << 8) }
func ntohs(v uint16) uint16 { return htons(v) }

// pfMethod implements firewall.Method using macOS pf.
type pfMethod struct {
	anchor string
	token  string
}

func (p *pfMethod) Name() string { return "pf" }

func (p *pfMethod) SupportedFeatures() []Feature {
	return []Feature{FeatureDNS, FeaturePortRange, FeatureIPv6}
}

func (p *pfMethod) Setup(subnets []SubnetRule, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	p.anchor = fmt.Sprintf("netferry-%d", proxyPort)

	// pfctl -E: enable pf and capture reference token.
	out, err := pfctl("-E")
	if err != nil {
		return fmt.Errorf("pfctl -E: %w", err)
	}
	if m := regexp.MustCompile(`Token : (\S+)`).FindSubmatch(out); len(m) > 1 {
		p.token = string(m[1])
	}

	// Fix "set skip on lo0" if present — our rdr rules run on lo0.
	status, _ := pfctl("-s", "Interfaces", "-i", "lo0", "-v")
	if bytes.Contains(status, []byte("skip")) {
		pfctlStdin([]byte("pass on lo\n"), "-f", "/dev/stdin")
	}

	// Add anchor hooks to main pf ruleset.
	p.addAnchors()

	// Build and load the anchor rules.
	rules := p.buildRules(subnets, excludes, proxyPort, dnsPort, dnsServers)
	if err := pfctlStdin(rules, "-a", p.anchor, "-f", "/dev/stdin"); err != nil {
		return fmt.Errorf("load pf rules: %w", err)
	}

	// Flush existing PF states for intercepted subnets so they are re-evaluated
	// by the new rules. Without this, connections established before Setup was
	// called continue to bypass the redirect rules (PF keeps state per-connection).
	p.flushStates(subnets)
	return nil
}

// flushStates kills PF state table entries destined for the given subnets.
// This forces existing connections to be re-evaluated by the new redirect rules.
// pfctl -k sends RSTs to both endpoints, so apps reconnect quickly.
func (p *pfMethod) flushStates(subnets []SubnetRule) {
	for _, subnet := range subnets {
		if subnet.IsIPv6() {
			pfctl("-k", "::/0", "-k", subnet.CIDR)
		} else {
			pfctl("-k", "0/0", "-k", subnet.CIDR)
		}
	}
}

func (p *pfMethod) addAnchors() {
	fd, err := openPFDev()
	if err != nil {
		return
	}

	// Check which anchors already exist.
	status, _ := pfctl("-s", "all")

	rdrAnchor := fmt.Sprintf(`rdr-anchor "%s"`, p.anchor)
	anchor := fmt.Sprintf(`anchor "%s"`, p.anchor)

	// Use ioctl DIOCCHANGERULE to append anchor rules individually,
	// matching sshuttle's approach. This avoids reloading the entire
	// ruleset which breaks due to ordering and unresolvable references.
	if !bytes.Contains(status, []byte(rdrAnchor)) {
		addAnchorRule(fd, _PF_RDR, p.anchor)
	}
	if !bytes.Contains(status, []byte(anchor)) {
		addAnchorRule(fd, _PF_PASS, p.anchor)
	}
}

// addAnchorRule appends a single anchor rule (rdr-anchor or anchor) to the
// running pf ruleset using DIOCCHANGERULE ioctl, without disturbing existing rules.
func addAnchorRule(fd int, kind uint32, name string) {
	pr := make([]byte, _PFIOC_RULE_SIZE)
	ppa := make([]byte, _PFIOC_POOLADDR_SIZE)

	// DIOCBEGINADDRS — required by FreeBSD/Darwin before DIOCCHANGERULE.
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), _DIOCBEGINADDRS,
		uintptr(unsafe.Pointer(&ppa[0])))

	// Copy pool ticket from ppa to pr.
	copy(pr[_POOL_TICKET_OFFSET:_POOL_TICKET_OFFSET+4], ppa[4:8])

	// Set anchor_call = name.
	copy(pr[_ANCHOR_CALL_OFFSET:], []byte(name))

	// Set rule.action = kind (PF_PASS for anchor, PF_RDR for rdr-anchor).
	binary.LittleEndian.PutUint32(pr[_RULE_ACTION_OFFSET:], kind)

	// Step 1: PF_CHANGE_GET_TICKET
	binary.LittleEndian.PutUint32(pr[_ACTION_OFFSET:], _PF_CHANGE_GET_TICKET)
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), _DIOCCHANGERULE,
		uintptr(unsafe.Pointer(&pr[0])))

	// Step 2: PF_CHANGE_ADD_TAIL
	binary.LittleEndian.PutUint32(pr[_ACTION_OFFSET:], _PF_CHANGE_ADD_TAIL)
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), _DIOCCHANGERULE,
		uintptr(unsafe.Pointer(&pr[0])))
}

func (p *pfMethod) buildRules(subnets []SubnetRule, excludes []string, proxyPort, dnsPort int, dnsServers []string) []byte {
	var b bytes.Buffer

	v4Subnets, v6Subnets := SplitByFamily(subnets)
	v4Excludes, v6Excludes := SplitExcludesByFamily(excludes)
	v4DNS, v6DNS := SplitDNSByFamily(dnsServers)

	// pf requires rules in order: tables, then ALL translation (rdr), then ALL filtering (pass).

	// --- Tables ---
	if dnsPort > 0 && len(v4DNS) > 0 {
		fmt.Fprintf(&b, "table <dns_servers> {%s}\n", strings.Join(v4DNS, ","))
	}
	if dnsPort > 0 && len(v6DNS) > 0 {
		fmt.Fprintf(&b, "table <dns6_servers> {%s}\n", strings.Join(v6DNS, ","))
	}

	// --- Translation rules (rdr) ---

	// IPv4 subnet rdr rules.
	for _, subnet := range v4Subnets {
		fmt.Fprintf(&b,
			"rdr pass on lo0 inet proto tcp from ! 127.0.0.1 to %s%s -> 127.0.0.1 port %d\n",
			subnet.CIDR, subnet.PfPortExpr(), proxyPort)
	}
	// IPv6 subnet rdr rules.
	for _, subnet := range v6Subnets {
		fmt.Fprintf(&b,
			"rdr pass on lo0 inet6 proto tcp from ! ::1 to %s%s -> ::1 port %d\n",
			subnet.CIDR, subnet.PfPortExpr(), proxyPort)
	}
	// DNS rdr rules.
	if dnsPort > 0 && len(v4DNS) > 0 {
		fmt.Fprintf(&b,
			"rdr pass on lo0 inet proto udp to <dns_servers> port 53 -> 127.0.0.1 port %d\n",
			dnsPort)
	}
	if dnsPort > 0 && len(v6DNS) > 0 {
		fmt.Fprintf(&b,
			"rdr pass on lo0 inet6 proto udp to <dns6_servers> port 53 -> ::1 port %d\n",
			dnsPort)
	}

	// --- Filtering rules (pass) ---
	// PF uses last-match-wins, so excludes must come AFTER subnet rules
	// to override the broader route-to lo0 rules.

	// IPv4 subnet pass rules.
	for _, subnet := range v4Subnets {
		fmt.Fprintf(&b,
			"pass out route-to lo0 inet proto tcp to %s%s keep state\n",
			subnet.CIDR, subnet.PfPortExpr())
	}
	// IPv6 subnet pass rules.
	for _, subnet := range v6Subnets {
		fmt.Fprintf(&b,
			"pass out route-to lo0 inet6 proto tcp to %s%s keep state\n",
			subnet.CIDR, subnet.PfPortExpr())
	}
	// DNS pass rules.
	if dnsPort > 0 && len(v4DNS) > 0 {
		fmt.Fprintf(&b,
			"pass out route-to lo0 inet proto udp to <dns_servers> port 53 keep state\n")
	}
	if dnsPort > 0 && len(v6DNS) > 0 {
		fmt.Fprintf(&b,
			"pass out route-to lo0 inet6 proto udp to <dns6_servers> port 53 keep state\n")
	}
	// IPv4 excludes (last-match-wins: these override the route-to rules above).
	for _, excl := range v4Excludes {
		fmt.Fprintf(&b, "pass out inet proto tcp to %s\n", excl)
	}
	// IPv6 excludes.
	for _, excl := range v6Excludes {
		fmt.Fprintf(&b, "pass out inet6 proto tcp to %s\n", excl)
	}

	return b.Bytes()
}

func (p *pfMethod) Restore() error {
	pfctl("-a", p.anchor, "-F", "all")
	if p.token != "" {
		pfctl("-X", p.token)
	}
	return nil
}

// pfctl runs pfctl with the given args and returns combined output.
func pfctl(args ...string) ([]byte, error) {
	cmd := exec.Command("pfctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("pfctl %v: %w\n%s", args, err, out)
	}
	return out, nil
}

// pfctlStdin runs pfctl with the given args and pipes stdin data.
func pfctlStdin(stdin []byte, args ...string) error {
	cmd := exec.Command("pfctl", args...)
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pfctl %v: %w\n%s", args, err, out)
	}
	return nil
}

// CleanStaleAnchors removes any leftover netferry-* pf anchors from a previous
// crashed run. Call at startup.
func CleanStaleAnchors() {
	out, err := exec.Command("pfctl", "-s", "all").Output()
	if err != nil {
		return
	}
	re := regexp.MustCompile(`anchor "(netferry-\d+)"`)
	for _, m := range re.FindAllSubmatch(out, -1) {
		anchor := string(m[1])
		exec.Command("pfctl", "-a", anchor, "-F", "all").Run()
	}
}
