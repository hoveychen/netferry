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
type winDivertMethod struct {
	handle    *divert.Handle
	proxyPort int
	dnsPort   int
	subnets   []net.IPNet
	excludes  []net.IPNet

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

func (w *winDivertMethod) SupportedFeatures() []Feature {
	return []Feature{FeatureDNS, FeaturePortRange, FeatureIPv6}
}

func (w *winDivertMethod) Setup(subnets []SubnetRule, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	w.proxyPort = proxyPort
	w.dnsPort = dnsPort
	w.stopCh = make(chan struct{})
	w.portRanges = make(map[int][2]uint16)

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

	// Capture all outbound IPv4/IPv6 TCP/UDP. Subnet matching is done in
	// userspace for flexibility (WinDivert filter language has limited CIDR
	// support).
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

	if addr.Outbound() {
		if !w.shouldIntercept(dstIP, dstPort) {
			w.handle.Send(pkt, addr)
			return
		}

		if proto == 6 { // TCP
			// Skip traffic already destined for our proxy.
			if isLoopback(dstIP) && dstPort == uint16(w.proxyPort) {
				w.handle.Send(pkt, addr)
				return
			}

			// Record original destination and rewrite to local proxy.
			key := connKey{proto: 6, ipVer: ipVer, srcPort: srcPort}
			w.connTrack.Store(key, connEntry{
				origDstIP:   append(net.IP(nil), dstIP...),
				origDstPort: dstPort,
				created:     time.Now(),
			})

			// Rewrite destination to loopback.
			if ipVer == 4 {
				copy(pkt[dstAddrOff:dstAddrOff+addrLen], net.IPv4(127, 0, 0, 1).To4())
			} else {
				copy(pkt[dstAddrOff:dstAddrOff+addrLen], net.IPv6loopback)
			}
			binary.BigEndian.PutUint16(transport[portDst:], uint16(w.proxyPort))

		} else if proto == 17 && dstPort == 53 && w.dnsPort > 0 { // UDP DNS
			key := connKey{proto: 17, ipVer: ipVer, srcPort: srcPort}
			w.connTrack.Store(key, connEntry{
				origDstIP:   append(net.IP(nil), dstIP...),
				origDstPort: 53,
				created:     time.Now(),
			})

			if ipVer == 4 {
				copy(pkt[dstAddrOff:dstAddrOff+addrLen], net.IPv4(127, 0, 0, 1).To4())
			} else {
				copy(pkt[dstAddrOff:dstAddrOff+addrLen], net.IPv6loopback)
			}
			binary.BigEndian.PutUint16(transport[portDst:], uint16(w.dnsPort))
		} else {
			w.handle.Send(pkt, addr)
			return
		}
	} else {
		// Inbound: restore source address for responses from our proxy.
		key := connKey{proto: proto, ipVer: ipVer, srcPort: dstPort}
		if entry, ok := w.connTrack.Load(key); ok {
			e := entry.(connEntry)
			if isLoopback(srcIP) {
				if ipVer == 4 {
					copy(pkt[srcAddrOff:srcAddrOff+addrLen], e.origDstIP.To4())
				} else {
					copy(pkt[srcAddrOff:srcAddrOff+addrLen], e.origDstIP.To16())
				}
				binary.BigEndian.PutUint16(transport[portSrc:], e.origDstPort)
			}
		} else {
			// No conntrack entry — pass through unchanged.
			w.handle.Send(pkt, addr)
			return
		}
	}

	// Recalculate checksums after header modification.
	divert.HelperCalcChecksum(&divert.Packet{Content: pkt, Addr: addr}, divert.All)
	w.handle.Send(pkt, addr)
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

func isLoopback(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		return ip4[0] == 127
	}
	return ip.Equal(net.IPv6loopback)
}
