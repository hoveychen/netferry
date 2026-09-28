package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// ServeHTTPProxy runs an HTTP proxy on ln: CONNECT tunnels any TCP (HTTPS),
// absolute-URI requests forward plain http://. Phones only offer an HTTP
// proxy in their Wi-Fi settings, so this is how they reach the tunnel.
func ServeHTTPProxy(ln net.Listener, client mux.TunnelClient, counters *stats.Counters) error {
	log.Printf("proxy: HTTP proxy serving on %s", ln.Addr())
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleHTTPProxy(conn, client, counters)
	}
}

func handleHTTPProxy(conn net.Conn, client mux.TunnelClient, counters *stats.Counters) {
	defer conn.Close()
	startedAt := time.Now()

	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(conn)
	tp := textproto.NewReader(br)
	line, err := tp.ReadLine()
	if err != nil {
		return
	}
	method, rest, ok1 := strings.Cut(line, " ")
	target, proto, ok2 := strings.Cut(rest, " ")
	if !ok1 || !ok2 {
		httpProxyError(conn, http.StatusBadRequest)
		return
	}
	mh, err := tp.ReadMIMEHeader()
	if err != nil {
		httpProxyError(conn, http.StatusBadRequest)
		return
	}
	conn.SetReadDeadline(time.Time{})

	if method == http.MethodConnect {
		host, port, err := splitHostPortDefault(target, 443)
		if err != nil {
			httpProxyError(conn, http.StatusBadRequest)
			return
		}
		io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		forwardTCP("http", conn, br, host, port, client, counters, startedAt)
		return
	}

	u, err := url.Parse(target)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		httpProxyError(conn, http.StatusBadRequest)
		return
	}
	host, port, err := splitHostPortDefault(u.Host, 80)
	if err != nil {
		httpProxyError(conn, http.StatusBadRequest)
		return
	}
	// Rewrite to origin-form and force one request per connection, so a
	// keep-alive client cannot send its next request (maybe to another host)
	// down this upstream. The body stays in br and streams through untouched.
	h := http.Header(mh)
	for _, k := range []string{"Proxy-Connection", "Proxy-Authorization", "Keep-Alive"} {
		h.Del(k)
	}
	h.Set("Connection", "close")
	if h.Get("Host") == "" {
		h.Set("Host", u.Host)
	}
	var pre bytes.Buffer
	fmt.Fprintf(&pre, "%s %s %s\r\n", method, u.RequestURI(), proto)
	h.Write(&pre)
	pre.WriteString("\r\n")
	forwardTCP("http", conn, io.MultiReader(&pre, br), host, port, client, counters, startedAt)
}

func httpProxyError(conn net.Conn, code int) {
	fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nConnection: close\r\nContent-Length: 0\r\n\r\n", code, http.StatusText(code))
}

// splitHostPortDefault parses host[:port], applying def when the port is absent.
func splitHostPortDefault(hostport string, def int) (string, int, error) {
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return strings.Trim(hostport, "[]"), def, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("bad port %q", portStr)
	}
	return host, port, nil
}
