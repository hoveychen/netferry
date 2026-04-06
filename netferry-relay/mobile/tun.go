package mobile

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const tunNICID = 1

// tunForwarder reads IP packets from a TUN fd and forwards TCP/DNS through
// the mux tunnel using gVisor's userspace TCP/IP stack.
//
// This is used on Android where VpnService provides a TUN fd. On iOS the
// SOCKS5 proxy approach is used instead (via NEProxySettings).
type tunForwarder struct {
	s        *stack.Stack
	ep       *channel.Endpoint
	tunFile  *os.File
	tunnel   mux.TunnelClient
	counters *stats.Counters
	mtu      int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newTunForwarder(tunFD int32, mtuSize int, tunnel mux.TunnelClient, counters *stats.Counters) (*tunForwarder, error) {
	tunFile := os.NewFile(uintptr(tunFD), "tun")
	if tunFile == nil {
		return nil, io.ErrClosedPipe
	}

	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})

	ep := channel.New(512, uint32(mtuSize), "")

	if tcpipErr := s.CreateNIC(tunNICID, ep); tcpipErr != nil {
		tunFile.Close()
		return nil, fmt.Errorf("create NIC: %v", tcpipErr)
	}

	s.SetPromiscuousMode(tunNICID, true)
	s.SetSpoofing(tunNICID, true)
	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: tunNICID},
		{Destination: header.IPv6EmptySubnet, NIC: tunNICID},
	})

	// Tune TCP buffer sizes for mobile.
	{
		opt := tcpip.TCPReceiveBufferSizeRangeOption{Min: 4096, Default: 212992, Max: 4 * 1024 * 1024}
		s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt)
	}
	{
		opt := tcpip.TCPSendBufferSizeRangeOption{Min: 4096, Default: 212992, Max: 4 * 1024 * 1024}
		s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt)
	}

	ctx, cancel := context.WithCancel(context.Background())
	tf := &tunForwarder{
		s: s, ep: ep, tunFile: tunFile,
		tunnel: tunnel, counters: counters,
		mtu: mtuSize, ctx: ctx, cancel: cancel,
	}

	// TCP forwarder: intercept all incoming TCP connections.
	tcpFwd := tcp.NewForwarder(s, 0, 4096, tf.handleTCP)
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	// UDP forwarder: intercept all incoming UDP packets (DNS).
	udpFwd := udp.NewForwarder(s, tf.handleUDP)
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	// TUN fd ↔ netstack packet exchange loops.
	tf.wg.Add(2)
	go tf.readFromTUN()
	go tf.writeToTUN()

	return tf, nil
}

// readFromTUN reads IP packets from the TUN fd and injects into netstack.
func (tf *tunForwarder) readFromTUN() {
	defer tf.wg.Done()
	buf := make([]byte, tf.mtu+64)
	for {
		n, err := tf.tunFile.Read(buf)
		if err != nil {
			select {
			case <-tf.ctx.Done():
			default:
				log.Printf("tun read: %v", err)
			}
			return
		}
		if n == 0 {
			continue
		}

		var proto tcpip.NetworkProtocolNumber
		switch buf[0] >> 4 {
		case 4:
			proto = header.IPv4ProtocolNumber
		case 6:
			proto = header.IPv6ProtocolNumber
		default:
			continue
		}

		pktData := make([]byte, n)
		copy(pktData, buf[:n])
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
			Payload: buffer.MakeWithData(pktData),
		})
		tf.ep.InjectInbound(proto, pkt)
		pkt.DecRef()
	}
}

// writeToTUN reads outgoing packets from netstack and writes to the TUN fd.
func (tf *tunForwarder) writeToTUN() {
	defer tf.wg.Done()
	for {
		pkt := tf.ep.ReadContext(tf.ctx)
		if pkt == nil {
			return
		}
		view := pkt.ToView()
		if _, err := tf.tunFile.Write(view.AsSlice()); err != nil {
			select {
			case <-tf.ctx.Done():
			default:
				log.Printf("tun write: %v", err)
			}
			pkt.DecRef()
			return
		}
		pkt.DecRef()
	}
}

// handleTCP proxies each intercepted TCP connection through the mux tunnel.
func (tf *tunForwarder) handleTCP(r *tcp.ForwarderRequest) {
	id := r.ID()
	dstIP := id.LocalAddress.String()
	dstPort := int(id.LocalPort)

	family := 2 // AF_INET
	if id.LocalAddress.Len() == header.IPv6AddressSize {
		family = 10 // AF_INET6
	}

	var wq waiter.Queue
	ep, epErr := r.CreateEndpoint(&wq)
	if epErr != nil {
		r.Complete(true) // RST
		return
	}
	r.Complete(false) // ACK

	localConn := gonet.NewTCPConn(&wq, ep)
	defer localConn.Close()

	// Open mux channel directly (bypasses SOCKS5 — more efficient).
	muxConn, err := tf.tunnel.OpenTCP(family, dstIP, dstPort, stats.DefaultPriority)
	if err != nil {
		log.Printf("tun: mux open %s:%d: %v", dstIP, dstPort, err)
		return
	}
	defer muxConn.Close()

	if tf.counters != nil {
		tf.counters.ActiveTCP.Add(1)
		tf.counters.TotalTCP.Add(1)
		defer tf.counters.ActiveTCP.Add(-1)
	}

	// Bidirectional copy.
	const idleTimeout = 2 * time.Minute
	done := make(chan struct{}, 2)

	go func() {
		copyWithStats(muxConn, localConn, idleTimeout, func(n int) {
			if tf.counters != nil {
				tf.counters.AddTx(int64(n))
			}
		})
		muxConn.CloseWrite()
		done <- struct{}{}
	}()

	go func() {
		copyWithStats(localConn, muxConn, idleTimeout, func(n int) {
			if tf.counters != nil {
				tf.counters.AddRx(int64(n))
			}
		})
		done <- struct{}{}
	}()

	<-done
	<-done
}

// handleUDP intercepts UDP packets. Only DNS (port 53) is forwarded.
func (tf *tunForwarder) handleUDP(r *udp.ForwarderRequest) {
	id := r.ID()
	if id.LocalPort != 53 {
		return // drop non-DNS UDP
	}

	var wq waiter.Queue
	ep, epErr := r.CreateEndpoint(&wq)
	if epErr != nil {
		return
	}
	conn := gonet.NewUDPConn(&wq, ep)
	defer conn.Close()

	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, srcAddr, err := conn.ReadFrom(buf)
	if err != nil {
		return
	}

	if tf.counters != nil {
		tf.counters.AddDNS()
	}

	resp, err := tf.tunnel.DNSRequest(buf[:n])
	if err != nil {
		log.Printf("tun: dns: %v", err)
		return
	}
	conn.WriteTo(resp, srcAddr)
}

func (tf *tunForwarder) Close() {
	tf.cancel()
	tf.tunFile.Close()
	tf.ep.Close()
	tf.s.Close()
	tf.wg.Wait()
}

// copyWithStats copies from src to dst with idle timeout and byte counting.
func copyWithStats(dst net.Conn, src net.Conn, idle time.Duration, onWrite func(int)) {
	buf := make([]byte, 32*1024)
	for {
		src.SetReadDeadline(time.Now().Add(idle))
		n, err := src.Read(buf)
		if n > 0 {
			if _, wErr := dst.Write(buf[:n]); wErr != nil {
				return
			}
			if onWrite != nil {
				onWrite(n)
			}
		}
		if err != nil {
			return
		}
	}
}
