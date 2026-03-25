package mux

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Handlers that the MuxServer calls when new requests arrive.
type ServerHandlers struct {
	// NewTCP is called when CMD_TCP_CONNECT arrives.
	// family: 2=IPv4, 10=IPv6 (Linux AF_ constants).
	NewTCP func(channel uint16, family int, dstIP string, dstPort int)

	// DNSReq is called when CMD_DNS_REQ arrives.
	DNSReq func(channel uint16, data []byte)

	// UDPOpen is called when CMD_UDP_OPEN arrives.
	UDPOpen func(channel uint16, family int)

	// UDPData is called when CMD_UDP_DATA arrives on an open UDP channel.
	UDPData func(channel uint16, data []byte)

	// UDPClose is called when CMD_UDP_CLOSE arrives.
	UDPClose func(channel uint16)

	// HostReq is called when CMD_HOST_REQ arrives (host watch feature).
	HostReq func(data []byte)
}

// chanState tracks per-channel state on the server.
type chanState struct {
	inbox *asyncInbox    // frames from mux destined for the downstream conn
	sw    *sendWindow    // nil when flow control is off
}

// MuxServer runs the server-side mux loop, reading from r and writing to w.
type MuxServer struct {
	r        io.Reader
	w        io.Writer
	handlers ServerHandlers

	mu       sync.Mutex
	channels map[uint16]*chanState

	out         chan Frame // serialised writer goroutine
	priorityOut chan Frame // PONG/DNS_RESPONSE/WINDOW_UPDATE bypass bulk data
	err         chan error // fatal error from reader or writer

	lastFrame atomic.Int64 // UnixNano of last frame received (idle watchdog)

	flowControl   bool
	initialWindow int64
}

// NewMuxServer creates a MuxServer. Call Run() to start it.
func NewMuxServer(r io.Reader, w io.Writer, h ServerHandlers) *MuxServer {
	return &MuxServer{
		r:           r,
		w:           w,
		handlers:    h,
		channels:    make(map[uint16]*chanState),
		out:         make(chan Frame, MUX_OUT_BUF),
		priorityOut: make(chan Frame, PRIORITY_OUT_BUF),
		err:         make(chan error, 2),
	}
}

// SetFlowControl enables per-channel sliding window flow control.
// Must be called before Run().
func (s *MuxServer) SetFlowControl(enabled bool, initialWindow int64) {
	s.flowControl = enabled
	s.initialWindow = initialWindow
}

// Send enqueues a frame to be written to the client.
func (s *MuxServer) Send(f Frame) {
	s.out <- f
}

// SendTo is a convenience wrapper used by handler goroutines.
func (s *MuxServer) SendTo(channel uint16, cmd uint16, data []byte) {
	s.Send(Frame{Channel: channel, Cmd: cmd, Data: data})
}

// SendPriority enqueues a high-priority frame that bypasses the bulk data queue.
func (s *MuxServer) SendPriority(f Frame) {
	s.priorityOut <- f
}

// SendPriorityTo is a convenience wrapper for SendPriority.
func (s *MuxServer) SendPriorityTo(channel uint16, cmd uint16, data []byte) {
	s.SendPriority(Frame{Channel: channel, Cmd: cmd, Data: data})
}

func (s *MuxServer) sendWindowUpdate(ch uint16, credit int64) {
	s.SendPriority(Frame{Channel: ch, Cmd: CMD_WINDOW_UPDATE, Data: EncodeWindowUpdate(credit)})
}

// InboxFor returns the inbox channel for a given channel, or nil if closed/unknown.
func (s *MuxServer) InboxFor(channel uint16) <-chan Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cs, ok := s.channels[channel]; ok {
		return cs.inbox.C()
	}
	return nil
}

// channelState returns the full chanState for a given channel.
func (s *MuxServer) channelState(channel uint16) *chanState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.channels[channel]
}

// ChannelState is the exported version of channelState for use by handlers.
func (s *MuxServer) ChannelState(channel uint16) *chanState {
	return s.channelState(channel)
}

// FlowControlEnabled returns whether flow control is active.
func (s *MuxServer) FlowControlEnabled() bool {
	return s.flowControl
}

// SendWindowUpdate sends a WINDOW_UPDATE frame via the priority channel.
func (s *MuxServer) SendWindowUpdate(ch uint16, credit int64) {
	s.sendWindowUpdate(ch, credit)
}

// SW returns the sendWindow for this channel state, or nil.
func (cs *chanState) SW() *sendWindow {
	if cs == nil {
		return nil
	}
	return cs.sw
}

// CloseChannel removes a channel and closes its inbox.
func (s *MuxServer) CloseChannel(channel uint16) {
	s.mu.Lock()
	cs, ok := s.channels[channel]
	if ok {
		delete(s.channels, channel)
	}
	s.mu.Unlock()
	if ok {
		if cs.sw != nil {
			cs.sw.Kill()
		}
		cs.inbox.Close()
	}
}

// Run starts the mux server and blocks until the connection closes or an error occurs.
func (s *MuxServer) Run() error {
	s.lastFrame.Store(time.Now().UnixNano())
	go s.writer()
	go s.reader()
	go s.idleWatchdog()
	return <-s.err
}

func (s *MuxServer) reader() {
	for {
		f, err := ReadFrame(s.r)
		if err != nil {
			s.err <- err
			return
		}
		s.lastFrame.Store(time.Now().UnixNano())
		if err := s.dispatch(f); err != nil {
			s.err <- err
			return
		}
	}
}

func (s *MuxServer) writer() {
	for {
		// Drain all priority frames first.
		select {
		case f, ok := <-s.priorityOut:
			if !ok {
				return
			}
			if err := WriteFrame(s.w, f); err != nil {
				s.err <- err
				return
			}
			continue
		default:
		}
		select {
		case f, ok := <-s.priorityOut:
			if !ok {
				return
			}
			if err := WriteFrame(s.w, f); err != nil {
				s.err <- err
				return
			}
		case f, ok := <-s.out:
			if !ok {
				return
			}
			if err := WriteFrame(s.w, f); err != nil {
				s.err <- err
				return
			}
		}
	}
}

func (s *MuxServer) idleWatchdog() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		last := time.Unix(0, s.lastFrame.Load())
		if time.Since(last) > SERVER_IDLE_TIMEOUT {
			s.err <- fmt.Errorf("mux: server idle timeout (no frames for %v)", time.Since(last))
			return
		}
	}
}

func (s *MuxServer) dispatch(f Frame) error {
	switch f.Cmd {
	case CMD_PING:
		s.SendPriorityTo(0, CMD_PONG, f.Data)

	case CMD_EXIT:
		return fmt.Errorf("mux: received CMD_EXIT")

	case CMD_TCP_CONNECT:
		family, dstIP, dstPort, err := parseTCPConnect(f.Data)
		if err != nil {
			s.SendTo(f.Channel, CMD_TCP_EOF, nil)
			return nil
		}
		var sw *sendWindow
		if s.flowControl {
			sw = newSendWindow(s.initialWindow)
		}
		cs := &chanState{inbox: newAsyncInbox(), sw: sw}
		s.mu.Lock()
		s.channels[f.Channel] = cs
		s.mu.Unlock()
		if s.handlers.NewTCP != nil {
			go s.handlers.NewTCP(f.Channel, family, dstIP, dstPort)
		}

	case CMD_DNS_REQ:
		if s.handlers.DNSReq != nil {
			go s.handlers.DNSReq(f.Channel, f.Data)
		}

	case CMD_UDP_OPEN:
		var family int
		fmt.Sscanf(string(f.Data), "%d", &family)
		var sw *sendWindow
		if s.flowControl {
			sw = newSendWindow(s.initialWindow)
		}
		cs := &chanState{inbox: newAsyncInbox(), sw: sw}
		s.mu.Lock()
		s.channels[f.Channel] = cs
		s.mu.Unlock()
		if s.handlers.UDPOpen != nil {
			go s.handlers.UDPOpen(f.Channel, family)
		}

	case CMD_HOST_REQ:
		if s.handlers.HostReq != nil {
			s.handlers.HostReq(f.Data)
		}

	case CMD_WINDOW_UPDATE:
		if s.flowControl {
			credit := DecodeWindowUpdate(f.Data)
			s.mu.Lock()
			cs, ok := s.channels[f.Channel]
			s.mu.Unlock()
			if ok && cs.sw != nil {
				cs.sw.Release(credit)
			}
		}

	default:
		// Per-channel data (TCP_DATA, TCP_EOF, TCP_STOP_SENDING, UDP_DATA, UDP_CLOSE)
		s.mu.Lock()
		cs, ok := s.channels[f.Channel]
		s.mu.Unlock()
		if ok {
			cs.inbox.send(f)
		}
	}
	return nil
}

// parseTCPConnect parses "family,dstIP,dstPort" from CMD_TCP_CONNECT data.
func parseTCPConnect(data []byte) (family int, dstIP string, dstPort int, err error) {
	parts := splitComma(string(data), 3)
	if len(parts) != 3 {
		err = fmt.Errorf("mux: bad TCP_CONNECT data: %q", data)
		return
	}
	if _, err = fmt.Sscanf(parts[0], "%d", &family); err != nil {
		return
	}
	dstIP = parts[1]
	_, err = fmt.Sscanf(parts[2], "%d", &dstPort)
	return
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

// HandleTCP is a ready-made TCP connection handler for MuxServer.
// Call it from ServerHandlers.NewTCP.
func (s *MuxServer) HandleTCP(channel uint16, family int, dstIP string, dstPort int) {
	// If dstIP is a domain name (from SOCKS5 proxy), use dual-stack "tcp"
	// so the server resolves it. For raw IPs, use the specified family.
	netFamily := "tcp4"
	if net.ParseIP(dstIP) == nil {
		netFamily = "tcp" // domain name — let the server resolve it
	} else if family != 2 { // AF_INET = 2
		netFamily = "tcp6"
	}
	addr := net.JoinHostPort(dstIP, strconv.Itoa(dstPort))

	conn, err := net.DialTimeout(netFamily, addr, 10*time.Second)
	if err != nil {
		s.SendTo(channel, CMD_TCP_EOF, nil)
		s.CloseChannel(channel)
		return
	}
	defer conn.Close()

	inbox := s.InboxFor(channel)
	if inbox == nil {
		return
	}

	// remote → mux
	go func() {
		buf := make([]byte, BUF_SIZE)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				// Acquire send window before transmitting.
				if s.flowControl {
					cs := s.channelState(channel)
					if cs != nil && cs.sw != nil {
						if !cs.sw.Acquire(n) {
							break
						}
					}
				}
				d := make([]byte, n)
				copy(d, buf[:n])
				s.SendTo(channel, CMD_TCP_DATA, d)
			}
			if err != nil {
				break
			}
		}
		s.SendTo(channel, CMD_TCP_EOF, nil)
		s.CloseChannel(channel)
	}()

	// mux → remote
	var rxPending int64
	for f := range inbox {
		switch f.Cmd {
		case CMD_TCP_DATA:
			if _, err := conn.Write(f.Data); err != nil {
				return
			}
			// Grant credit back to the client sender in batches.
			if s.flowControl {
				rxPending += int64(len(f.Data))
				if rxPending >= WINDOW_UPDATE_THRESHOLD {
					s.sendWindowUpdate(channel, rxPending)
					rxPending = 0
				}
			}
		case CMD_TCP_EOF:
			if tc, ok := conn.(*net.TCPConn); ok {
				tc.CloseWrite()
			}
		case CMD_TCP_STOP_SENDING:
			return
		}
	}
}
