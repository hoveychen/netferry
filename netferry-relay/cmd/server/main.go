// netferry-server is the remote-side binary deployed to the SSH host.
// It communicates via stdin/stdout using the sshuttle mux protocol.
// Zero external dependencies — only the Go standard library.
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/hoveychen/netferry/relay/internal/mux"
)

var Version = "dev"

func main() {
	autoNets := false
	toNameserver := ""
	verbose := false

	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--auto-nets":
			autoNets = true
		case "--verbose", "-v":
			verbose = true
		case "--to-ns":
			i++
			if i < len(os.Args) {
				toNameserver = os.Args[i]
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

	log.Printf("netferry-server %s on %s/%s", Version, runtime.GOOS, runtime.GOARCH)

	r := os.Stdin
	w := os.Stdout

	// Write synchronisation header — client reads this to confirm server started.
	if err := mux.WriteSyncHeader(w); err != nil {
		fmt.Fprintf(os.Stderr, "s: fatal: write sync header: %v\n", err)
		os.Exit(1)
	}

	// Build routes packet before starting the mux loop.
	routeData := buildRoutePacket(autoNets)

	// Use a pointer so handler closures reference the final srv value.
	var srv *mux.MuxServer

	handlers := mux.ServerHandlers{
		NewTCP: func(channel uint16, family int, dstIP string, dstPort int) {
			log.Printf("TCP %d → %s:%d", channel, dstIP, dstPort)
			srv.HandleTCP(channel, family, dstIP, dstPort)
		},
		DNSReq: func(channel uint16, data []byte) {
			log.Printf("DNS %d len=%d", channel, len(data))
			handleDNS(srv, channel, data, toNameserver)
		},
		UDPOpen: func(channel uint16, family int) {
			log.Printf("UDP open %d family=%d", channel, family)
			handleUDPOpen(srv, channel, family)
		},
	}

	srv = mux.NewMuxServer(r, w, handlers)

	// CMD_ROUTES must be enqueued before Run() so it's the first frame the client sees.
	srv.Send(mux.Frame{Channel: 0, Cmd: mux.CMD_ROUTES, Data: routeData})

	if err := srv.Run(); err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "s: fatal: %v\n", err)
		os.Exit(99)
	}
}

func buildRoutePacket(autoNets bool) []byte {
	if !autoNets {
		return nil // empty routes packet
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

// listRoutes returns available routes in "family,ip,width" format.
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
	if len(fields) == 0 {
		return ""
	}
	dest := fields[0]
	if !strings.Contains(dest, "/") || dest == "default" {
		return ""
	}
	parts := strings.SplitN(dest, "/", 2)
	ip := parts[0]
	width, err := strconv.Atoi(parts[1])
	if err != nil {
		return ""
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

// handleDNS forwards a DNS query to a local or specified nameserver.
func handleDNS(srv *mux.MuxServer, channel uint16, data []byte, toNameserver string) {
	defer srv.CloseChannel(channel)

	ns := "127.0.0.1:53"
	if toNameserver != "" {
		parts := strings.SplitN(toNameserver, "@", 2)
		port := "53"
		if len(parts) == 2 {
			port = parts[1]
		}
		ns = net.JoinHostPort(parts[0], port)
	} else if servers := readResolvConf(); len(servers) > 0 {
		ns = net.JoinHostPort(servers[0], "53")
	}

	conn, err := net.DialTimeout("udp", ns, 5*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(data); err != nil {
		return
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}
	resp := make([]byte, n)
	copy(resp, buf[:n])
	srv.SendTo(channel, mux.CMD_DNS_RESPONSE, resp)
}

// handleUDPOpen creates a UDP proxy for the given channel.
func handleUDPOpen(srv *mux.MuxServer, channel uint16, family int) {
	netFamily := "udp4"
	if family != 2 {
		netFamily = "udp6"
	}
	conn, err := net.ListenPacket(netFamily, ":0")
	if err != nil {
		return
	}
	defer func() {
		conn.Close()
		srv.CloseChannel(channel)
	}()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			udpAddr := addr.(*net.UDPAddr)
			hdr := fmt.Sprintf("%s,%d,", udpAddr.IP.String(), udpAddr.Port)
			out := append([]byte(hdr), buf[:n]...)
			srv.SendTo(channel, mux.CMD_UDP_DATA, out)
		}
	}()

	inbox := srv.InboxFor(channel)
	if inbox == nil {
		return
	}
	for f := range inbox {
		switch f.Cmd {
		case mux.CMD_UDP_DATA:
			parts := strings.SplitN(string(f.Data), ",", 3)
			if len(parts) != 3 {
				continue
			}
			port, _ := strconv.Atoi(parts[1])
			dst := &net.UDPAddr{IP: net.ParseIP(parts[0]), Port: port}
			conn.WriteTo([]byte(parts[2]), dst)
		case mux.CMD_UDP_CLOSE:
			return
		}
	}
}

func readResolvConf() []string {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	var servers []string
	for _, line := range strings.Split(string(data), "\n") {
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
