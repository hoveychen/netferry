// netferry-server is the remote-side binary deployed to the SSH host.
// It communicates via stdin/stdout using smux over the SSH channel.
// Zero external dependencies beyond smux — no OS-specific code.
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/xtaci/smux"
)

var Version = "dev"

// ctrlSocketPath returns the unix socket path used to coordinate the data and
// ctrl SSH sessions when running in split-conn mode.
func ctrlSocketPath(sessionID string) string {
	return filepath.Join(os.TempDir(), "netferry-ctrl-"+sessionID+".sock")
}

// runCtrlRelay handles the --role=ctrl mode.
//
// The ctrl relay connects to the unix socket created by the main server
// instance and bidirectionally forwards bytes between the socket and
// stdin/stdout (the SSH session's data channel).  It then writes the sync
// header so the client knows the ctrl channel is fully connected.
func runCtrlRelay(sessionID string) {
	sockPath := ctrlSocketPath(sessionID)
	var conn net.Conn
	var err error
	for i := 0; i < 20; i++ {
		conn, err = net.DialTimeout("unix", sockPath, time.Second)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "s: ctrl relay: dial %s: %v\n", sockPath, err)
		os.Exit(1)
	}
	defer conn.Close()

	// Signal the client that the ctrl channel is ready.
	if err := mux.WriteSyncHeader(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "s: ctrl relay: write sync header: %v\n", err)
		return
	}

	done := make(chan struct{}, 2)
	go func() { io.Copy(conn, os.Stdin); done <- struct{}{} }()
	go func() { io.Copy(os.Stdout, conn); done <- struct{}{} }()
	<-done
}

func main() {
	autoNets := false
	toNameserver := ""
	verbose := false
	sessionID := ""
	role := "main"

	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--auto-nets":
			autoNets = true
		case "--verbose", "-v":
			verbose = true
		case "--flow-control": // accepted but ignored; smux handles flow control
		case "--to-ns":
			i++
			if i < len(os.Args) {
				toNameserver = os.Args[i]
			}
		case "--session-id":
			i++
			if i < len(os.Args) {
				sessionID = os.Args[i]
			}
		case "--role":
			i++
			if i < len(os.Args) {
				role = os.Args[i]
			}
		case "--version":
			fmt.Println(Version)
			os.Exit(0)
		}
	}

	if verbose {
		log.SetFlags(0)
		log.SetPrefix(" s: ")
	} else {
		log.SetOutput(io.Discard)
	}

	// Handle ctrl relay mode: no smux session, just relay bytes to the main
	// server's unix socket.
	if role == "ctrl" {
		if sessionID == "" {
			fmt.Fprintln(os.Stderr, "s: --role=ctrl requires --session-id")
			os.Exit(1)
		}
		runCtrlRelay(sessionID)
		return
	}

	log.Printf("netferry-server %s on %s/%s", Version, runtime.GOOS, runtime.GOARCH)

	// Split-conn mode: create a unix socket for the ctrl relay BEFORE writing
	// the sync header so the ctrl relay can always connect after the client
	// reads the sync header from the data session.
	var ctrlConn net.Conn
	if sessionID != "" {
		sockPath := ctrlSocketPath(sessionID)
		os.Remove(sockPath) // remove stale socket from a previous run
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "s: ctrl socket listen: %v\n", err)
			os.Exit(1)
		}
		defer os.Remove(sockPath)
		defer ln.Close()

		// Write sync header for the data session first so the client knows
		// the main server is up and starts the ctrl session.
		if err := mux.WriteSyncHeader(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "s: fatal: write sync header: %v\n", err)
			os.Exit(1)
		}

		// Block until the ctrl relay connects (it connects after the client
		// reads the data sync header and starts the ctrl SSH session).
		if ul, ok := ln.(*net.UnixListener); ok {
			ul.SetDeadline(time.Now().Add(30 * time.Second))
		}
		ctrlConn, err = ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "s: ctrl socket accept: %v\n", err)
			os.Exit(1)
		}
		if ul, ok := ln.(*net.UnixListener); ok {
			ul.SetDeadline(time.Time{})
		}
		defer ctrlConn.Close()
	} else {
		// Write synchronisation header — client reads this to confirm server started.
		if err := mux.WriteSyncHeader(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "s: fatal: write sync header: %v\n", err)
			os.Exit(1)
		}
	}

	var conn io.ReadWriteCloser
	if ctrlConn != nil {
		conn = mux.NewSplitConn(os.Stdin, os.Stdout, ctrlConn, ctrlConn)
	} else {
		conn = &rwConn{r: os.Stdin, w: os.Stdout}
	}

	cfg := smux.DefaultConfig()
	cfg.Version = 2
	cfg.MaxFrameSize = 65535
	cfg.MaxReceiveBuffer = 16 * 1024 * 1024
	cfg.MaxStreamBuffer = 4 * 1024 * 1024
	cfg.KeepAliveInterval = mux.KEEPALIVE_INTERVAL
	cfg.KeepAliveTimeout = mux.KEEPALIVE_INTERVAL + mux.KEEPALIVE_TIMEOUT

	sess, err := smux.Server(conn, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "s: fatal: smux.Server: %v\n", err)
		os.Exit(1)
	}

	// Push routes to the client via a server-opened stream.
	go pushRoutes(sess, autoNets)

	// Accept and dispatch client streams.
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			// Session closed (SSH channel died) — exit cleanly.
			break
		}
		go handleStream(stream, toNameserver)
	}
}

// rwConn adapts separate stdin/stdout into an io.ReadWriteCloser for smux.
type rwConn struct {
	r io.Reader
	w io.Writer
}

func (c *rwConn) Read(b []byte) (int, error)  { return c.r.Read(b) }
func (c *rwConn) Write(b []byte) (int, error) { return c.w.Write(b) }
func (c *rwConn) Close() error                { return nil }

// pushRoutes opens a server-initiated stream and writes the route list.
func pushRoutes(sess *smux.Session, autoNets bool) {
	stream, err := sess.OpenStream()
	if err != nil {
		log.Printf("pushRoutes: open stream: %v", err)
		return
	}
	defer stream.Close()

	routeData := buildRoutePacket(autoNets)
	payload := append([]byte("ROUTES\n"), routeData...)
	if _, err := stream.Write(payload); err != nil {
		log.Printf("pushRoutes: write: %v", err)
	}
}

// handleStream reads the stream type header and dispatches to the right handler.
func handleStream(stream *smux.Stream, toNameserver string) {
	defer stream.Close()

	br := bufio.NewReader(stream)
	hdr, err := br.ReadString('\n')
	if err != nil {
		return
	}
	hdr = strings.TrimRight(hdr, "\n")
	parts := strings.SplitN(hdr, " ", 4)

	switch parts[0] {
	case "TCP":
		if len(parts) != 4 {
			log.Printf("bad TCP header: %q", hdr)
			return
		}
		family, _ := strconv.Atoi(parts[1])
		ip := parts[2]
		port, _ := strconv.Atoi(parts[3])
		handleTCP(stream, br, family, ip, port)

	case "DNS":
		handleDNS(stream, br, toNameserver)

	case "UDP":
		if len(parts) != 2 {
			log.Printf("bad UDP header: %q", hdr)
			return
		}
		family, _ := strconv.Atoi(parts[1])
		handleUDP(stream, br, family)

	default:
		log.Printf("unknown stream type: %q", hdr)
	}
}

// handleTCP proxies a TCP connection through the stream using length-framed messages.
// Half-close (zero-length message) from either side is forwarded to the real TCP conn.
func handleTCP(stream *smux.Stream, br *bufio.Reader, family int, dstIP string, dstPort int) {
	netFamily := "tcp4"
	if net.ParseIP(dstIP) == nil {
		netFamily = "tcp" // domain — resolve on server
	} else if family != 2 {
		netFamily = "tcp6"
	}
	addr := net.JoinHostPort(dstIP, strconv.Itoa(dstPort))
	log.Printf("TCP → %s", addr)

	conn, err := net.DialTimeout(netFamily, addr, 10*time.Second)
	if err != nil {
		log.Printf("TCP dial %s: %v", addr, err)
		// Signal EOF back to client.
		writeMsg(stream, nil)
		return
	}
	defer conn.Close()

	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	type result struct{ err error }
	done := make(chan result, 2)

	// mux → remote: read length-framed messages from client, write to remote conn.
	go func() {
		for {
			payload, err := readMsgBuf(br)
			if err != nil {
				done <- result{err}
				return
			}
			if payload == nil {
				// Half-close from client: signal EOF to remote.
				if tc, ok := conn.(*net.TCPConn); ok {
					tc.CloseWrite()
				}
				done <- result{nil}
				return
			}
			if _, err := conn.Write(payload); err != nil {
				done <- result{err}
				return
			}
		}
	}()

	// remote → mux: read from remote conn, write length-framed messages to client.
	go func() {
		buf := make([]byte, mux.BUF_SIZE)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if werr := writeMsg(stream, buf[:n]); werr != nil {
					done <- result{werr}
					return
				}
			}
			if err != nil {
				// Send half-close to client.
				writeMsg(stream, nil)
				done <- result{err}
				return
			}
		}
	}()

	<-done
	<-done
}

// handleDNS forwards a single DNS query and writes back the response.
func handleDNS(stream *smux.Stream, br *bufio.Reader, toNameserver string) {
	stream.SetDeadline(time.Now().Add(15 * time.Second))

	query, err := readMsgBuf(br)
	if err != nil || query == nil {
		return
	}

	ns := resolveNameserver(toNameserver)
	log.Printf("DNS len=%d → %s", len(query), ns)

	conn, err := net.DialTimeout("udp", ns, 5*time.Second)
	if err != nil {
		log.Printf("DNS dial %s: %v", ns, err)
		writeMsg(stream, servfail(query))
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	if _, err := conn.Write(query); err != nil {
		writeMsg(stream, servfail(query))
		return
	}
	buf := make([]byte, 65535)
	n, err := conn.Read(buf)
	if err != nil {
		writeMsg(stream, servfail(query))
		return
	}
	writeMsg(stream, buf[:n])
}

// handleUDP proxies UDP datagrams through the stream.
// Each datagram is length-framed; payload format: "ip,port,<raw>".
func handleUDP(stream *smux.Stream, br *bufio.Reader, family int) {
	netFamily := "udp4"
	if family != 2 {
		netFamily = "udp6"
	}
	conn, err := net.ListenPacket(netFamily, ":0")
	if err != nil {
		log.Printf("UDP listen: %v", err)
		return
	}
	defer conn.Close()

	// remote → mux
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			udpAddr := addr.(*net.UDPAddr)
			hdr := fmt.Sprintf("%s,%d,", udpAddr.IP.String(), udpAddr.Port)
			payload := make([]byte, len(hdr)+n)
			copy(payload, hdr)
			copy(payload[len(hdr):], buf[:n])
			if werr := writeMsg(stream, payload); werr != nil {
				return
			}
		}
	}()

	// mux → remote
	for {
		payload, err := readMsgBuf(br)
		if err != nil || payload == nil {
			return
		}
		// Parse "dstIP,dstPort,<raw>"
		s := string(payload)
		i1 := strings.Index(s, ",")
		if i1 < 0 {
			continue
		}
		rest := s[i1+1:]
		i2 := strings.Index(rest, ",")
		if i2 < 0 {
			continue
		}
		dstIP := s[:i1]
		dstPort, _ := strconv.Atoi(rest[:i2])
		data := payload[i1+1+i2+1:]
		dst := &net.UDPAddr{IP: net.ParseIP(dstIP), Port: dstPort}
		conn.WriteTo(data, dst)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// writeMsg writes a length-prefixed message (nil payload = half-close signal).
func writeMsg(w io.Writer, payload []byte) error {
	buf := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(buf[:2], uint16(len(payload)))
	copy(buf[2:], payload)
	_, err := w.Write(buf)
	return err
}

// readMsgBuf reads one length-prefixed message from a bufio.Reader.
// Returns (nil, nil) for a half-close frame.
func readMsgBuf(r *bufio.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(hdr[:])
	if n == 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func resolveNameserver(toNameserver string) string {
	if toNameserver != "" {
		parts := strings.SplitN(toNameserver, "@", 2)
		port := "53"
		if len(parts) == 2 {
			port = parts[1]
		}
		return net.JoinHostPort(parts[0], port)
	}
	if servers := readResolvConf(); len(servers) > 0 {
		return net.JoinHostPort(servers[0], "53")
	}
	return "127.0.0.1:53"
}

// servfail returns a minimal DNS SERVFAIL response for the given query.
func servfail(query []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	resp := make([]byte, 12)
	copy(resp[0:2], query[0:2])
	flags := uint16(query[2])<<8 | uint16(query[3])
	opcode := flags & 0x7800
	resp[2] = byte((0x8002 | opcode) >> 8)
	resp[3] = byte((0x8002 | opcode) & 0xff)
	return resp
}

func readResolvConf() []string {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	var servers []string
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.IndexAny(line, "#;"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nameserver ") {
			ip := strings.TrimSpace(line[11:])
			if net.ParseIP(ip) != nil {
				servers = append(servers, ip)
			}
		}
	}
	return servers
}

// ── route discovery ───────────────────────────────────────────────────────────

func buildRoutePacket(autoNets bool) []byte {
	if !autoNets {
		return nil
	}
	routes := listRoutes()
	log.Printf("auto-nets: %d routes", len(routes))
	var sb strings.Builder
	for _, rt := range routes {
		sb.WriteString(rt)
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}

func listRoutes() []string {
	var lines []string
	if _, err := exec.LookPath("ip"); err == nil {
		out, err := exec.Command("ip", "route").Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if rt := parseIPRoute(line); rt != "" {
					lines = append(lines, rt)
				}
			}
			return lines
		}
	}
	out, err := exec.Command("netstat", "-rn").Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rt := parseNetstatRoute(line); rt != "" {
			lines = append(lines, rt)
		}
	}
	return lines
}

func parseIPRoute(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 || !strings.Contains(fields[0], "/") || fields[0] == "default" {
		return ""
	}
	parts := strings.SplitN(fields[0], "/", 2)
	ip := parts[0]
	width, err := strconv.Atoi(parts[1])
	if err != nil || strings.HasPrefix(ip, "0.") || strings.HasPrefix(ip, "127.") {
		return ""
	}
	addr := net.ParseIP(ip)
	if addr == nil {
		return ""
	}
	family := 2
	if addr.To4() == nil {
		family = 10
	}
	return fmt.Sprintf("%d,%s,%d", family, ip, width)
}

func parseNetstatRoute(line string) string {
	cols := strings.Fields(line)
	if len(cols) < 3 {
		return ""
	}
	dest := cols[0]
	if dest == "Destination" || dest == "default" || dest == "Network" {
		return ""
	}
	var ip string
	var width int
	if strings.Contains(dest, "/") {
		parts := strings.SplitN(dest, "/", 2)
		ip = parts[0]
		w, err := strconv.Atoi(parts[1])
		if err != nil {
			return ""
		}
		width = w
	} else {
		ip = dest
		width = 32
	}
	if strings.HasPrefix(ip, "0.") || strings.HasPrefix(ip, "127.") {
		return ""
	}
	addr := net.ParseIP(ip)
	if addr == nil {
		return ""
	}
	family := 2
	if addr.To4() == nil {
		family = 10
	}
	return fmt.Sprintf("%d,%s,%d", family, ip, width)
}
