package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/sockmark"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// resolveCacheTTL bounds how long a hostname→IP answer is reused for UDP
// datagrams, so a busy flow does not issue one DNS query per packet.
const resolveCacheTTL = 60 * time.Second

// handleSOCKS5UDP serves one UDP ASSOCIATE (RFC 1928 §7): a relay socket on
// the interface the client reached us through, alive as long as the control
// TCP connection stays open. Datagrams follow the same route rules as TCP.
func handleSOCKS5UDP(conn net.Conn, client mux.TunnelClient, counters *stats.Counters) {
	localIP := conn.LocalAddr().(*net.TCPAddr).IP
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: localIP})
	if err != nil {
		log.Printf("socks5: udp associate listen: %v", err)
		sendSOCKS5Reply(conn, socks5ReplyFail)
		return
	}
	defer pc.Close()
	sendSOCKS5BoundReply(conn, pc.LocalAddr().(*net.UDPAddr))

	r := &udpRelay{
		pc:       pc,
		client:   client,
		counters: counters,
		clientIP: conn.RemoteAddr().(*net.TCPAddr).IP,
		tunnels:  make(map[string]*mux.UDPChannel),
		resolved: make(map[string]resolvedIP),
	}
	log.Printf("socks5: udp associate %s via %s", conn.RemoteAddr(), pc.LocalAddr())
	go r.readLoop()
	io.Copy(io.Discard, conn) // blocks until the client drops the association
	r.close()
}

type resolvedIP struct {
	ip  net.IP
	exp time.Time
}

type udpRelay struct {
	pc       *net.UDPConn
	client   mux.TunnelClient
	counters *stats.Counters
	clientIP net.IP

	mu         sync.Mutex
	clientAddr *net.UDPAddr               // learned from the first datagram
	tunnels    map[string]*mux.UDPChannel // key: profileID/family
	direct     *net.UDPConn
	resolved   map[string]resolvedIP
	closed     bool
}

func (r *udpRelay) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for _, ch := range r.tunnels {
		ch.Close()
	}
	if r.direct != nil {
		r.direct.Close()
	}
}

func (r *udpRelay) readLoop() {
	buf := make([]byte, 65535)
	for {
		n, from, err := r.pc.ReadFromUDP(buf)
		if err != nil {
			return
		}
		// Only the client that opened the association may use the relay.
		if !from.IP.Equal(r.clientIP) {
			continue
		}
		r.mu.Lock()
		r.clientAddr = from
		r.mu.Unlock()

		host, port, payload, err := parseSOCKS5UDP(buf[:n])
		if err != nil {
			log.Printf("socks5: udp: %v", err)
			continue
		}
		r.forward(host, port, append([]byte(nil), payload...))
	}
}

func (r *udpRelay) forward(host string, port int, payload []byte) {
	dstAddr := net.JoinHostPort(host, strconv.Itoa(port))
	name := ""
	if net.ParseIP(host) == nil {
		name = host
	}
	routeKind := stats.RouteTunnel
	if r.counters != nil {
		routeKind = r.counters.LookupRouteMode(dstAddr, name).Kind
	}

	switch routeKind {
	case stats.RouteBlocked:
		return
	case stats.RouteDirect:
		ip, err := r.resolve(net.DefaultResolver, "direct", host)
		if err != nil {
			log.Printf("socks5: udp resolve %s: %v", host, err)
			return
		}
		d, err := r.directConn()
		if err != nil {
			log.Printf("socks5: udp direct: %v", err)
			return
		}
		d.WriteToUDP(payload, &net.UDPAddr{IP: ip, Port: port})
		return
	}

	dispatch := r.client
	profileID := ""
	if sm, ok := r.client.(*mux.SessionManager); ok {
		if id, pool := sm.PoolFor(dstAddr, name); pool != nil {
			dispatch, profileID = pool, id
		}
	}
	ip, err := r.resolve(tunnelResolver(dispatch), profileID, host)
	if err != nil {
		log.Printf("socks5: udp resolve %s via tunnel: %v", host, err)
		return
	}
	family := 2 // AF_INET
	if ip.To4() == nil {
		family = 10 // AF_INET6
	}
	ch, err := r.tunnelChannel(dispatch, profileID, family)
	if err != nil {
		log.Printf("socks5: udp open channel: %v", err)
		return
	}
	if err := ch.SendTo(ip.String(), port, payload); err != nil {
		log.Printf("socks5: udp sendto %s: %v", dstAddr, err)
	}
}

// resolve maps host to an IP, caching answers per resolver scope.
func (r *udpRelay) resolve(res *net.Resolver, scope, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	key := scope + "/" + host
	r.mu.Lock()
	if e, ok := r.resolved[key]; ok && time.Now().Before(e.exp) {
		r.mu.Unlock()
		return e.ip, nil
	}
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addrs, err := res.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no addresses")
	}
	ip := addrs[0].IP
	for _, a := range addrs { // prefer IPv4, like the TCP path's servers
		if a.IP.To4() != nil {
			ip = a.IP
			break
		}
	}
	r.mu.Lock()
	r.resolved[key] = resolvedIP{ip: ip, exp: time.Now().Add(resolveCacheTTL)}
	r.mu.Unlock()
	return ip, nil
}

func (r *udpRelay) tunnelChannel(dispatch mux.TunnelClient, profileID string, family int) (*mux.UDPChannel, error) {
	key := fmt.Sprintf("%s/%d", profileID, family)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, net.ErrClosed
	}
	if ch, ok := r.tunnels[key]; ok {
		return ch, nil
	}
	ch, err := dispatch.OpenUDP(family)
	if err != nil {
		return nil, err
	}
	r.tunnels[key] = ch
	go func() {
		for {
			dg, err := ch.Recv()
			if err != nil {
				r.mu.Lock()
				if r.tunnels[key] == ch {
					delete(r.tunnels, key)
				}
				r.mu.Unlock()
				return
			}
			r.reply(net.ParseIP(dg.IP), dg.Port, dg.Data)
		}
	}()
	return ch, nil
}

func (r *udpRelay) directConn() (*net.UDPConn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, net.ErrClosed
	}
	if r.direct != nil {
		return r.direct, nil
	}
	d, err := sockmark.ListenUDP()
	if err != nil {
		return nil, err
	}
	r.direct = d
	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := d.ReadFromUDP(buf)
			if err != nil {
				return
			}
			r.reply(from.IP, from.Port, buf[:n])
		}
	}()
	return d, nil
}

// reply wraps a datagram from srcIP:srcPort in a SOCKS5 UDP header and sends
// it back to the client.
func (r *udpRelay) reply(srcIP net.IP, srcPort int, data []byte) {
	r.mu.Lock()
	to := r.clientAddr
	r.mu.Unlock()
	if to == nil || srcIP == nil {
		return
	}
	hdr := []byte{0, 0, 0}
	if ip4 := srcIP.To4(); ip4 != nil {
		hdr = append(append(hdr, socks5AddrIPv4), ip4...)
	} else {
		hdr = append(append(hdr, socks5AddrIPv6), srcIP.To16()...)
	}
	hdr = binary.BigEndian.AppendUint16(hdr, uint16(srcPort))
	r.pc.WriteToUDP(append(hdr, data...), to)
}

// parseSOCKS5UDP splits a client datagram: RSV(2) FRAG ATYP DST.ADDR DST.PORT DATA.
func parseSOCKS5UDP(b []byte) (host string, port int, payload []byte, err error) {
	if len(b) < 4 {
		return "", 0, nil, fmt.Errorf("short datagram")
	}
	if b[2] != 0 {
		return "", 0, nil, fmt.Errorf("fragmented datagram not supported")
	}
	rest := b[4:]
	switch b[3] {
	case socks5AddrIPv4:
		if len(rest) < 4+2 {
			return "", 0, nil, fmt.Errorf("short datagram")
		}
		host, rest = net.IP(rest[:4]).String(), rest[4:]
	case socks5AddrIPv6:
		if len(rest) < 16+2 {
			return "", 0, nil, fmt.Errorf("short datagram")
		}
		host, rest = net.IP(rest[:16]).String(), rest[16:]
	case socks5AddrDomain:
		if len(rest) < 1 || len(rest) < 1+int(rest[0])+2 {
			return "", 0, nil, fmt.Errorf("short datagram")
		}
		host, rest = string(rest[1:1+int(rest[0])]), rest[1+int(rest[0]):]
	default:
		return "", 0, nil, fmt.Errorf("unsupported address type %d", b[3])
	}
	return host, int(binary.BigEndian.Uint16(rest[:2])), rest[2:], nil
}

// sendSOCKS5BoundReply sends a success reply carrying the relay address.
func sendSOCKS5BoundReply(conn net.Conn, addr *net.UDPAddr) {
	reply := []byte{socks5Version, socks5ReplyOK, 0}
	if ip4 := addr.IP.To4(); ip4 != nil {
		reply = append(append(reply, socks5AddrIPv4), ip4...)
	} else {
		reply = append(append(reply, socks5AddrIPv6), addr.IP.To16()...)
	}
	reply = binary.BigEndian.AppendUint16(reply, uint16(addr.Port))
	conn.Write(reply)
}
