//go:build windows

package firewall

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	divert "github.com/gone-lib/divert-go"
)

// winDivertMethod implements firewall.Method using WinDivert for transparent
// packet-level interception on Windows. Unlike the SOCKS5 system proxy
// approach (winMethod), this intercepts all TCP traffic regardless of whether
// the application honours system proxy settings.
//
// IMPORTANT: WinDivert cannot redirect packets from a public source address
// to a loopback destination (127.0.0.1). Windows silently drops packets that
// cross from public to loopback address space. The proxy must therefore bind
// to a non-loopback address and packets are redirected there instead.
// See: https://github.com/basil00/Divert/issues/82
//
// Additionally, WinDivert classifies ALL local-process traffic as "outbound",
// including responses from the proxy back to the application. We detect proxy
// responses by matching source IP/port against the proxy address.
type winDivertMethod struct {
	handle    *divert.Handle
	proxyPort int
	dnsPort   int
	proxyIPv4 net.IP // non-loopback IPv4 address for the proxy
	proxyIPv6 net.IP // non-loopback IPv6 address for the proxy (may be nil)
	subnets   []net.IPNet
	excludes  []net.IPNet
	blockIPv6 bool

	// portRanges maps subnet index to port range. If the entry exists and
	// has non-zero values, only traffic within [low, high] is intercepted.
	portRanges map[int][2]uint16

	// connTrack maps {protocol, ipVer, srcPort} → original destination for
	// connections that have been redirected to the local proxy.
	connTrack sync.Map // connKey → connEntry

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type connKey struct {
	proto   uint8  // 6=TCP, 17=UDP
	ipVer   uint8  // 4 or 6
	srcPort uint16 // local ephemeral port
}

type connEntry struct {
	origDstIP   net.IP
	origDstPort uint16
	created     time.Time
}

func (w *winDivertMethod) Name() string { return "windivert" }

func (w *winDivertMethod) SetBlockIPv6(block bool) { w.blockIPv6 = block }

func (w *winDivertMethod) SupportedFeatures() []Feature {
	return []Feature{FeatureDNS, FeaturePortRange, FeatureIPv6}
}

func (w *winDivertMethod) Setup(subnets []SubnetRule, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	w.proxyPort = proxyPort
	w.dnsPort = dnsPort
	w.stopCh = make(chan struct{})
	w.portRanges = make(map[int][2]uint16)

	// WinDivert cannot redirect packets to loopback addresses — Windows
	// drops packets that cross from public to loopback address space.
	// Find a non-loopback interface IP to use as the proxy target.
	ipv4, ipv6, err := findNonLoopbackIPs()
	if err != nil {
		return fmt.Errorf("windivert: %w", err)
	}
	w.proxyIPv4 = ipv4
	w.proxyIPv6 = ipv6
	log.Printf("windivert: proxy target IPv4=%v IPv6=%v", ipv4, ipv6)

	for i, s := range subnets {
		_, ipnet, err := net.ParseCIDR(s.CIDR)
		if err != nil {
			return fmt.Errorf("parse subnet %q: %w", s.CIDR, err)
		}
		w.subnets = append(w.subnets, *ipnet)
		if s.HasPortRange() {
			w.portRanges[i] = [2]uint16{uint16(s.PortLow), uint16(s.PortHigh)}
		}
	}
	for _, s := range excludes {
		_, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			return fmt.Errorf("parse exclude %q: %w", s, err)
		}
		w.excludes = append(w.excludes, *ipnet)
	}

	// Capture all outbound IPv4/IPv6 TCP/UDP. WinDivert classifies ALL
	// local-process traffic as "outbound" (including proxy responses), so
	// this single filter captures both directions. Subnet matching and
	// proxy-response detection are done in userspace.
	filter := "outbound and (ip or ipv6) and (tcp or udp)"

	handle, err := divert.Open(filter, divert.LayerNetwork, 0, divert.FlagDefault)
	if err != nil {
		return fmt.Errorf("WinDivert open: %w", err)
	}
	w.handle = handle

	// Packet interception goroutine.
	w.wg.Add(1)
	go w.interceptLoop()

	// Stale entry cleanup goroutine.
	w.wg.Add(1)
	go w.cleanupLoop()

	return nil
}

// findNonLoopbackIPs returns the first non-loopback, non-link-local unicast
// IPv4 and IPv6 addresses found on the system's interfaces.
func findNonLoopbackIPs() (ipv4, ipv6 net.IP, err error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, fmt.Errorf("list interfaces: %w", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				if ipv4 == nil {
					ipv4 = ip4
				}
			} else if ip.To16() != nil {
				if ipv6 == nil {
					ipv6 = ip.To16()
				}
			}
		}
	}
	if ipv4 == nil {
		return nil, nil, fmt.Errorf("no non-loopback IPv4 address found; WinDivert requires a LAN interface")
	}
	return ipv4, ipv6, nil
}

func (w *winDivertMethod) Restore() error {
	close(w.stopCh)
	if w.handle != nil {
		w.handle.Shutdown(divert.ShutdownBoth)
		w.handle.Close()
	}
	w.wg.Wait()
	return nil
}

func (w *winDivertMethod) interceptLoop() {
	defer w.wg.Done()

	buf := make([]byte, 65535)
	var addr divert.Address

	for {
		select {
		case <-w.stopCh:
			return
		default:
		}

		n, err := w.handle.Recv(buf, &addr)
		if err != nil {
			select {
			case <-w.stopCh:
				return
			default:
				log.Printf("windivert recv: %v", err)
				continue
			}
		}

		packet := make([]byte, n)
		copy(packet, buf[:n])

		w.processPacket(packet, &addr)
	}
}

// IPv4 header offsets (big-endian).
const (
	ipv4VerIHL  = 0
	ipv4Proto   = 9
	ipv4SrcAddr = 12
	ipv4DstAddr = 16
	ipv4HdrMin  = 20
)

// IPv6 header offsets (big-endian).
const (
	ipv6NextHeader = 6
	ipv6SrcAddr    = 8
	ipv6DstAddr    = 24
	ipv6HdrLen     = 40
)

// TCP/UDP port offsets (relative to transport header start).
const (
	portSrc = 0
	portDst = 2
)

func (w *winDivertMethod) processPacket(pkt []byte, addr *divert.Address) {
	if len(pkt) < ipv4HdrMin {
		w.handle.Send(pkt, addr)
		return
	}

	ver := pkt[ipv4VerIHL] >> 4

	var (
		proto      uint8
		ipVer      uint8
		srcIP      net.IP
		dstIP      net.IP
		transport  []byte
		srcAddrOff int
		dstAddrOff int
		addrLen    int
	)

	switch ver {
	case 4:
		ipVer = 4
		ihl := int(pkt[ipv4VerIHL]&0x0f) * 4
		if len(pkt) < ihl+4 {
			w.handle.Send(pkt, addr)
			return
		}
		proto = pkt[ipv4Proto]
		srcIP = net.IP(pkt[ipv4SrcAddr : ipv4SrcAddr+4])
		dstIP = net.IP(pkt[ipv4DstAddr : ipv4DstAddr+4])
		transport = pkt[ihl:]
		srcAddrOff = ipv4SrcAddr
		dstAddrOff = ipv4DstAddr
		addrLen = 4

	case 6:
		ipVer = 6
		if len(pkt) < ipv6HdrLen+4 {
			w.handle.Send(pkt, addr)
			return
		}
		proto = pkt[ipv6NextHeader]
		srcIP = net.IP(pkt[ipv6SrcAddr : ipv6SrcAddr+16])
		dstIP = net.IP(pkt[ipv6DstAddr : ipv6DstAddr+16])
		transport = pkt[ipv6HdrLen:]
		srcAddrOff = ipv6SrcAddr
		dstAddrOff = ipv6DstAddr
		addrLen = 16

	default:
		w.handle.Send(pkt, addr)
		return
	}

	srcPort := binary.BigEndian.Uint16(transport[portSrc:])
	dstPort := binary.BigEndian.Uint16(transport[portDst:])

	// WinDivert classifies ALL local-process traffic as "outbound", so we
	// cannot rely on addr.Outbound() to distinguish directions. Instead we
	// detect proxy responses by matching src IP/port against the proxy.

	// --- IPv6 blanket block (when --no-ipv6) ---
	// Removing IPv6 redirect rules alone leaves apps free to reach AAAA
	// destinations over native IPv6 (Happy Eyeballs prefers IPv6), bypassing
	// the tunnel. Drop all outbound IPv6 except loopback / link-local /
	// multicast so NDP / DHCPv6 / local services keep working.
	if w.blockIPv6 && ipVer == 6 {
		if !dstIP.IsLoopback() && !dstIP.IsLinkLocalUnicast() && !dstIP.IsLinkLocalMulticast() && !dstIP.IsMulticast() {
			// Drop by not re-injecting the packet.
			return
		}
	}

	// ── Proxy response (reverse NAT) ─────────────────────────────────────
	// Packets FROM our proxy back to the application need their source
	// address restored to the original remote destination.
	if w.isFromProxy(srcIP, srcPort, proto) {
		key := connKey{proto: proto, ipVer: ipVer, srcPort: dstPort}
		if entry, ok := w.connTrack.Load(key); ok {
			e := entry.(connEntry)
			if ipVer == 4 {
				copy(pkt[srcAddrOff:srcAddrOff+addrLen], e.origDstIP.To4())
			} else {
				copy(pkt[srcAddrOff:srcAddrOff+addrLen], e.origDstIP.To16())
			}
			binary.BigEndian.PutUint16(transport[portSrc:], e.origDstPort)
			divert.HelperCalcChecksum(&divert.Packet{Content: pkt, Addr: addr}, divert.All)
			w.handle.Send(pkt, addr)
			return
		}
		// No conntrack entry — pass through unchanged (not a redirected conn).
		w.handle.Send(pkt, addr)
		return
	}

	// ── Skip traffic already destined for our proxy ──────────────────────
	if w.isToProxy(dstIP, dstPort) {
		w.handle.Send(pkt, addr)
		return
	}

	// ── Outbound: subnet matching + DNAT to proxy ────────────────────────
	if !w.shouldIntercept(dstIP, dstPort) {
		w.handle.Send(pkt, addr)
		return
	}

	proxyIP := w.proxyIPForVer(ipVer)
	if proxyIP == nil {
		// No proxy address for this IP version — pass through.
		w.handle.Send(pkt, addr)
		return
	}

	if proto == 6 { // TCP
		key := connKey{proto: 6, ipVer: ipVer, srcPort: srcPort}
		w.connTrack.Store(key, connEntry{
			origDstIP:   append(net.IP(nil), dstIP...),
			origDstPort: dstPort,
			created:     time.Now(),
		})
		copy(pkt[dstAddrOff:dstAddrOff+addrLen], proxyIP)
		binary.BigEndian.PutUint16(transport[portDst:], uint16(w.proxyPort))

	} else if proto == 17 && dstPort == 53 && w.dnsPort > 0 { // UDP DNS
		key := connKey{proto: 17, ipVer: ipVer, srcPort: srcPort}
		w.connTrack.Store(key, connEntry{
			origDstIP:   append(net.IP(nil), dstIP...),
			origDstPort: 53,
			created:     time.Now(),
		})
		copy(pkt[dstAddrOff:dstAddrOff+addrLen], proxyIP)
		binary.BigEndian.PutUint16(transport[portDst:], uint16(w.dnsPort))

	} else {
		w.handle.Send(pkt, addr)
		return
	}

	// Recalculate checksums after header modification.
	divert.HelperCalcChecksum(&divert.Packet{Content: pkt, Addr: addr}, divert.All)
	w.handle.Send(pkt, addr)
}

// isFromProxy returns true if the packet originates from our local proxy.
func (w *winDivertMethod) isFromProxy(srcIP net.IP, srcPort uint16, proto uint8) bool {
	port := uint16(w.proxyPort)
	if proto == 17 {
		port = uint16(w.dnsPort)
		if port == 0 {
			return false
		}
	}
	if srcPort != port {
		return false
	}
	if w.proxyIPv4 != nil && srcIP.Equal(w.proxyIPv4) {
		return true
	}
	if w.proxyIPv6 != nil && srcIP.Equal(w.proxyIPv6) {
		return true
	}
	return false
}

// isToProxy returns true if the packet is already destined for our proxy.
func (w *winDivertMethod) isToProxy(dstIP net.IP, dstPort uint16) bool {
	if dstPort != uint16(w.proxyPort) && dstPort != uint16(w.dnsPort) {
		return false
	}
	if w.proxyIPv4 != nil && dstIP.Equal(w.proxyIPv4) {
		return true
	}
	if w.proxyIPv6 != nil && dstIP.Equal(w.proxyIPv6) {
		return true
	}
	return false
}

// proxyIPForVer returns the proxy target IP for the given IP version.
func (w *winDivertMethod) proxyIPForVer(ipVer uint8) net.IP {
	if ipVer == 4 {
		return w.proxyIPv4
	}
	return w.proxyIPv6
}

func (w *winDivertMethod) shouldIntercept(ip net.IP, dstPort uint16) bool {
	for i := range w.excludes {
		if w.excludes[i].Contains(ip) {
			return false
		}
	}
	for i := range w.subnets {
		if w.subnets[i].Contains(ip) {
			// Check port range if one is configured for this subnet.
			if pr, ok := w.portRanges[i]; ok {
				if dstPort < pr[0] || dstPort > pr[1] {
					return false
				}
			}
			return true
		}
	}
	return false
}

func (w *winDivertMethod) cleanupLoop() {
	defer w.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-2 * time.Minute)
			w.connTrack.Range(func(k, v interface{}) bool {
				if v.(connEntry).created.Before(cutoff) {
					w.connTrack.Delete(k)
				}
				return true
			})
		}
	}
}

// QueryOrigDst returns the original destination for a connection redirected by
// WinDivert. The proxy sees connections from the app's srcPort → proxy port;
// we use the remote port to look up the original destination.
func (w *winDivertMethod) QueryOrigDst(conn net.Conn) (string, int, error) {
	ra := conn.RemoteAddr().(*net.TCPAddr)
	// Try IPv4 first, then IPv6.
	for _, v := range []uint8{4, 6} {
		key := connKey{proto: 6, ipVer: v, srcPort: uint16(ra.Port)}
		if entry, ok := w.connTrack.Load(key); ok {
			e := entry.(connEntry)
			return e.origDstIP.String(), int(e.origDstPort), nil
		}
	}
	return "", 0, fmt.Errorf("windivert: no conntrack entry for srcPort %d", ra.Port)
}

// ProxyBindAddr returns the address the proxy should bind to on Windows.
// WinDivert requires a non-loopback address.
func (w *winDivertMethod) ProxyBindAddr() string {
	return "0.0.0.0"
}

