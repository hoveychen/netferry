// Package proxy implements the local transparent TCP proxy and DNS interceptor.
package proxy

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// directDialTracker prevents infinite redirect loops when the firewall
// fails to exclude the proxy's own outgoing connections from the redirect
// rules. If the same destination accumulates too many concurrent direct
// dials, further attempts are rejected.
var directDialTracker struct {
	mu    sync.Mutex
	count map[string]int32 // dstAddr → active direct dial count
}

const maxConcurrentDirectDials = 8 // per destination

func init() {
	directDialTracker.count = make(map[string]int32)
}

func directDialAcquire(dst string) bool {
	directDialTracker.mu.Lock()
	defer directDialTracker.mu.Unlock()
	if directDialTracker.count[dst] >= maxConcurrentDirectDials {
		return false
	}
	directDialTracker.count[dst]++
	return true
}

func directDialRelease(dst string) {
	directDialTracker.mu.Lock()
	defer directDialTracker.mu.Unlock()
	directDialTracker.count[dst]--
	if directDialTracker.count[dst] <= 0 {
		delete(directDialTracker.count, dst)
	}
}

// directDialLoopDetected counts how many times a loop was detected and
// logged, to avoid flooding the log.
var directDialLoopDetected atomic.Int64

// QueryOrigDstFunc is the platform-specific function to resolve the original
// destination of a redirected TCP connection.
// Set by platform-specific init() or by the caller before Listen().
var QueryOrigDstFunc func(conn net.Conn) (ip string, port int, err error)

// UseTProxy selects the TPROXY listener instead of the default NAT-based one.
// Set by cmd/tunnel when --method=tproxy is chosen. Only used on Linux.
var UseTProxy bool

// BindAddr is the address the proxy listens on. Defaults to "127.0.0.1".
// WinDivert on Windows requires "0.0.0.0" because it redirects packets to a
// non-loopback interface address.
var BindAddr = "127.0.0.1"

// connIdleTimeout is the maximum time a proxied connection may be idle
// (no data flowing in either direction) before it is forcibly closed.
// Prevents stuck connections from accumulating during network congestion.
const connIdleTimeout = 2 * time.Minute

// Listen accepts connections on the local proxy port and forwards them via mux.
// Blocks until the listener is closed.
func Listen(port int, client mux.TunnelClient, counters *stats.Counters) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", BindAddr, port))
	if err != nil {
		return fmt.Errorf("proxy listen :%d: %w", port, err)
	}
	defer ln.Close()

	log.Printf("proxy: listening on :%d", port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleConn(conn, client, counters)
	}
}

func handleConn(conn net.Conn, client mux.TunnelClient, counters *stats.Counters) {
	defer conn.Close()
	startedAt := time.Now()

	// Resolve original destination.
	var dstIP string
	var dstPort int
	var err error

	if QueryOrigDstFunc != nil {
		dstIP, dstPort, err = QueryOrigDstFunc(conn)
		if err != nil {
			log.Printf("proxy: origdst lookup: %v", err)
			return
		}
	} else {
		// Fallback: use local address (happens when no firewall redirect).
		la := conn.LocalAddr().(*net.TCPAddr)
		dstIP = la.IP.String()
		dstPort = la.Port
	}

	// Determine address family.
	ip := net.ParseIP(dstIP)
	family := 2 // AF_INET
	if ip != nil && ip.To4() == nil {
		family = 10 // AF_INET6
	}

	srcAddr := conn.RemoteAddr().String()
	dstAddr := fmt.Sprintf("%s:%d", dstIP, dstPort)

	// Peek at the first bytes to extract the hostname (TLS SNI or HTTP Host).
	host, br := peekHost(conn, dstPort)
	if host != "" {
		log.Printf("c : Accept TCP: %s -> %s (%s).", srcAddr, dstAddr, host)
	} else {
		log.Printf("c : Accept TCP: %s -> %s.", srcAddr, dstAddr)
	}

	// Look up destination priority and route mode.
	priority := stats.DefaultPriority
	routeKind := stats.RouteTunnel
	if counters != nil {
		priority = counters.LookupPriority(dstAddr, host)
		routeKind = counters.LookupRouteMode(dstAddr, host).Kind
	}

	// Apply route mode: blocked → reject, direct → bypass tunnel.
	switch routeKind {
	case stats.RouteBlocked:
		log.Printf("proxy: blocked %s -> %s (%s)", srcAddr, dstAddr, host)
		return
	case stats.RouteDirect:
		handleDirect(conn, br, dstAddr, srcAddr, host, counters, startedAt)
		return
	}

	// Open a mux channel first so we can record the tunnel index in ConnOpen.
	muxConn, err := client.OpenTCP(family, dstIP, dstPort, priority)
	if err != nil {
		log.Printf("proxy: open channel to %s:%d: %v", dstIP, dstPort, err)
		return
	}
	defer muxConn.Close()

	var connID uint64
	if counters != nil {
		connID = counters.ConnOpen(srcAddr, dstAddr, host, muxConn.TunnelIndex)
	}

	// touch resets the idle deadline on both ends. Called after each successful
	// write so any data flowing in either direction keeps the connection alive.
	touch := func() {
		deadline := time.Now().Add(connIdleTimeout)
		conn.SetDeadline(deadline)
		muxConn.SetDeadline(deadline)
	}
	touch() // set initial deadline before first I/O

	// Bidirectional copy. Use the buffered reader (br) so peeked bytes are
	// replayed into the mux channel.
	done := make(chan copyResult, 2)
	go func() {
		n, err := io.Copy(&countingWriter{w: muxConn, touch: touch, onWrite: func(wrote int) {
			if counters != nil {
				counters.ConnAddTx(connID, int64(wrote))
			}
		}}, br)
		muxConn.CloseWrite()
		done <- copyResult{direction: "upload", bytes: n, err: normalizeCopyErr(err)}
	}()
	go func() {
		n, err := io.Copy(&countingWriter{w: conn, touch: touch, onWrite: func(wrote int) {
			if counters != nil {
				counters.ConnAddRx(connID, int64(wrote))
			}
		}}, muxConn)
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- copyResult{direction: "download", bytes: n, err: normalizeCopyErr(err)}
	}()
	first := <-done
	second := <-done
	if counters != nil {
		counters.ConnClose(connID, srcAddr, dstAddr)
	}
	logConnSummary("tcp", connID, srcAddr, dstAddr, host, startedAt, first, second)
}

// handleDirect establishes a direct TCP connection to the destination,
// bypassing the tunnel entirely. Used for "direct" route mode destinations.
func handleDirect(clientConn net.Conn, br io.Reader, dstAddr, srcAddr, host string, counters *stats.Counters, startedAt time.Time) {
	// Guard against redirect loops: if the firewall failed to exclude
	// our outgoing connection, it gets redirected back here, creating
	// an infinite cascade. Cap concurrent direct dials per destination.
	if !directDialAcquire(dstAddr) {
		n := directDialLoopDetected.Add(1)
		if n <= 3 || n%100 == 0 {
			log.Printf("proxy: direct-dial loop detected for %s (suppressed %d), falling back to tunnel", dstAddr, n)
		}
		return
	}
	defer directDialRelease(dstAddr)

	log.Printf("proxy: direct %s -> %s (%s)", srcAddr, dstAddr, host)

	remote, err := net.DialTimeout("tcp", dstAddr, 10*time.Second)
	if err != nil {
		log.Printf("proxy: direct dial %s: %v", dstAddr, err)
		return
	}
	defer remote.Close()

	var connID uint64
	if counters != nil {
		connID = counters.ConnOpen(srcAddr, dstAddr, host, 0)
	}

	touch := func() {
		deadline := time.Now().Add(connIdleTimeout)
		clientConn.SetDeadline(deadline)
		remote.SetDeadline(deadline)
	}
	touch()

	done := make(chan copyResult, 2)
	go func() {
		n, err := io.Copy(&countingWriter{w: remote, touch: touch, onWrite: func(wrote int) {
			if counters != nil {
				counters.ConnAddTx(connID, int64(wrote))
			}
		}}, br)
		if tc, ok := remote.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- copyResult{direction: "upload", bytes: n, err: normalizeCopyErr(err)}
	}()
	go func() {
		n, err := io.Copy(&countingWriter{w: clientConn, touch: touch, onWrite: func(wrote int) {
			if counters != nil {
				counters.ConnAddRx(connID, int64(wrote))
			}
		}}, remote)
		if tc, ok := clientConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- copyResult{direction: "download", bytes: n, err: normalizeCopyErr(err)}
	}()
	first := <-done
	second := <-done
	if counters != nil {
		counters.ConnClose(connID, srcAddr, dstAddr)
	}
	logConnSummary("tcp-direct", connID, srcAddr, dstAddr, host, startedAt, first, second)
}

type countingWriter struct {
	w       io.Writer
	onWrite func(int)
	touch   func() // called after each successful write to reset idle deadline
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	if n > 0 {
		if cw.onWrite != nil {
			cw.onWrite(n)
		}
		if cw.touch != nil {
			cw.touch()
		}
	}
	return n, err
}

type copyResult struct {
	direction string
	bytes     int64
	err       error
}

func normalizeCopyErr(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return nil // idle timeout fired; not a real error
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "use of closed network connection"):
		return nil
	case strings.Contains(msg, "closed pipe"):
		return nil
	default:
		return err
	}
}

func logConnSummary(kind string, connID uint64, srcAddr, dstAddr, host string, startedAt time.Time, first, second copyResult) {
	upload := first
	download := second
	if first.direction == "download" {
		upload, download = second, first
	}
	duration := time.Since(startedAt)
	fields := []string{
		fmt.Sprintf("id=%d", connID),
		fmt.Sprintf("src=%s", srcAddr),
		fmt.Sprintf("dst=%s", dstAddr),
		fmt.Sprintf("dur=%s", duration.Round(time.Millisecond)),
		fmt.Sprintf("upload=%dB", upload.bytes),
		fmt.Sprintf("download=%dB", download.bytes),
	}
	if host != "" {
		fields = append(fields, fmt.Sprintf("host=%q", host))
	}
	if upload.err != nil {
		fields = append(fields, fmt.Sprintf("upload_err=%q", upload.err))
	}
	if download.err != nil {
		fields = append(fields, fmt.Sprintf("download_err=%q", download.err))
	}
	prefix := "conn summary"
	if upload.err != nil || download.err != nil {
		prefix = "warning: conn summary"
	}
	log.Printf("%s: kind=%s %s", prefix, kind, strings.Join(fields, " "))
}
