//go:build linux

package proxy

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

const (
	// IP_RECVORIGDSTADDR is the socket option to receive the original
	// destination address in ancillary data (Linux-specific).
	ipRecvOrigDstAddr = 20

	// udpFlowIdleTimeout is how long a UDP flow can be idle before cleanup.
	udpFlowIdleTimeout = 2 * time.Minute

	// udpFlowCleanupInterval is how often we scan for idle flows.
	udpFlowCleanupInterval = 30 * time.Second
)

// udpFlowKey identifies a unique UDP flow.
type udpFlowKey struct {
	srcIP   string
	srcPort int
	dstIP   string
	dstPort int
}

// udpFlow tracks a single bidirectional UDP flow.
type udpFlow struct {
	ch       *mux.UDPChannel
	lastSeen time.Time
	srcAddr  *net.UDPAddr // original client address for sending replies back
	connID   uint64       // stats connection ID for ConnClose; 0 if counters == nil
	srcStr   string
	dstStr   string
}

// ListenUDPTProxy starts a TPROXY-aware UDP listener that intercepts all UDP
// traffic (except DNS) and forwards it through the mux tunnel. It uses
// IP_RECVORIGDSTADDR to recover the original destination from redirected packets.
func ListenUDPTProxy(port int, client mux.TunnelClient, counters *stats.Counters) error {
	// Create a raw socket with IP_TRANSPARENT and IP_RECVORIGDSTADDR.
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return fmt.Errorf("udp tproxy: socket: %w", err)
	}

	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		syscall.Close(fd)
		return fmt.Errorf("udp tproxy: SO_REUSEADDR: %w", err)
	}
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, syscall.IP_TRANSPARENT, 1); err != nil {
		syscall.Close(fd)
		return fmt.Errorf("udp tproxy: IP_TRANSPARENT: %w", err)
	}
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, ipRecvOrigDstAddr, 1); err != nil {
		syscall.Close(fd)
		return fmt.Errorf("udp tproxy: IP_RECVORIGDSTADDR: %w", err)
	}

	sa := &syscall.SockaddrInet4{Port: port}
	// Bind to 0.0.0.0
	if err := syscall.Bind(fd, sa); err != nil {
		syscall.Close(fd)
		return fmt.Errorf("udp tproxy: bind :%d: %w", port, err)
	}

	log.Printf("proxy: UDP TPROXY listening on :%d", port)

	var (
		mu    sync.Mutex
		flows = make(map[udpFlowKey]*udpFlow)
	)

	// Start cleanup goroutine.
	go func() {
		ticker := time.NewTicker(udpFlowCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			mu.Lock()
			for key, flow := range flows {
				if now.Sub(flow.lastSeen) > udpFlowIdleTimeout {
					flow.ch.Close()
					if counters != nil && flow.connID != 0 {
						counters.ConnClose(flow.connID, flow.srcStr, flow.dstStr)
					}
					delete(flows, key)
				}
			}
			mu.Unlock()
		}
	}()

	// Main receive loop using recvmsg to get ancillary data.
	buf := make([]byte, 65536)
	oob := make([]byte, 256)

	for {
		n, oobn, _, from, err := syscall.Recvmsg(fd, buf, oob, 0)
		if err != nil {
			return fmt.Errorf("udp tproxy: recvmsg: %w", err)
		}

		// Parse source address.
		var srcIP string
		var srcPort int
		switch sa := from.(type) {
		case *syscall.SockaddrInet4:
			srcIP = net.IP(sa.Addr[:]).String()
			srcPort = sa.Port
		default:
			continue
		}

		// Parse original destination from ancillary data.
		dstIP, dstPort, err := parseOrigDstAddr(oob[:oobn])
		if err != nil {
			log.Printf("proxy: udp tproxy: failed to parse orig dst: %v", err)
			continue
		}

		// Copy the data.
		data := make([]byte, n)
		copy(data, buf[:n])

		key := udpFlowKey{
			srcIP:   srcIP,
			srcPort: srcPort,
			dstIP:   dstIP,
			dstPort: dstPort,
		}

		mu.Lock()
		flow, exists := flows[key]
		if exists {
			flow.lastSeen = time.Now()
		}
		mu.Unlock()

		if !exists {
			// Determine address family.
			family := 2 // AF_INET
			if ip := net.ParseIP(dstIP); ip != nil && ip.To4() == nil {
				family = 10 // AF_INET6
			}

			ch, err := client.OpenUDP(family)
			if err != nil {
				log.Printf("proxy: udp tproxy: open UDP channel: %v", err)
				continue
			}

			srcStr := fmt.Sprintf("%s:%d", srcIP, srcPort)
			dstStr := fmt.Sprintf("%s:%d", dstIP, dstPort)
			srcAddr := &net.UDPAddr{IP: net.ParseIP(srcIP), Port: srcPort}
			flow = &udpFlow{
				ch:       ch,
				lastSeen: time.Now(),
				srcAddr:  srcAddr,
				srcStr:   srcStr,
				dstStr:   dstStr,
			}

			log.Printf("c : Accept UDP: %s:%d -> %s:%d", srcIP, srcPort, dstIP, dstPort)
			if counters != nil {
				flow.connID = counters.ConnOpen(srcStr, dstStr, "", 0)
			}

			mu.Lock()
			flows[key] = flow
			mu.Unlock()

			// Start receiver goroutine for this flow.
			go udpFlowReceiver(fd, ch, srcAddr, dstIP, dstPort, &mu, flows, key, flow.connID, srcStr, dstStr, counters)
		}

		// Forward data to remote.
		if err := flow.ch.SendTo(dstIP, dstPort, data); err != nil {
			log.Printf("proxy: udp tproxy: sendto: %v", err)
		}
	}
}

// udpFlowReceiver reads datagrams from the mux channel and sends them back
// to the original client using the TPROXY socket.
func udpFlowReceiver(fd int, ch *mux.UDPChannel, srcAddr *net.UDPAddr, origDstIP string, origDstPort int, mu *sync.Mutex, flows map[udpFlowKey]*udpFlow, key udpFlowKey, connID uint64, srcStr, dstStr string, counters *stats.Counters) {
	defer func() {
		ch.Close()
		if counters != nil && connID != 0 {
			counters.ConnClose(connID, srcStr, dstStr)
		}
		mu.Lock()
		delete(flows, key)
		mu.Unlock()
	}()

	for {
		dg, err := ch.Recv()
		if err != nil {
			return
		}

		// Send the reply back to the client.
		// We need to send from the original destination address (spoofed via TPROXY).
		// Create a new socket bound to the original destination to send the reply.
		replyFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
		if err != nil {
			log.Printf("proxy: udp tproxy: reply socket: %v", err)
			return
		}
		syscall.SetsockoptInt(replyFd, syscall.IPPROTO_IP, syscall.IP_TRANSPARENT, 1)
		syscall.SetsockoptInt(replyFd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)

		// Bind to the original destination so the reply appears to come from there.
		origIP := net.ParseIP(origDstIP).To4()
		if origIP == nil {
			syscall.Close(replyFd)
			continue
		}
		var bindAddr syscall.SockaddrInet4
		copy(bindAddr.Addr[:], origIP)
		bindAddr.Port = origDstPort
		if err := syscall.Bind(replyFd, &bindAddr); err != nil {
			syscall.Close(replyFd)
			log.Printf("proxy: udp tproxy: bind reply: %v", err)
			continue
		}

		// Send to the original client.
		clientIP := srcAddr.IP.To4()
		if clientIP == nil {
			syscall.Close(replyFd)
			continue
		}
		var dstSA syscall.SockaddrInet4
		copy(dstSA.Addr[:], clientIP)
		dstSA.Port = srcAddr.Port
		syscall.Sendto(replyFd, dg.Data, 0, &dstSA)
		syscall.Close(replyFd)
	}
}

// parseOrigDstAddr extracts the original destination address from the
// ancillary data returned by recvmsg with IP_RECVORIGDSTADDR enabled.
func parseOrigDstAddr(oob []byte) (string, int, error) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return "", 0, fmt.Errorf("parse control message: %w", err)
	}
	for _, msg := range msgs {
		// IP_RECVORIGDSTADDR returns a sockaddr_in in cmsg data.
		// Level: SOL_IP (0), Type: IP_RECVORIGDSTADDR (20).
		if msg.Header.Level == syscall.IPPROTO_IP && msg.Header.Type == ipRecvOrigDstAddr {
			if len(msg.Data) < 8 {
				continue
			}
			// sockaddr_in layout: family(2) + port(2) + addr(4)
			port := int(binary.BigEndian.Uint16(msg.Data[2:4]))
			ip := net.IP(msg.Data[4:8]).String()
			return ip, port, nil
		}
	}
	return "", 0, fmt.Errorf("IP_RECVORIGDSTADDR not found in ancillary data")
}
