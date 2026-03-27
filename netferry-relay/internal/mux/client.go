package mux

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoveychen/netferry/relay/internal/stats"
	"github.com/xtaci/smux"
)

// MuxClient runs the client-side mux over a smux session.
// Each SSH stdin/stdout pair becomes one smux session.
// TCP, DNS, and UDP requests each open a new smux stream.
type MuxClient struct {
	session  *smux.Session
	counters *stats.Counters
	routesCh chan []string
	done     atomic.Bool
}

// rwConn adapts separate io.Reader / io.Writer into the io.ReadWriteCloser
// that smux requires.
type rwConn struct {
	r io.Reader
	w io.Writer
}

func (c *rwConn) Read(b []byte) (int, error)  { return c.r.Read(b) }
func (c *rwConn) Write(b []byte) (int, error) { return c.w.Write(b) }
func (c *rwConn) Close() error                { return nil }

func smuxClientConfig() *smux.Config {
	cfg := smux.DefaultConfig()
	cfg.KeepAliveInterval = KEEPALIVE_INTERVAL
	cfg.KeepAliveTimeout = KEEPALIVE_INTERVAL + KEEPALIVE_TIMEOUT
	return cfg
}

// NewMuxClient creates a client. Call Run() in a goroutine.
func NewMuxClient(r io.Reader, w io.Writer) *MuxClient {
	sess, err := smux.Client(&rwConn{r: r, w: w}, smuxClientConfig())
	if err != nil {
		// smux.Client only errors if config is invalid; this won't happen.
		panic(fmt.Sprintf("mux: smux.Client: %v", err))
	}
	return &MuxClient{
		session:  sess,
		routesCh: make(chan []string, 1),
	}
}

// SetCounters attaches stats counters. Must be called before Run().
func (c *MuxClient) SetCounters(ct *stats.Counters) { c.counters = ct }

// SetFlowControl is a no-op: smux manages flow control internally.
func (c *MuxClient) SetFlowControl(_ bool, _ int64) {}

// RoutesCh returns the channel on which the server-pushed route list arrives.
func (c *MuxClient) RoutesCh() <-chan []string { return c.routesCh }

// Run accepts server-initiated streams (used for route pushes) and blocks
// until the session dies.
func (c *MuxClient) Run() error {
	defer c.done.Store(true)
	for {
		stream, err := c.session.AcceptStream()
		if err != nil {
			return err
		}
		go c.handleServerStream(stream)
	}
}

// handleServerStream processes a stream opened by the server (currently only ROUTES).
func (c *MuxClient) handleServerStream(stream *smux.Stream) {
	defer stream.Close()
	br := bufio.NewReader(stream)
	hdr, err := br.ReadString('\n')
	if err != nil {
		return
	}
	hdr = strings.TrimRight(hdr, "\n")
	switch hdr {
	case "ROUTES":
		data, _ := io.ReadAll(br)
		routes := parseRoutes(data)
		select {
		case c.routesCh <- routes:
		default:
		}
	default:
		log.Printf("mux: unknown server-pushed stream type: %q", hdr)
	}
}

// OpenTCP opens a new smux stream for a TCP connection to dstIP:dstPort.
// family: 2=IPv4, 10=IPv6.
func (c *MuxClient) OpenTCP(family int, dstIP string, dstPort int) (*ClientConn, error) {
	if c.done.Load() {
		return nil, fmt.Errorf("mux: client closed")
	}
	stream, err := c.session.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("mux: open stream: %w", err)
	}
	hdr := fmt.Sprintf("TCP %d %s %d\n", family, dstIP, dstPort)
	if _, err := stream.Write([]byte(hdr)); err != nil {
		stream.Close()
		return nil, fmt.Errorf("mux: write TCP header: %w", err)
	}
	if c.counters != nil {
		active := c.counters.ActiveTCP.Add(1)
		c.counters.TotalTCP.Add(1)
		c.counters.NoteActiveConns(active)
	}
	return &ClientConn{stream: stream, counters: c.counters}, nil
}

// DNSRequest opens a stream, sends a DNS query, and returns the response.
func (c *MuxClient) DNSRequest(data []byte) ([]byte, error) {
	if c.done.Load() {
		return nil, fmt.Errorf("mux: client closed")
	}
	stream, err := c.session.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("mux: open stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(30 * time.Second))

	// Header + length-prefixed query in one write.
	hdr := "DNS\n"
	msg := make([]byte, len(hdr)+2+len(data))
	copy(msg, hdr)
	binary.BigEndian.PutUint16(msg[len(hdr):], uint16(len(data)))
	copy(msg[len(hdr)+2:], data)
	if _, err := stream.Write(msg); err != nil {
		return nil, fmt.Errorf("mux: dns write: %w", err)
	}
	return readMsg(stream)
}

// OpenUDP opens a smux stream for UDP datagram forwarding.
func (c *MuxClient) OpenUDP(family int) (*UDPChannel, error) {
	if c.done.Load() {
		return nil, fmt.Errorf("mux: client closed")
	}
	stream, err := c.session.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("mux: open stream: %w", err)
	}
	hdr := fmt.Sprintf("UDP %d\n", family)
	if _, err := stream.Write([]byte(hdr)); err != nil {
		stream.Close()
		return nil, fmt.Errorf("mux: write UDP header: %w", err)
	}
	if c.counters != nil {
		active := c.counters.ActiveTCP.Add(1)
		c.counters.NoteActiveConns(active)
	}
	return &UDPChannel{stream: stream, client: c}, nil
}

// ── ClientConn ────────────────────────────────────────────────────────────────

// ClientConn implements net.Conn over a smux stream.
//
// Wire framing within the stream:
//   - Data frame:   [uint16 BE length > 0][payload bytes]
//   - Half-close:   [uint16 BE length == 0]  (maps to CloseWrite)
type ClientConn struct {
	stream   *smux.Stream
	counters *stats.Counters

	readBuf []byte     // leftover bytes from the last data frame
	readEOF bool       // received half-close from remote
	closed  sync.Once
	done    atomic.Bool
}

func (cc *ClientConn) Read(b []byte) (int, error) {
	if cc.readEOF {
		return 0, io.EOF
	}
	for len(cc.readBuf) == 0 {
		payload, err := readMsg(cc.stream)
		if err != nil {
			return 0, err
		}
		if len(payload) == 0 {
			cc.readEOF = true
			return 0, io.EOF
		}
		cc.readBuf = payload
	}
	n := copy(b, cc.readBuf)
	cc.readBuf = cc.readBuf[n:]
	if cc.counters != nil && n > 0 {
		cc.counters.AddRx(int64(n))
	}
	return n, nil
}

func (cc *ClientConn) Write(b []byte) (int, error) {
	if cc.done.Load() {
		return 0, net.ErrClosed
	}
	total := 0
	for len(b) > 0 {
		chunk := b
		if len(chunk) > BUF_SIZE {
			chunk = chunk[:BUF_SIZE]
		}
		if err := writeMsg(cc.stream, chunk); err != nil {
			return total, err
		}
		total += len(chunk)
		b = b[len(chunk):]
	}
	if cc.counters != nil && total > 0 {
		cc.counters.AddTx(int64(total))
	}
	return total, nil
}

// CloseWrite sends a zero-length frame to signal half-close (EOF) to the
// remote without closing the stream for reading.
func (cc *ClientConn) CloseWrite() error {
	if cc.done.Load() {
		return net.ErrClosed
	}
	return writeMsg(cc.stream, nil)
}

func (cc *ClientConn) Close() error {
	cc.closed.Do(func() {
		cc.done.Store(true)
		// Best-effort half-close so server can drain its write side.
		writeMsg(cc.stream, nil)
		cc.stream.Close()
		if cc.counters != nil {
			active := cc.counters.ActiveTCP.Add(-1)
			cc.counters.NoteActiveConns(active)
		}
	})
	return nil
}

func (cc *ClientConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (cc *ClientConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (cc *ClientConn) SetDeadline(t time.Time) error      { return cc.stream.SetDeadline(t) }
func (cc *ClientConn) SetReadDeadline(t time.Time) error  { return cc.stream.SetReadDeadline(t) }
func (cc *ClientConn) SetWriteDeadline(t time.Time) error { return cc.stream.SetWriteDeadline(t) }

// ── UDPChannel ────────────────────────────────────────────────────────────────

// UDPDatagram represents a single UDP datagram with its remote address.
type UDPDatagram struct {
	IP   string
	Port int
	Data []byte
}

// UDPChannel wraps a smux stream for UDP datagram forwarding.
// Each datagram is length-prefixed; len=0 signals close.
// Wire format per datagram payload: "ip,port,<raw bytes>"
type UDPChannel struct {
	stream *smux.Stream
	client *MuxClient
	closed sync.Once
}

func (uc *UDPChannel) SendTo(dstIP string, dstPort int, data []byte) error {
	hdr := fmt.Sprintf("%s,%d,", dstIP, dstPort)
	payload := make([]byte, len(hdr)+len(data))
	copy(payload, hdr)
	copy(payload[len(hdr):], data)
	if err := writeMsg(uc.stream, payload); err != nil {
		return err
	}
	if uc.client.counters != nil {
		uc.client.counters.AddTx(int64(len(data)))
	}
	return nil
}

func (uc *UDPChannel) Recv() (UDPDatagram, error) {
	payload, err := readMsg(uc.stream)
	if err != nil {
		return UDPDatagram{}, err
	}
	if len(payload) == 0 {
		return UDPDatagram{}, fmt.Errorf("mux: udp channel closed by remote")
	}
	s := string(payload)
	i1 := indexByte(s, ',')
	if i1 < 0 {
		return UDPDatagram{}, fmt.Errorf("mux: bad udp datagram")
	}
	i2 := indexByte(s[i1+1:], ',') + i1 + 1
	if i2 <= i1 {
		return UDPDatagram{}, fmt.Errorf("mux: bad udp datagram")
	}
	ip := s[:i1]
	var port int
	fmt.Sscanf(s[i1+1:i2], "%d", &port)
	data := payload[i2+1:]
	if uc.client.counters != nil {
		uc.client.counters.AddRx(int64(len(data)))
	}
	return UDPDatagram{IP: ip, Port: port, Data: data}, nil
}

func (uc *UDPChannel) Close() error {
	uc.closed.Do(func() {
		writeMsg(uc.stream, nil) // signal close to server
		uc.stream.Close()
		if uc.client.counters != nil {
			active := uc.client.counters.ActiveTCP.Add(-1)
			uc.client.counters.NoteActiveConns(active)
		}
	})
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func parseRoutes(data []byte) []string {
	var routes []string
	s := string(data)
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			start = i + 1
			if line == "" {
				continue
			}
			parts := splitComma(line, 3)
			if len(parts) == 3 {
				var width int
				fmt.Sscanf(parts[2], "%d", &width)
				routes = append(routes, fmt.Sprintf("%s/%d", parts[1], width))
			}
		}
	}
	return routes
}

func splitComma(s string, n int) []string {
	out := make([]string, 0, n)
	start := 0
	for i := 0; i < len(s) && len(out) < n-1; i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
