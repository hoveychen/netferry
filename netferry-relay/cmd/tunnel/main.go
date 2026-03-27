// netferry-tunnel is the local sidecar that:
//   1. Connects to the remote host via SSH
//   2. Deploys netferry-server if not already present (version-cached)
//   3. Sets up local firewall rules (pf on macOS, nft/iptables on Linux)
//   4. Runs a transparent TCP proxy + optional DNS/UDP proxy via the mux protocol
//
// Log output is designed to be parsed by the Tauri sidecar.rs monitor.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hoveychen/netferry/relay/internal/deploy"
	"github.com/hoveychen/netferry/relay/internal/firewall"
	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/netmon"
	"github.com/hoveychen/netferry/relay/internal/proxy"
	"github.com/hoveychen/netferry/relay/internal/sshconn"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

var Version = "dev"

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
		udpProxy     = flag.Bool("udp", false, "enable generic UDP proxy (tproxy only)")
		tproxyMark   = flag.Int("tproxy-mark", 1, "TPROXY fwmark value")
		tproxyTable  = flag.Int("tproxy-table", 100, "TPROXY routing table number")
		verbose      = flag.Bool("v", false, "verbose logging")
		extraSSHOpts = flag.String("extra-ssh-opts", "", "extra SSH options")
		jumpHostsJSON = flag.String("jump", "", "explicit jump hosts as JSON array: [{\"remote\":\"user@host:port\",\"identityFile\":\"/path/to/key\"}]")
		flowCtrl      = flag.Bool("flow-control", false, "enable per-channel flow control (requires matching server)")
		excludeNets   = flag.String("exclude", "", "comma-separated CIDRs to exclude from tunnel")
		showVersion   = flag.Bool("version", false, "print version and exit")
		listFeatures  = flag.Bool("list-features", false, "print method features as JSON and exit")
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
	if dir, err := extractWinDivert(); err != nil {
		log.Printf("windivert extract: %v (WinDivert may not be available)", err)
	} else if dir != "" {
		defer os.RemoveAll(dir)
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

	// ── SSH config resolution ────────────────────────────────────────────────
	hc, err := sshconn.ParseSSHConfig(*remote)
	if err != nil {
		fatalf("ssh config: %v", err)
	}

	ac := sshconn.AuthConfig{
		IdentityFile: *identity,
		ExtraOptions: *extraSSHOpts,
	}

	// Parse explicit jump hosts (overrides ProxyJump from SSH config).
	var jumpHosts []sshconn.JumpHostSpec
	if *jumpHostsJSON != "" {
		if err := json.Unmarshal([]byte(*jumpHostsJSON), &jumpHosts); err != nil {
			fatalf("--jump JSON: %v", err)
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

	// ── Start remote server session ──────────────────────────────────────────
	sess, err := sshClient.NewSession()
	if err != nil {
		fatalf("new ssh session: %v", err)
	}
	defer sess.Close()

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
	if *flowCtrl {
		serverArgs = append(serverArgs, "--flow-control")
	}

	remoteCmd := remotePath
	if len(serverArgs) > 0 {
		remoteCmd += " " + strings.Join(serverArgs, " ")
	}

	sessStdin, err := sess.StdinPipe()
	if err != nil {
		fatalf("session stdin: %v", err)
	}
	sessStdout, err := sess.StdoutPipe()
	if err != nil {
		fatalf("session stdout: %v", err)
	}
	sess.Stderr = os.Stderr

	if err := sess.Start(remoteCmd); err != nil {
		fatalf("start remote server: %v", err)
	}

	// ── Read sync header ─────────────────────────────────────────────────────
	if err := mux.ReadSyncHeader(sessStdout); err != nil {
		fatalf("server handshake: %v — is the deployed binary corrupted?", err)
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
	stopSSHKeepalive := sshconn.StartSSHKeepalive(sshClient, 30*time.Second)
	defer stopSSHKeepalive()

	// ── Start mux client ─────────────────────────────────────────────────────
	muxClient := mux.NewMuxClient(sessStdout, sessStdin)
	muxClient.SetCounters(counters)
	if *flowCtrl {
		muxClient.SetFlowControl(true, mux.DEFAULT_INITIAL_WINDOW)
	}
	muxErrCh := make(chan error, 1)
	go func() {
		muxErrCh <- muxClient.Run()
	}()

	// Collect CMD_ROUTES if --auto-nets (arrives within ~200ms of connect).
	var autoNetRoutes []string
	if *autoNets {
		select {
		case routes := <-muxClient.RoutesCh():
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
		case <-muxClient.RoutesCh():
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
			addr := "127.0.0.1:0"
			if preferred > 0 {
				addr = fmt.Sprintf("127.0.0.1:%d", preferred)
			}
			dnsListener, err = net.ListenPacket("udp", addr)
			if err != nil && preferred > 0 {
				log.Printf("preferred DNS port %d in use, picking a new one", preferred)
				dnsListener, err = net.ListenPacket("udp", "127.0.0.1:0")
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

	// Ensure firewall cleanup on any exit path.
	defer fw.Restore()
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

	// ── Start DNS proxy ───────────────────────────────────────────────────────
	if *dns && dnsListener != nil {
		go func() {
			if err := proxy.ServeDNS(dnsListener, muxClient, counters); err != nil {
				log.Printf("DNS proxy: %v", err)
			}
		}()
	}

	// ── Start UDP proxy (tproxy only) ────────────────────────────────────────
	if *udpProxy && proxy.UseTProxy {
		go func() {
			if err := proxy.ListenUDPTProxy(proxyPort, muxClient, counters); err != nil {
				log.Printf("UDP proxy: %v", err)
			}
		}()
	}

	// ── Start TCP proxy (transparent on Unix, SOCKS5 on Windows) ─────────────
	proxyErrCh := make(chan error, 1)
	go func() {
		proxyErrCh <- proxy.ListenTransparent(proxyPort, muxClient, counters)
	}()

	// ── Monitor network changes (WiFi switch, interface up/down) ─────────
	netmonDone := make(chan struct{})
	defer close(netmonDone)
	netChangeCh := make(chan error, 1)
	go func() {
		netChangeCh <- netmon.Watch(netmonDone)
	}()

	select {
	case err := <-muxErrCh:
		if err != nil {
			log.Printf("mux closed: %v", err)
		}
	case err := <-proxyErrCh:
		if err != nil {
			log.Printf("proxy closed: %v", err)
		}
	case err := <-netChangeCh:
		if err != nil {
			log.Printf("netmon error: %v", err)
		} else {
			log.Printf("network change detected, exiting for reconnect")
		}
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
// it falls back to an OS-assigned port.
func pickFreePort(network string, preferredPort int) int {
	if preferredPort > 0 {
		switch network {
		case "tcp":
			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferredPort))
			if err == nil {
				ln.Close()
				return preferredPort
			}
		case "udp":
			ln, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", preferredPort))
			if err == nil {
				ln.Close()
				return preferredPort
			}
		}
		log.Printf("preferred %s port %d in use, picking a new one", network, preferredPort)
	}

	switch network {
	case "tcp":
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fatalf("pick free TCP port: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		return port
	case "udp":
		ln, err := net.ListenPacket("udp", "127.0.0.1:0")
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
