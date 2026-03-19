//go:build darwin

package firewall

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
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
	PF_OUT     = 2
	IPPROTO_TCP = 6
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
	pnl.af = syscall.AF_INET

	copy(pnl.saddr.addr[:], peerAddr.IP.To4())
	copy(pnl.daddr.addr[:], proxyAddr.IP.To4())
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

	ip := net.IP(pnl.rdaddr.addr[:4]).String()
	port := int(ntohs(pnl.rdxport.port))
	return ip, port, nil
}

var pfDev *os.File

func openPFDev() (int, error) {
	if pfDev == nil {
		f, err := os.OpenFile("/dev/pf", os.O_RDWR, 0)
		if err != nil {
			return -1, fmt.Errorf("open /dev/pf: %w (running as root?)", err)
		}
		pfDev = f
	}
	return int(pfDev.Fd()), nil
}

func htons(v uint16) uint16 { return (v>>8)|(v<<8) }
func ntohs(v uint16) uint16 { return htons(v) }

// pfMethod implements firewall.Method using macOS pf.
type pfMethod struct {
	anchor string
	token  string
}

func (p *pfMethod) Name() string { return "pf" }

func (p *pfMethod) Setup(subnets, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
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
	return nil
}

func (p *pfMethod) addAnchors() {
	// Get current main ruleset.
	currentRules, _ := pfctl("-s", "rules")
	currentNAT, _ := pfctl("-s", "nat")

	rdrAnchor := fmt.Sprintf("rdr-anchor \"%s\"", p.anchor)
	anchor := fmt.Sprintf("anchor \"%s\"", p.anchor)

	// Rebuild the main ruleset with our anchors prepended if not already present.
	var rules bytes.Buffer

	// Add rdr-anchor to NAT rules if not present.
	if !bytes.Contains(currentNAT, []byte(rdrAnchor)) {
		fmt.Fprintln(&rules, rdrAnchor)
	}
	if len(currentNAT) > 0 {
		rules.Write(bytes.TrimSpace(currentNAT))
		rules.WriteByte('\n')
	}

	// Add anchor to filter rules if not present.
	if !bytes.Contains(currentRules, []byte(anchor)) {
		fmt.Fprintln(&rules, anchor)
	}
	if len(currentRules) > 0 {
		rules.Write(bytes.TrimSpace(currentRules))
		rules.WriteByte('\n')
	}

	pfctlStdin(rules.Bytes(), "-f", "/dev/stdin")
}

func (p *pfMethod) buildRules(subnets, excludes []string, proxyPort, dnsPort int, dnsServers []string) []byte {
	var b bytes.Buffer

	// DNS table.
	if dnsPort > 0 && len(dnsServers) > 0 {
		fmt.Fprintf(&b, "table <dns_servers> {%s}\n", strings.Join(dnsServers, ","))
	}

	// Per-subnet redirect rules.
	for _, excl := range excludes {
		fmt.Fprintf(&b, "pass out inet proto tcp to %s\n", excl)
	}
	for _, subnet := range subnets {
		fmt.Fprintf(&b,
			"rdr pass on lo0 inet proto tcp from ! 127.0.0.1 to %s -> 127.0.0.1 port %d\n",
			subnet, proxyPort)
		fmt.Fprintf(&b,
			"pass out route-to lo0 inet proto tcp to %s keep state\n",
			subnet)
	}

	// DNS redirect.
	if dnsPort > 0 && len(dnsServers) > 0 {
		fmt.Fprintf(&b,
			"rdr pass on lo0 inet proto udp to <dns_servers> port 53 -> 127.0.0.1 port %d\n",
			dnsPort)
		fmt.Fprintf(&b,
			"pass out route-to lo0 inet proto udp to <dns_servers> port 53 keep state\n")
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
