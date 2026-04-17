// netferry-tunnel is the local sidecar that:
//   1. Connects to the remote host via SSH
//   2. Deploys netferry-server if not already present (version-cached)
//   3. Sets up local firewall rules (pf on macOS, nft/iptables on Linux)
//   4. Runs a transparent TCP proxy + optional DNS/UDP proxy via the mux protocol
//
// Log output is designed to be parsed by the Tauri sidecar.rs monitor.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoveychen/netferry/relay/internal/deploy"
	"github.com/hoveychen/netferry/relay/internal/firewall"
	"github.com/hoveychen/netferry/relay/internal/logfile"
	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/netmon"
	"github.com/hoveychen/netferry/relay/internal/proxy"
	"github.com/hoveychen/netferry/relay/internal/sshconn"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

var Version = "dev"

// serverStderr is the writer used for remote server stderr output.
// It is set up in main() to tee to both os.Stderr and the server log file.
var serverStderr io.Writer = os.Stderr

func main() {
	log.SetFlags(0)
	log.SetPrefix("c : ")

	// ── CLI flags ────────────────────────────────────────────────────────────
	var (
		remote       = flag.String("remote", "", "SSH target: [user@]host[:port]")
		identity     = flag.String("identity", "", "SSH private key path")
		autoNets     = flag.Bool("auto-nets", false, "add remote routes to proxy subnets")
		dns          = flag.Bool("dns", false, "intercept DNS requests")
		dnsTarget    = flag.String("dns-target", "", "remote DNS server IP[@port]")
		method       = flag.String("method", "auto", "firewall method: auto|pf|nft|ipt|tproxy|windivert|socks5")
		noIPv6       = flag.Bool("no-ipv6", false, "disable IPv6 handling")
		noBlockUDP   = flag.Bool("no-block-udp", false, "allow non-DNS UDP (disables QUIC leak prevention)")
		udpProxy     = flag.Bool("udp", false, "enable generic UDP proxy (tproxy only)")
		tproxyMark   = flag.Int("tproxy-mark", 1, "TPROXY fwmark value")
		tproxyTable  = flag.Int("tproxy-table", 100, "TPROXY routing table number")
		verbose      = flag.Bool("v", false, "verbose logging")
		extraSSHOpts = flag.String("extra-ssh-opts", "", "extra SSH options")
		jumpHostsJSON = flag.String("jump", "", "explicit jump hosts as JSON array: [{\"remote\":\"user@host:port\",\"identityFile\":\"/path/to/key\"}]")
		excludeNets   = flag.String("exclude", "", "comma-separated CIDRs to exclude from tunnel")
		poolSize      = flag.Int("pool", 1, "number of parallel SSH TCP connections for connection bonding (1 = disabled; use 2-4 for high-concurrency workloads)")
		splitConn     = flag.Bool("split", false, "open a second SSH connection per pool member to carry smux control frames (SYN/NOP/UPD) separately from data frames (PSH/FIN), preventing bulk data from delaying window updates")
		tcpBalance    = flag.String("tcp-balance", "least-loaded", "TCP load-balancing strategy across pool members: round-robin|least-loaded")
		showVersion   = flag.Bool("version", false, "print version and exit")
		listFeatures  = flag.Bool("list-features", false, "print method features as JSON and exit")
		noNetmon      = flag.Bool("no-netmon", false, "disable network-change monitor (use on stable servers that never switch networks)")
	)
	flag.Parse()
	subnets := flag.Args()

	if *showVersion {
		fmt.Println(Version)
		os.Exit(0)
	}
	if *listFeatures {
		features := firewall.ListMethodFeatures()
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(features)
		os.Exit(0)
	}

	// Extract embedded WinDivert DLL on Windows (no-op on other platforms).
	// Files are placed in a fixed, content-addressed directory and deliberately
	// NOT cleaned up — they are reused across restarts and tolerate a locked
	// .sys from a previous crash.
	if _, err := extractWinDivert(); err != nil {
		log.Printf("windivert extract: %v (WinDivert may not be available)", err)
	}

	if *remote == "" {
		fmt.Fprintln(os.Stderr, "fatal: --remote is required")
		flag.Usage()
		os.Exit(1)
	}

	if !*verbose {
		// Keep stderr output, but suppress extra debug noise.
		log.SetOutput(os.Stderr)
	}

	// ── Log files (client.log + server.log with size-based rotation) ────────
	if logDir, err := os.UserCacheDir(); err == nil {
		logDir = filepath.Join(logDir, "netferry", "logs")
		if cw, err := logfile.New(filepath.Join(logDir, "client.log"), logfile.DefaultMaxSize, logfile.DefaultMaxBackups); err == nil {
			log.SetOutput(io.MultiWriter(log.Writer(), cw))
		}
		if sw, err := logfile.New(filepath.Join(logDir, "server.log"), logfile.DefaultMaxSize, logfile.DefaultMaxBackups); err == nil {
			serverStderr = io.MultiWriter(os.Stderr, sw)
		}
	}

	// ── SSH config resolution ────────────────────────────────────────────────
	hc, err := sshconn.ParseSSHConfig(*remote)
	if err != nil {
		fatalf("ssh config: %v", err)
	}

	ac := sshconn.AuthConfig{
		IdentityFile: *identity,
		IdentityPEM:  os.Getenv("NETFERRY_IDENTITY_PEM"),
		ExtraOptions: *extraSSHOpts,
	}

	// Parse explicit jump hosts (overrides ProxyJump from SSH config).
	var jumpHosts []sshconn.JumpHostSpec
	if *jumpHostsJSON != "" {
		if err := json.Unmarshal([]byte(*jumpHostsJSON), &jumpHosts); err != nil {
			fatalf("--jump JSON: %v", err)
		}
	}
	// Populate inline PEM keys from env vars (set by Tauri app; never on disk).
	for i := range jumpHosts {
		if pem := os.Getenv(fmt.Sprintf("NETFERRY_JUMP_KEY_%d", i)); pem != "" {
			jumpHosts[i].IdentityPEM = pem
		}
	}

	// ── SSH connection ───────────────────────────────────────────────────────
	log.Printf("connecting to %s@%s:%d", hc.User, hc.HostName, hc.Port)
	sshClient, err := sshconn.Dial(hc, ac, jumpHosts...)
	if err != nil {
		fatalf("ssh connect: %v", err)
	}
	defer sshClient.Close()

	// SSH server IP must be excluded from firewall rules to prevent loop.
	sshServerIP := deploy.RemoteIP(sshClient)
	excludes := []string{
		sshServerIP + "/32",
		"127.0.0.0/8",
		"169.254.0.0/16",
	}
	if !*noIPv6 {
		// Exclude IPv6 loopback and link-local.
		excludes = append(excludes, "::1/128", "fe80::/10")
	}
	if *excludeNets != "" {
		for _, cidr := range strings.Split(*excludeNets, ",") {
			cidr = strings.TrimSpace(cidr)
			if cidr != "" {
				excludes = append(excludes, cidr)
			}
		}
	}

	// ── Deploy server binary ─────────────────────────────────────────────────
	remotePath, err := deploy.EnsureServer(sshClient, Version)
	if err != nil {
		fatalf("deploy server: %v", err)
	}
	log.Printf("remote server: %s", remotePath)

	// ── Build server command ─────────────────────────────────────────────────
	var serverArgs []string
	if *autoNets {
		serverArgs = append(serverArgs, "--auto-nets")
	}
	if *dnsTarget != "" {
		serverArgs = append(serverArgs, "--to-ns", *dnsTarget)
	}
	if *verbose {
		serverArgs = append(serverArgs, "--verbose")
	}
	remoteCmd := remotePath
	if len(serverArgs) > 0 {
		remoteCmd += " " + strings.Join(serverArgs, " ")
	}

	// ── Load cached ports for stability across reconnections ────────────────
	cachedPorts := loadPortCache()

	// ── Create stats counters and start HTTP/SSE server ──────────────────────
	counters := stats.NewCounters()
	statsPort, err := counters.ListenAndServe(cachedPorts.StatsPort)
	if err != nil {
		fatalf("stats server: %v", err)
	}
	// Print the port on stderr so the Tauri sidecar can pick it up.
	fmt.Fprintf(os.Stderr, "c : stats-port: %d\n", statsPort)

	// ── SSH-level keepalive ───────────────────────────────────────────────────
	// Sends keepalive@openssh.com global requests so the SSH transport detects
	// dead TCP connections promptly (critical on Windows where the OS may not
	// surface TCP errors without an explicit write).
	// The first sshClient is already connected; keepalive is started after all
	// pool connections are established so we can cover additional clients too.

	// ── Start mux pool (N parallel SSH TCP connections) ───────────────────────
	// Each pool member is a separate SSH client (separate TCP connection to the
	// server). Multiple sessions on the same ssh.Client would share one TCP
	// connection and provide no bonding benefit.
	n := *poolSize
	if n < 1 {
		n = 1
	}
	// sshClients[0] is the already-dialed sshClient; additional ones are dialed now.
	sshClients := make([]*ssh.Client, n)
	sshClients[0] = sshClient
	for i := 1; i < n; i++ {
		extra, err := sshconn.Dial(hc, ac, jumpHosts...)
		if err != nil {
			fatalf("ssh connect (pool %d/%d): %v", i+1, n, err)
		}
		defer extra.Close()
		sshClients[i] = extra
	}

	clients := make([]*mux.MuxClient, n)
	muxErrCh := make(chan error, 1)
	for i, sc := range sshClients {
		// Register per-tunnel counters before keepalive so RTT can be reported.
		var tc *stats.TunnelCounters
		if n > 1 {
			tc = counters.RegisterTunnel(i + 1)
		}

		// Start SSH-level keepalive with per-tunnel RTT measurement.
		rttCb := buildRTTCallback(counters, tc, i == 0)
		stop := sshconn.StartSSHKeepalive(sc, 30*time.Second, rttCb)
		defer stop()

		var c *mux.MuxClient
		if *splitConn {
			c = startSplitMuxClient(sc, hc, ac, jumpHosts, remoteCmd, i+1, n)
		} else {
			mc, err := tryMuxClient(sc, remoteCmd, i+1, n)
			if err != nil {
				fatalf("%v", err)
			}
			c = mc
		}
		c.SetCounters(counters)
		if tc != nil {
			c.SetTunnelIndex(i+1, tc)
		}
		clients[i] = c
	}
	if *splitConn {
		log.Printf("mux: split-conn enabled (data/ctrl on separate TCP connections)")
	}
	if n > 1 {
		log.Printf("mux pool: %d parallel TCP connections", n)
	}

	// Use the first client to collect routes; all sessions connect to the same
	// server so routes are identical across all.
	firstClient := clients[0]
	var tunnelClient mux.TunnelClient
	if n == 1 {
		// Single tunnel: its death exits the tunnel process (Tauri will reconnect).
		go func() { muxErrCh <- firstClient.Run() }()
		tunnelClient = firstClient
	} else {
		strategy := mux.LBLeastLoaded
		if *tcpBalance == "round-robin" {
			strategy = mux.LBRoundRobin
		}
		pool := mux.NewMuxPoolWithStrategy(clients, strategy)
		tunnelClient = pool
		// All pool members reconnect independently on death.
		// The process only exits if all members die at the same time (network-level failure).
		for i := 0; i < n; i++ {
			idx := i
			go reconnectPoolMember(pool, idx, n, clients[idx], hc, ac, jumpHosts, remoteCmd, *splitConn, counters, muxErrCh)
		}
	}

	// Collect CMD_ROUTES if --auto-nets (arrives within ~200ms of connect).
	var autoNetRoutes []string
	if *autoNets {
		select {
		case routes := <-firstClient.RoutesCh():
			autoNetRoutes = routes
			log.Printf("auto-nets: %d routes received", len(autoNetRoutes))
		case <-time.After(5 * time.Second):
			log.Printf("auto-nets: timeout waiting for routes")
		case err := <-muxErrCh:
			fatalf("mux: %v", err)
		}
	} else {
		// Drain the empty CMD_ROUTES sent by the server unconditionally.
		select {
		case <-firstClient.RoutesCh():
		case <-time.After(3 * time.Second):
		case err := <-muxErrCh:
			fatalf("mux: %v", err)
		}
	}

	allSubnetStrings := append(subnets, autoNetRoutes...)
	if len(allSubnetStrings) == 0 {
		fatalf("no subnets to proxy — specify at least one CIDR (e.g. 0.0.0.0/0)")
	}

	// Parse subnet rules (with optional port ranges).
	effectiveSubnets, err := firewall.ParseSubnetRules(allSubnetStrings)
	if err != nil {
		fatalf("parse subnets: %v", err)
	}

	// Filter out IPv6 subnets if --no-ipv6.
	if *noIPv6 {
		var v4Only []firewall.SubnetRule
		for _, s := range effectiveSubnets {
			if !s.IsIPv6() {
				v4Only = append(v4Only, s)
			}
		}
		effectiveSubnets = v4Only
	}

	// ── Firewall setup ───────────────────────────────────────────────────────
	firewall.CleanStaleAnchors()

	var fw firewall.Method
	if *method == "auto" {
		fw = firewall.NewAuto()
	} else {
		fw, err = firewall.New(*method)
		if err != nil {
			fatalf("firewall: %v", err)
		}
	}
	// Apply UDP blocking (default on; prevents QUIC leaks on pf).
	firewall.SetUDPBlock(fw, !*noBlockUDP)
	// Apply TPROXY configuration if applicable.
	firewall.SetTProxyConfig(fw, firewall.TProxyConfig{
		FWMark:     *tproxyMark,
		RouteTable: *tproxyTable,
	})
	log.Printf("firewall: using %s (features: %v)", fw.Name(), fw.SupportedFeatures())

	// Validate feature requirements.
	hasIPv6Subnets := false
	hasPortRange := false
	for _, s := range effectiveSubnets {
		if s.IsIPv6() {
			hasIPv6Subnets = true
		}
		if s.HasPortRange() {
			hasPortRange = true
		}
	}
	if hasIPv6Subnets && !firewall.Supports(fw, firewall.FeatureIPv6) {
		fatalf("firewall method %q does not support IPv6; remove IPv6 subnets or use a different method", fw.Name())
	}
	if hasPortRange && !firewall.Supports(fw, firewall.FeaturePortRange) {
		fatalf("firewall method %q does not support port ranges; remove port ranges or use a different method", fw.Name())
	}
	if *udpProxy && !firewall.Supports(fw, firewall.FeatureUDP) {
		fatalf("firewall method %q does not support UDP proxy; use --method=tproxy for UDP support", fw.Name())
	}

	// Configure proxy mode based on the selected firewall method.
	proxy.BindAddr = firewall.ProxyBindAddrFor(fw)
	switch fw.Name() {
	case "tproxy":
		proxy.UseTProxy = true
		// TPROXY preserves original dest in conn.LocalAddr(); no QueryOrigDstFunc needed.
		proxy.QueryOrigDstFunc = nil
	case "windivert":
		proxy.QueryOrigDstFunc = firewall.QueryOrigDstFor(fw)
	}

	var dnsServers []string
	dnsPort := 0
	var dnsListener net.PacketConn
	if *dns {
		dnsServers = proxy.DetectDNSServers()
		// Bind the DNS listener BEFORE installing firewall rules so that
		// redirected DNS packets never arrive at an unbound port (which would
		// cause ICMP port-unreachable → "DNS probe failed" in browsers).
		var err error
		if proxy.UseTProxy {
			// TPROXY does not rewrite packet headers — the socket must
			// have IP_TRANSPARENT and bind to 0.0.0.0 to accept packets
			// with non-local destination addresses.
			dnsListener, err = proxy.ListenDNSTProxy(cachedPorts.DNSPort)
			if err != nil && cachedPorts.DNSPort > 0 {
				log.Printf("preferred DNS tproxy port %d in use, picking a new one", cachedPorts.DNSPort)
				dnsListener, err = proxy.ListenDNSTProxy(0)
			}
		} else {
			preferred := cachedPorts.DNSPort
			dnsBindAddr := proxy.BindAddr
			addr := fmt.Sprintf("%s:0", dnsBindAddr)
			if preferred > 0 {
				addr = fmt.Sprintf("%s:%d", dnsBindAddr, preferred)
			}
			dnsListener, err = net.ListenPacket("udp", addr)
			if err != nil && preferred > 0 {
				log.Printf("preferred DNS port %d in use, picking a new one", preferred)
				dnsListener, err = net.ListenPacket("udp", fmt.Sprintf("%s:0", dnsBindAddr))
			}
		}
		if err != nil {
			fatalf("dns listen: %v", err)
		}
		dnsPort = dnsListener.LocalAddr().(*net.UDPAddr).Port
		log.Printf("DNS: servers=%v localPort=%d", dnsServers, dnsPort)
	}

	proxyPort := pickFreePort("tcp", cachedPorts.ProxyPort)

	// ── Save ports for next reconnection ────────────────────────────────────
	savePortCache(portCache{
		ProxyPort: proxyPort,
		DNSPort:   dnsPort,
		StatsPort: statsPort,
	})

	if err := fw.Setup(effectiveSubnets, excludes, proxyPort, dnsPort, dnsServers); err != nil {
		fatalf("firewall setup: %v", err)
	}

	// Ensure firewall cleanup on any exit path — unless we are exiting for
	// reconnect, in which case we deliberately keep the rules in place so
	// traffic is blocked (redirected to the dead proxy port) rather than
	// leaking to the public internet during the reconnect window.
	skipFWRestore := false
	defer func() {
		if !skipFWRestore {
			fw.Restore()
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		s := <-sig
		log.Printf("received signal %v, cleaning up", s)
		fw.Restore()
		os.Exit(0)
	}()

	// ── Signal tunnel is ready (Tauri sidecar.rs watches for this exact line) ─
	fmt.Fprintln(os.Stderr, "c : Connected to server.")

	// Flush DNS cache BEFORE resetting TCP connections. Order matters:
	// 1. FlushDNSCache — discard stale/poisoned DNS entries
	// 2. FlushExistingConnections — RST existing TCP connections
	// If we reset TCP first, apps reconnect immediately using the still-
	// poisoned DNS cache and hit the wrong IPs. By flushing DNS first,
	// reconnecting apps re-resolve through the tunnel's DNS interceptor.
	firewall.FlushDNSCache()
	firewall.FlushExistingConnections(effectiveSubnets)

	// ── Start DNS proxy ───────────────────────────────────────────────────────
	if *dns && dnsListener != nil {
		go func() {
			if err := proxy.ServeDNS(dnsListener, tunnelClient, counters); err != nil {
				log.Printf("DNS proxy: %v", err)
			}
		}()
	}

	// ── Start UDP proxy (tproxy only) ────────────────────────────────────────
	if *udpProxy && proxy.UseTProxy {
		go func() {
			if err := proxy.ListenUDPTProxy(proxyPort, tunnelClient, counters); err != nil {
				log.Printf("UDP proxy: %v", err)
			}
		}()
	}

	// ── Start TCP proxy (transparent on Unix, SOCKS5 on Windows) ─────────────
	proxyErrCh := make(chan error, 1)
	go func() {
		proxyErrCh <- proxy.ListenTransparent(proxyPort, tunnelClient, counters)
	}()

	// ── Monitor network changes (WiFi switch, interface up/down) ─────────
	netmonDone := make(chan struct{})
	defer close(netmonDone)
	netChangeCh := make(chan error, 1)
	if !*noNetmon {
		go func() {
			netChangeCh <- netmon.Watch(netmonDone)
		}()
	}

	select {
	case err := <-muxErrCh:
		if err != nil {
			log.Printf("mux closed: %v", err)
		}
		// Mux dying means the SSH connection dropped — keep TCP redirect
		// rules so traffic is blocked rather than leaking during reconnect,
		// but remove DNS redirect rules so the reconnecting tunnel process
		// can resolve the SSH server hostname via normal system DNS.
		firewall.DisableDNSRedirect(fw)
		skipFWRestore = true
		fmt.Fprintln(os.Stderr, "c : exit-for-reconnect")
	case err := <-proxyErrCh:
		if err != nil {
			log.Printf("proxy closed: %v", err)
		}
		// Local proxy error — not a network issue, restore firewall.
	case err := <-netChangeCh:
		if err != nil {
			log.Printf("netmon error: %v", err)
		} else {
			log.Printf("network change detected, exiting for reconnect")
		}
		// Network change — keep TCP redirect rules during reconnect window,
		// but remove DNS redirect rules so the reconnecting tunnel process
		// can resolve the SSH server hostname via normal system DNS.
		firewall.DisableDNSRedirect(fw)
		skipFWRestore = true
		fmt.Fprintln(os.Stderr, "c : exit-for-reconnect")
	}
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "fatal: "+format+"\n", args...)
	os.Exit(1)
}

// portCache stores previously used ports so reconnections reuse the same ports
// when possible.
type portCache struct {
	ProxyPort int `json:"proxy_port,omitempty"`
	DNSPort   int `json:"dns_port,omitempty"`
	StatsPort int `json:"stats_port,omitempty"`
}

func portCachePath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "netferry", "ports.json")
	}
	return ""
}

func loadPortCache() portCache {
	path := portCachePath()
	if path == "" {
		return portCache{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return portCache{}
	}
	var pc portCache
	json.Unmarshal(data, &pc)
	return pc
}

func savePortCache(pc portCache) {
	path := portCachePath()
	if path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	data, _ := json.Marshal(pc)
	os.WriteFile(path, data, 0o644)
}

// pickFreePort tries to bind to preferredPort first; if that fails (or is 0),
// it falls back to an OS-assigned port. Uses proxy.BindAddr for TCP.
func pickFreePort(network string, preferredPort int) int {
	bindAddr := proxy.BindAddr

	if preferredPort > 0 {
		switch network {
		case "tcp":
			ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", bindAddr, preferredPort))
			if err == nil {
				ln.Close()
				return preferredPort
			}
		case "udp":
			ln, err := net.ListenPacket("udp", fmt.Sprintf("%s:%d", bindAddr, preferredPort))
			if err == nil {
				ln.Close()
				return preferredPort
			}
		}
		log.Printf("preferred %s port %d in use, picking a new one", network, preferredPort)
	}

	switch network {
	case "tcp":
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:0", bindAddr))
		if err != nil {
			fatalf("pick free TCP port: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		return port
	case "udp":
		ln, err := net.ListenPacket("udp", fmt.Sprintf("%s:0", bindAddr))
		if err != nil {
			fatalf("pick free UDP port: %v", err)
		}
		port := ln.LocalAddr().(*net.UDPAddr).Port
		ln.Close()
		return port
	default:
		panic("unknown network: " + network)
	}
}

// newSessionID returns a short random hex string used to coordinate the data
// and ctrl SSH sessions in split-conn mode.
func newSessionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("rand: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// startSplitMuxClient is the fatal wrapper around trySplitMuxClient for
// initial connection setup.
func startSplitMuxClient(
	sc *ssh.Client,
	hc *sshconn.HostConfig,
	ac sshconn.AuthConfig,
	jumpHosts []sshconn.JumpHostSpec,
	remoteCmd string,
	member, total int,
) *mux.MuxClient {
	c, err := trySplitMuxClient(sc, hc, ac, jumpHosts, remoteCmd, member, total)
	if err != nil {
		fatalf("%v", err)
	}
	return c
}

// tryMuxClient creates a non-split MuxClient on the given SSH connection.
func tryMuxClient(sc *ssh.Client, remoteCmd string, member, total int) (*mux.MuxClient, error) {
	sess, err := sc.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new ssh session %d/%d: %w", member, total, err)
	}
	sessStdin, err := sess.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("session %d stdin: %w", member, err)
	}
	sessStdout, err := sess.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("session %d stdout: %w", member, err)
	}
	sess.Stderr = serverStderr
	if err := sess.Start(remoteCmd); err != nil {
		return nil, fmt.Errorf("start remote server (session %d): %w", member, err)
	}
	if err := mux.ReadSyncHeader(sessStdout); err != nil {
		return nil, fmt.Errorf("server handshake (session %d): %w", member, err)
	}
	return mux.NewMuxClient(sessStdout, sessStdin), nil
}

// trySplitMuxClient opens two SSH sessions (data + ctrl) and returns a
// split-conn MuxClient. Returns an error instead of calling fatalf so it
// can be used in reconnection loops.
func trySplitMuxClient(
	sc *ssh.Client,
	hc *sshconn.HostConfig,
	ac sshconn.AuthConfig,
	jumpHosts []sshconn.JumpHostSpec,
	remoteCmd string,
	member, total int,
) (*mux.MuxClient, error) {
	sid := newSessionID()

	// ── data session ─────────────────────────────────────────────────────────
	dataSess, err := sc.NewSession()
	if err != nil {
		return nil, fmt.Errorf("split data session %d/%d: %w", member, total, err)
	}
	dataStdin, err := dataSess.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("split data session %d/%d stdin: %w", member, total, err)
	}
	dataStdout, err := dataSess.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("split data session %d/%d stdout: %w", member, total, err)
	}
	dataSess.Stderr = serverStderr

	dataCmd := remoteCmd + " --session-id " + sid + " --role main"
	if err := dataSess.Start(dataCmd); err != nil {
		return nil, fmt.Errorf("split data session %d/%d start: %w", member, total, err)
	}

	// ── ctrl session ──────────────────────────────────────────────────────────
	ctrlClient, err := sshconn.Dial(hc, ac, jumpHosts...)
	if err != nil {
		return nil, fmt.Errorf("split ctrl SSH connect %d/%d: %w", member, total, err)
	}
	sshconn.StartSSHKeepalive(ctrlClient, 30*time.Second, nil)

	ctrlSess, err := ctrlClient.NewSession()
	if err != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split ctrl session %d/%d: %w", member, total, err)
	}
	ctrlStdin, err := ctrlSess.StdinPipe()
	if err != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split ctrl session %d/%d stdin: %w", member, total, err)
	}
	ctrlStdout, err := ctrlSess.StdoutPipe()
	if err != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split ctrl session %d/%d stdout: %w", member, total, err)
	}
	ctrlSess.Stderr = serverStderr

	ctrlCmd := remoteCmd
	if idx := strings.IndexByte(remoteCmd, ' '); idx >= 0 {
		ctrlCmd = remoteCmd[:idx]
	}
	ctrlCmd += " --session-id " + sid + " --role ctrl"
	if err := ctrlSess.Start(ctrlCmd); err != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split ctrl session %d/%d start: %w", member, total, err)
	}

	// ── read sync headers concurrently ────────────────────────────────────────
	var syncErr [2]error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		syncErr[0] = mux.ReadSyncHeader(dataStdout)
	}()
	go func() {
		defer wg.Done()
		syncErr[1] = mux.ReadSyncHeader(ctrlStdout)
	}()
	wg.Wait()

	if syncErr[0] != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split data handshake %d/%d: %w", member, total, syncErr[0])
	}
	if syncErr[1] != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split ctrl handshake %d/%d: %w", member, total, syncErr[1])
	}

	return mux.NewMuxClientSplit(dataStdout, dataStdin, ctrlStdout, ctrlStdin), nil
}

// connectPoolMember dials a fresh SSH connection and creates a MuxClient.
// Used by reconnectPoolMember for reconnecting dead secondary pool members.
func connectPoolMember(
	hc *sshconn.HostConfig,
	ac sshconn.AuthConfig,
	jumpHosts []sshconn.JumpHostSpec,
	remoteCmd string,
	split bool,
	counters *stats.Counters,
	member, total int,
) (*mux.MuxClient, error) {
	sc, err := sshconn.Dial(hc, ac, jumpHosts...)
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}
	tc := counters.TunnelCounterAt(member)
	sshconn.StartSSHKeepalive(sc, 30*time.Second, buildRTTCallback(counters, tc, false))

	var c *mux.MuxClient
	if split {
		c, err = trySplitMuxClient(sc, hc, ac, jumpHosts, remoteCmd, member, total)
	} else {
		c, err = tryMuxClient(sc, remoteCmd, member, total)
	}
	if err != nil {
		sc.Close()
		return nil, err
	}
	c.SetCounters(counters)
	if tc != nil {
		c.SetTunnelIndex(member, tc)
	}
	return c, nil
}

// maxReconnectAttempts is the number of consecutive reconnect failures before
// a pool member gives up and signals a fatal error (triggering a full tunnel
// restart via Tauri).
const maxReconnectAttempts = 10

// reconnectPoolMember runs the given MuxClient and, when it dies, reconnects
// with exponential backoff and replaces it in the pool. If reconnection fails
// maxReconnectAttempts times in a row, it sends a fatal error on fatalCh so
// the process can exit and let Tauri restart the whole tunnel.
func reconnectPoolMember(
	pool *mux.MuxPool,
	idx, total int,
	initial *mux.MuxClient,
	hc *sshconn.HostConfig,
	ac sshconn.AuthConfig,
	jumpHosts []sshconn.JumpHostSpec,
	remoteCmd string,
	split bool,
	counters *stats.Counters,
	fatalCh chan<- error,
) {
	c := initial
	backoff := 5 * time.Second
	const maxBackoff = 60 * time.Second

	// Update tunnel state.
	if tc := counters.TunnelCounterAt(idx + 1); tc != nil {
		tc.SetState(stats.TunnelAlive)
	}

	for {
		if err := c.Run(); err != nil {
			log.Printf("mux pool member %d/%d closed: %v", idx+1, total, err)
		}

		if tc := counters.TunnelCounterAt(idx + 1); tc != nil {
			tc.SetState(stats.TunnelReconnecting)
		}

		// Reconnect loop with exponential backoff.
		attempts := 0
		for {
			attempts++
			if attempts > maxReconnectAttempts {
				log.Printf("mux pool member %d/%d: giving up after %d failed reconnect attempts", idx+1, total, maxReconnectAttempts)
				if tc := counters.TunnelCounterAt(idx + 1); tc != nil {
					tc.SetState(stats.TunnelDead)
				}
				select {
				case fatalCh <- fmt.Errorf("pool member %d/%d: exhausted %d reconnect attempts", idx+1, total, maxReconnectAttempts):
				default:
				}
				return
			}

			log.Printf("mux pool member %d/%d: reconnecting in %v (attempt %d/%d)", idx+1, total, backoff, attempts, maxReconnectAttempts)
			time.Sleep(backoff)

			newClient, err := connectPoolMember(hc, ac, jumpHosts, remoteCmd, split, counters, idx+1, total)
			if err != nil {
				log.Printf("mux pool member %d/%d reconnect failed: %v", idx+1, total, err)
				backoff = min(backoff*2, maxBackoff)
				continue
			}

			pool.ReplaceClient(idx, newClient)
			c = newClient
			backoff = 5 * time.Second
			if tc := counters.TunnelCounterAt(idx + 1); tc != nil {
				tc.SetState(stats.TunnelAlive)
			}
			log.Printf("mux pool member %d/%d: reconnected successfully", idx+1, total)
			break
		}
	}
}

// buildRTTCallback returns a keepalive RTT observer that records measurements
// in both the per-tunnel counter (if non-nil) and, when primary is true, the
// global counter (for backward-compatible display when pool size == 1).
func buildRTTCallback(counters *stats.Counters, tc *stats.TunnelCounters, primary bool) func(time.Duration) {
	return func(rtt time.Duration) {
		if tc != nil {
			tc.ObserveRTT(rtt)
		}
		if primary {
			counters.ObserveKeepaliveRTT(rtt)
		}
	}
}
