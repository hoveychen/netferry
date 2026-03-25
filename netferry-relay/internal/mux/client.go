package mux

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoveychen/netferry/relay/internal/stats"
)

// MuxClient runs the client-side mux loop.
// It connects to the remote MuxServer over an SSH session's stdin/stdout.
type MuxClient struct {
	r io.Reader
	w io.Writer

	mu       sync.Mutex
	channels map[uint16]*clientChan
	nextChan uint16

	out         chan Frame
	priorityOut chan Frame // PING/PONG/DNS/WINDOW_UPDATE bypass bulk data
	err         chan error
	routesCh    chan []string

	done      atomic.Bool
	lastPong  atomic.Int64 // UnixNano of last PONG received
	counters  *stats.Counters // optional; nil = no-op

	flowControl   bool
	initialWindow int64
}

// clientChan holds per-channel state on the client.
type clientChan struct {
	inbox *asyncInbox
	sw    *sendWindow // nil when flow control is off
}

// NewMuxClient creates a client. Call Run() in a goroutine.
func NewMuxClient(r io.Reader, w io.Writer) *MuxClient {
	return &MuxClient{
		r:           r,
		w:           w,
		channels:    make(map[uint16]*clientChan),
		nextChan:    0,
		out:         make(chan Frame, MUX_OUT_BUF),
		priorityOut: make(chan Frame, PRIORITY_OUT_BUF),
		err:         make(chan error, 2),
		routesCh:    make(chan []string, 1),
	}
}

// SetFlowControl enables per-channel sliding window flow control.
// Must be called before Run(). Both client and server must have the
// same setting, controlled via the --flow-control flag.
func (c *MuxClient) SetFlowControl(enabled bool, initialWindow int64) {
	c.flowControl = enabled
	c.initialWindow = initialWindow
}

// SetCounters attaches a stats.Counters to this client so that byte and
// connection metrics are collected. Must be called before Run().
func (c *MuxClient) SetCounters(ct *stats.Counters) {
	c.counters = ct
}

// RoutesCh returns the channel on which CMD_ROUTES will be delivered (once).
func (c *MuxClient) RoutesCh() <-chan []string {
	return c.routesCh
}

// Run starts the mux client loops. Blocks until the connection dies.
func (c *MuxClient) Run() error {
	c.lastPong.Store(time.Now().UnixNano())
	go c.writer()
	go c.reader()
	go c.keepalive()
	err := <-c.err
	c.done.Store(true)
	return err
}

// OpenTCP sends CMD_TCP_CONNECT and returns a net.Conn-like channel.
// family: net.AF_INET (2) or net.AF_INET6 (10).
func (c *MuxClient) OpenTCP(family int, dstIP string, dstPort int) (*ClientConn, error) {
	if c.done.Load() {
		return nil, fmt.Errorf("mux: client closed")
	}
	ch, inbox := c.allocChannel()
	data := []byte(fmt.Sprintf("%d,%s,%d", family, dstIP, dstPort))
	c.out <- Frame{Channel: ch, Cmd: CMD_TCP_CONNECT, Data: data}
	if c.counters != nil {
		c.counters.ActiveTCP.Add(1)
		c.counters.TotalTCP.Add(1)
	}
	return &ClientConn{client: c, channel: ch, inbox: inbox, counters: c.counters}, nil
}

// DNSRequest sends a DNS query and blocks until the response arrives (or timeout).
func (c *MuxClient) DNSRequest(data []byte) ([]byte, error) {
	if c.done.Load() {
		return nil, fmt.Errorf("mux: client closed")
	}
	ch, inbox := c.allocChannel()
	defer c.freeChannel(ch)

	c.priorityOut <- Frame{Channel: ch, Cmd: CMD_DNS_REQ, Data: data}

	select {
	case f, ok := <-inbox:
		if !ok {
			return nil, fmt.Errorf("mux: dns channel closed")
		}
		if f.Cmd == CMD_DNS_RESPONSE {
			return f.Data, nil
		}
		return nil, fmt.Errorf("mux: unexpected dns cmd %04x", f.Cmd)
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("mux: dns timeout")
	}
}


func (c *MuxClient) allocChannel() (uint16, <-chan Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := 0; i < MAX_CHAN; i++ {
		c.nextChan++
		if c.nextChan == 0 {
			c.nextChan = 1
		}
		if _, used := c.channels[c.nextChan]; !used {
			inbox := newAsyncInbox()
			var sw *sendWindow
			if c.flowControl {
				sw = newSendWindow(c.initialWindow)
			}
			c.channels[c.nextChan] = &clientChan{inbox: inbox, sw: sw}
			return c.nextChan, inbox.C()
		}
	}
	panic("mux: all channels exhausted")
}

func (c *MuxClient) freeChannel(ch uint16) {
	c.mu.Lock()
	cc, ok := c.channels[ch]
	if ok {
		delete(c.channels, ch)
	}
	c.mu.Unlock()
	if ok {
		if cc.sw != nil {
			cc.sw.Kill()
		}
		cc.inbox.Close()
	}
}

func (c *MuxClient) reader() {
	for {
		f, err := ReadFrame(c.r)
		if err != nil {
			c.err <- err
			return
		}
		c.dispatchIncoming(f)
	}
}

func (c *MuxClient) writer() {
	bw := NewBufferedWriter(c.w)
	for {
		// Block until at least one frame is available.
		select {
		case f, ok := <-c.priorityOut:
			if !ok {
				return
			}
			if err := WriteFrame(bw, f); err != nil {
				c.err <- err
				return
			}
		case f, ok := <-c.out:
			if !ok {
				return
			}
			if err := WriteFrame(bw, f); err != nil {
				c.err <- err
				return
			}
		}
		// Drain all queued frames (priority first) before flushing,
		// so multiple frames are coalesced into fewer system calls.
		if err := drainAndFlush(bw, c.priorityOut, c.out, c.err); err != nil {
			return
		}
	}
}

func (c *MuxClient) keepalive() {
	ticker := time.NewTicker(KEEPALIVE_INTERVAL)
	defer ticker.Stop()
	for range ticker.C {
		if c.done.Load() {
			return
		}
		last := time.Unix(0, c.lastPong.Load())
		if time.Since(last) > KEEPALIVE_INTERVAL+KEEPALIVE_TIMEOUT {
			c.err <- fmt.Errorf("mux: keepalive timeout (no pong for %v)", time.Since(last))
			return
		}
		select {
		case c.priorityOut <- Frame{Channel: 0, Cmd: CMD_PING}:
		default:
		}
	}
}

func (c *MuxClient) sendWindowUpdate(ch uint16, credit int64) {
	c.priorityOut <- Frame{Channel: ch, Cmd: CMD_WINDOW_UPDATE, Data: EncodeWindowUpdate(credit)}
}

func (c *MuxClient) dispatchIncoming(f Frame) {
	switch f.Cmd {
	case CMD_PING:
		c.priorityOut <- Frame{Channel: 0, Cmd: CMD_PONG, Data: f.Data}
	case CMD_PONG:
		c.lastPong.Store(time.Now().UnixNano())
	case CMD_ROUTES:
		routes := parseRoutes(f.Data)
		select {
		case c.routesCh <- routes:
		default:
		}
	case CMD_WINDOW_UPDATE:
		if c.flowControl {
			credit := DecodeWindowUpdate(f.Data)
			c.mu.Lock()
			cc, ok := c.channels[f.Channel]
			c.mu.Unlock()
			if ok && cc.sw != nil {
				cc.sw.Release(credit)
			}
		}
	default:
		c.mu.Lock()
		cc, ok := c.channels[f.Channel]
		c.mu.Unlock()
		if ok {
			cc.inbox.send(f)
		}
	}
}

func parseRoutes(data []byte) []string {
	// Format: "family,ip,width\nfamily,ip,width\n..."
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

// ClientConn implements net.Conn over a mux channel.
type ClientConn struct {
	client   *MuxClient
	channel  uint16
	inbox    <-chan Frame
	buf      []byte // leftover read buffer
	closed   sync.Once
	isClosed atomic.Bool
	counters *stats.Counters // may be nil

	// Flow control: accumulate consumed bytes and batch WINDOW_UPDATEs.
	rxPending int64
}

func (cc *ClientConn) Read(b []byte) (int, error) {
	for len(cc.buf) == 0 {
		f, ok := <-cc.inbox
		if !ok {
			return 0, io.EOF
		}
		switch f.Cmd {
		case CMD_TCP_DATA:
			cc.buf = f.Data
		case CMD_TCP_EOF:
			return 0, io.EOF
		case CMD_TCP_STOP_SENDING:
			return 0, io.EOF
		}
	}
	n := copy(b, cc.buf)
	cc.buf = cc.buf[n:]
	if cc.counters != nil && n > 0 {
		cc.counters.RxTotal.Add(int64(n))
	}
	// Send WINDOW_UPDATE back to the remote sender in batches.
	if cc.client.flowControl && n > 0 {
		cc.rxPending += int64(n)
		if cc.rxPending >= WINDOW_UPDATE_THRESHOLD {
			cc.client.sendWindowUpdate(cc.channel, cc.rxPending)
			cc.rxPending = 0
		}
	}
	return n, nil
}

func (cc *ClientConn) Write(b []byte) (int, error) {
	if cc.isClosed.Load() || cc.client.done.Load() {
		return 0, net.ErrClosed
	}
	total := 0
	for len(b) > 0 {
		chunk := b
		if len(chunk) > BUF_SIZE {
			chunk = chunk[:BUF_SIZE]
		}
		// Acquire send window before enqueuing the frame.
		if cc.client.flowControl {
			cc.client.mu.Lock()
			ch, ok := cc.client.channels[cc.channel]
			cc.client.mu.Unlock()
			if ok && ch.sw != nil {
				if !ch.sw.Acquire(len(chunk)) {
					return total, net.ErrClosed
				}
			}
		}
		d := make([]byte, len(chunk))
		copy(d, chunk)
		cc.client.out <- Frame{Channel: cc.channel, Cmd: CMD_TCP_DATA, Data: d}
		total += len(chunk)
		b = b[len(chunk):]
	}
	if cc.counters != nil && total > 0 {
		cc.counters.TxTotal.Add(int64(total))
	}
	return total, nil
}

func (cc *ClientConn) CloseWrite() error {
	if cc.isClosed.Load() {
		return net.ErrClosed
	}
	cc.client.out <- Frame{Channel: cc.channel, Cmd: CMD_TCP_EOF}
	return nil
}

func (cc *ClientConn) Close() error {
	cc.closed.Do(func() {
		cc.isClosed.Store(true)
		cc.client.out <- Frame{Channel: cc.channel, Cmd: CMD_TCP_EOF}
		cc.client.freeChannel(cc.channel)
		if cc.counters != nil {
			cc.counters.ActiveTCP.Add(-1)
		}
	})
	return nil
}

// net.Conn boilerplate — not used but required by interface.
func (cc *ClientConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (cc *ClientConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (cc *ClientConn) SetDeadline(t time.Time) error    { return nil }
func (cc *ClientConn) SetReadDeadline(t time.Time) error { return nil }
func (cc *ClientConn) SetWriteDeadline(t time.Time) error { return nil }

// UDPDatagram represents a single UDP datagram with its remote address.
type UDPDatagram struct {
	IP   string
	Port int
	Data []byte
}

// UDPChannel wraps a mux channel for UDP communication.
// The server creates a UDP socket and relays datagrams bidirectionally.
// Wire format for CMD_UDP_DATA: "ip,port,payload" (same as server).
type UDPChannel struct {
	client  *MuxClient
	channel uint16
	inbox   <-chan Frame
	closed  sync.Once
}

// OpenUDP sends CMD_UDP_OPEN and returns a UDPChannel for sending/receiving
// UDP datagrams via the remote server. family: 2=IPv4, 10=IPv6.
func (c *MuxClient) OpenUDP(family int) (*UDPChannel, error) {
	if c.done.Load() {
		return nil, fmt.Errorf("mux: client closed")
	}
	ch, inbox := c.allocChannel()
	data := []byte(fmt.Sprintf("%d", family))
	c.out <- Frame{Channel: ch, Cmd: CMD_UDP_OPEN, Data: data}
	if c.counters != nil {
		c.counters.ActiveTCP.Add(1) // reuse counter for active channels
	}
	return &UDPChannel{client: c, channel: ch, inbox: inbox}, nil
}

// SendTo sends a UDP datagram to the specified destination via the remote server.
func (uc *UDPChannel) SendTo(dstIP string, dstPort int, data []byte) error {
	if uc.client.done.Load() {
		return fmt.Errorf("mux: client closed")
	}
	hdr := fmt.Sprintf("%s,%d,", dstIP, dstPort)
	payload := append([]byte(hdr), data...)
	if uc.client.flowControl {
		uc.client.mu.Lock()
		ch, ok := uc.client.channels[uc.channel]
		uc.client.mu.Unlock()
		if ok && ch.sw != nil {
			if !ch.sw.Acquire(len(payload)) {
				return fmt.Errorf("mux: udp channel closed")
			}
		}
	}
	uc.client.out <- Frame{Channel: uc.channel, Cmd: CMD_UDP_DATA, Data: payload}
	if uc.client.counters != nil {
		uc.client.counters.TxTotal.Add(int64(len(data)))
	}
	return nil
}

// Recv blocks until a UDP datagram is received from the remote server, or the
// channel is closed. Returns the source address and payload.
func (uc *UDPChannel) Recv() (UDPDatagram, error) {
	for {
		f, ok := <-uc.inbox
		if !ok {
			return UDPDatagram{}, fmt.Errorf("mux: udp channel closed")
		}
		switch f.Cmd {
		case CMD_UDP_DATA:
			// Parse "srcIP,srcPort,payload"
			s := string(f.Data)
			i1 := indexByte(s, ',')
			if i1 < 0 {
				continue
			}
			i2 := indexByte(s[i1+1:], ',')
			if i2 < 0 {
				continue
			}
			i2 += i1 + 1
			ip := s[:i1]
			var port int
			fmt.Sscanf(s[i1+1:i2], "%d", &port)
			payload := f.Data[i2+1:]
			if uc.client.counters != nil {
				uc.client.counters.RxTotal.Add(int64(len(payload)))
			}
			if uc.client.flowControl {
				uc.client.sendWindowUpdate(uc.channel, int64(len(f.Data)))
			}
			return UDPDatagram{IP: ip, Port: port, Data: payload}, nil
		case CMD_UDP_CLOSE:
			return UDPDatagram{}, fmt.Errorf("mux: udp channel closed by remote")
		}
	}
}

// RecvCh returns the raw inbox channel for select-based usage.
func (uc *UDPChannel) RecvCh() <-chan Frame {
	return uc.inbox
}

// Close sends CMD_UDP_CLOSE and frees the channel.
func (uc *UDPChannel) Close() error {
	uc.closed.Do(func() {
		uc.client.out <- Frame{Channel: uc.channel, Cmd: CMD_UDP_CLOSE}
		uc.client.freeChannel(uc.channel)
		if uc.client.counters != nil {
			uc.client.counters.ActiveTCP.Add(-1)
		}
	})
	return nil
}

// indexByte returns the index of the first instance of c in s, or -1.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
