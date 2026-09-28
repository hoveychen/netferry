package proxy

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hoveychen/netferry/relay/internal/stats"
)

// The LAN proxies share forwardTCP / the UDP relay with the tunnel path; a
// direct route rule for loopback exercises parsing and forwarding end to end
// without a mux.
func directCounters() *stats.Counters {
	c := stats.NewCounters()
	c.SetRouteModes(map[string]stats.RouteMode{
		"127.0.0.1": {Kind: stats.RouteDirect},
		"localhost": {Kind: stats.RouteDirect},
	})
	return c
}

func startTCPEcho(t *testing.T) *net.TCPAddr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return ln.Addr().(*net.TCPAddr)
}

func startServer(t *testing.T, serve func(net.Listener) error) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go serve(ln)
	return ln.Addr().String()
}

func socks5Dial(t *testing.T, proxyAddr string, cmd byte, req []byte) (net.Conn, []byte) {
	t.Helper()
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte{5, 1, 0})
	greet := make([]byte, 2)
	if _, err := io.ReadFull(c, greet); err != nil {
		t.Fatal(err)
	}
	c.Write(append([]byte{5, cmd, 0}, req...))
	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil {
		t.Fatal(err)
	}
	if head[1] != 0 {
		t.Fatalf("socks5 reply %d", head[1])
	}
	n := 4
	if head[3] == socks5AddrIPv6 {
		n = 16
	}
	bound := make([]byte, n+2)
	if _, err := io.ReadFull(c, bound); err != nil {
		t.Fatal(err)
	}
	return c, bound
}

func addrIPv4(ip net.IP, port int) []byte {
	return binary.BigEndian.AppendUint16(append([]byte{socks5AddrIPv4}, ip.To4()...), uint16(port))
}

func addrDomain(host string, port int) []byte {
	b := append([]byte{socks5AddrDomain, byte(len(host))}, host...)
	return binary.BigEndian.AppendUint16(b, uint16(port))
}

func TestSOCKS5ConnectDirect(t *testing.T) {
	echo := startTCPEcho(t)
	counters := directCounters()
	proxyAddr := startServer(t, func(ln net.Listener) error { return ServeSOCKS5(ln, nil, counters) })

	c, _ := socks5Dial(t, proxyAddr, socks5CmdConnect, addrIPv4(echo.IP, echo.Port))
	defer c.Close()
	c.Write([]byte("hello"))
	got := make([]byte, 5)
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "hello" {
		t.Fatalf("echo = %q, %v", got, err)
	}
}

// loopbackUDPWorks reports whether a UDP round trip on 127.0.0.1 succeeds;
// a running netferry pf anchor blocks all non-DNS UDP, loopback included.
func loopbackUDPWorks() bool {
	s, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return false
	}
	defer s.Close()
	c, err := net.DialUDP("udp", nil, s.LocalAddr().(*net.UDPAddr))
	if err != nil {
		return false
	}
	defer c.Close()
	c.Write([]byte("x"))
	s.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, err = s.ReadFromUDP(make([]byte, 1))
	return err == nil
}

func TestSOCKS5UDPAssociateDirect(t *testing.T) {
	if !loopbackUDPWorks() {
		t.Skip("loopback UDP is blocked here (netferry pf anchor active?)")
	}
	echo, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := echo.ReadFromUDP(buf)
			if err != nil {
				return
			}
			echo.WriteToUDP(buf[:n], from)
		}
	}()
	echoAddr := echo.LocalAddr().(*net.UDPAddr)

	counters := directCounters()
	proxyAddr := startServer(t, func(ln net.Listener) error { return ServeSOCKS5(ln, nil, counters) })
	ctrl, bound := socks5Dial(t, proxyAddr, socks5CmdUDPAssociate, addrIPv4(net.IPv4zero, 0))
	defer ctrl.Close()
	relay := &net.UDPAddr{IP: net.IP(bound[:4]), Port: int(binary.BigEndian.Uint16(bound[4:]))}

	uc, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()
	uc.SetDeadline(time.Now().Add(5 * time.Second))

	for _, dst := range [][]byte{
		addrIPv4(echoAddr.IP, echoAddr.Port),
		addrDomain("localhost", echoAddr.Port),
	} {
		uc.Write(append(append([]byte{0, 0, 0}, dst...), "ping"...))
		buf := make([]byte, 2048)
		n, err := uc.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		host, port, payload, err := parseSOCKS5UDP(buf[:n])
		if err != nil || host != "127.0.0.1" || port != echoAddr.Port || string(payload) != "ping" {
			t.Fatalf("reply = %s:%d %q, %v", host, port, payload, err)
		}
	}
}

func TestHTTPProxyForwardAndConnect(t *testing.T) {
	var gotReq *http.Request
	var gotBody string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		fmt.Fprint(w, "ok:"+r.URL.Path)
	}))
	defer origin.Close()

	counters := directCounters()
	proxyAddr := startServer(t, func(ln net.Listener) error { return ServeHTTPProxy(ln, nil, counters) })
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}

	// Plain http:// with a chunked body.
	resp, err := hc.Post(origin.URL+"/p?q=1", "text/plain", io.MultiReader(strings.NewReader("bo"), strings.NewReader("dy")))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok:/p" || gotBody != "body" || gotReq.RequestURI != "/p?q=1" || gotReq.Header.Get("Proxy-Connection") != "" {
		t.Fatalf("forward: body=%q upstreamBody=%q uri=%q", body, gotBody, gotReq.RequestURI)
	}

	// CONNECT, then raw bytes both ways.
	echo := startTCPEcho(t)
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", echo, echo)
	head := make([]byte, len("HTTP/1.1 200 Connection Established\r\n\r\n"))
	if _, err := io.ReadFull(c, head); err != nil || !bytes.HasPrefix(head, []byte("HTTP/1.1 200")) {
		t.Fatalf("connect reply %q, %v", head, err)
	}
	c.Write([]byte("tls?"))
	got := make([]byte, 4)
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "tls?" {
		t.Fatalf("echo = %q, %v", got, err)
	}
}
