package mux

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// MuxClient runs the client-side mux loop.
// It connects to the remote MuxServer over an SSH session's stdin/stdout.
type MuxClient struct {
	r io.Reader
	w io.Writer

	mu       sync.Mutex
	channels map[uint16]*clientChan
	nextChan uint16

	out      chan Frame
	err      chan error
	routesCh chan []string

	done atomic.Bool
}

// clientChan holds per-channel state on the client.
type clientChan struct {
	inbox chan Frame
	once  sync.Once
}

// NewMuxClient creates a client. Call Run() in a goroutine.
func NewMuxClient(r io.Reader, w io.Writer) *MuxClient {
	return &MuxClient{
		r:        r,
		w:        w,
		channels: make(map[uint16]*clientChan),
		nextChan: 0,
		out:      make(chan Frame, MUX_OUT_BUF),
		err:      make(chan error, 2),
		routesCh: make(chan []string, 1),
	}
}

// RoutesCh returns the channel on which CMD_ROUTES will be delivered (once).
func (c *MuxClient) RoutesCh() <-chan []string {
	return c.routesCh
}

// Run starts the mux client loops. Blocks until the connection dies.
func (c *MuxClient) Run() error {
	go c.writer()
	go c.reader()
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
	return &ClientConn{client: c, channel: ch, inbox: inbox}, nil
}

// DNSRequest sends a DNS query and blocks until the response arrives (or timeout).
func (c *MuxClient) DNSRequest(data []byte) ([]byte, error) {
	if c.done.Load() {
		return nil, fmt.Errorf("mux: client closed")
	}
	ch, inbox := c.allocChannel()
	defer c.freeChannel(ch)

	c.out <- Frame{Channel: ch, Cmd: CMD_DNS_REQ, Data: data}

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


func (c *MuxClient) allocChannel() (uint16, chan Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := 0; i < MAX_CHAN; i++ {
		c.nextChan++
		if c.nextChan == 0 {
			c.nextChan = 1
		}
		if _, used := c.channels[c.nextChan]; !used {
			inbox := make(chan Frame, 64)
			c.channels[c.nextChan] = &clientChan{inbox: inbox}
			return c.nextChan, inbox
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
		cc.once.Do(func() { close(cc.inbox) })
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
	for f := range c.out {
		if err := WriteFrame(c.w, f); err != nil {
			c.err <- err
			return
		}
	}
}

func (c *MuxClient) dispatchIncoming(f Frame) {
	switch f.Cmd {
	case CMD_PING:
		c.out <- Frame{Channel: 0, Cmd: CMD_PONG, Data: f.Data}
	case CMD_PONG:
		// keepalive response — ignore
	case CMD_ROUTES:
		routes := parseRoutes(f.Data)
		select {
		case c.routesCh <- routes:
		default:
		}
	default:
		c.mu.Lock()
		cc, ok := c.channels[f.Channel]
		c.mu.Unlock()
		if ok {
			select {
			case cc.inbox <- f:
			default:
				// drop if inbox full
			}
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
	client  *MuxClient
	channel uint16
	inbox   chan Frame
	buf     []byte // leftover read buffer
	closed  sync.Once
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
	return n, nil
}

func (cc *ClientConn) Write(b []byte) (int, error) {
	if cc.client.done.Load() {
		return 0, net.ErrClosed
	}
	total := 0
	for len(b) > 0 {
		chunk := b
		if len(chunk) > BUF_SIZE {
			chunk = chunk[:BUF_SIZE]
		}
		d := make([]byte, len(chunk))
		copy(d, chunk)
		cc.client.out <- Frame{Channel: cc.channel, Cmd: CMD_TCP_DATA, Data: d}
		total += len(chunk)
		b = b[len(chunk):]
	}
	return total, nil
}

func (cc *ClientConn) CloseWrite() error {
	cc.client.out <- Frame{Channel: cc.channel, Cmd: CMD_TCP_EOF}
	return nil
}

func (cc *ClientConn) Close() error {
	cc.closed.Do(func() {
		cc.client.out <- Frame{Channel: cc.channel, Cmd: CMD_TCP_EOF}
		cc.client.freeChannel(cc.channel)
	})
	return nil
}

// net.Conn boilerplate — not used but required by interface.
func (cc *ClientConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (cc *ClientConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (cc *ClientConn) SetDeadline(t time.Time) error    { return nil }
func (cc *ClientConn) SetReadDeadline(t time.Time) error { return nil }
func (cc *ClientConn) SetWriteDeadline(t time.Time) error { return nil }
