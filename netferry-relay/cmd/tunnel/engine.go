package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoveychen/netferry/relay/internal/firewall"
	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/netmon"
	"github.com/hoveychen/netferry/relay/internal/proxy"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// ErrExitForReconnect signals that the engine stopped because the SSH mux
// died, the network changed, or another condition where the caller should
// re-create the engine to reconnect. Firewall rules are deliberately left in
// place when this is returned — the caller must spin up a new engine quickly
// to avoid leaking traffic during the reconnect window.
var ErrExitForReconnect = errors.New("exit for reconnect")

// EngineConfig is the fully-resolved input to Engine.Run. It is built either
// by cliconfig.go (from CLI flags + .nfprofile / group file) or programmatically
// by the TUI (from a stored profile).
type EngineConfig struct {
	// Backends holds one config per profile to bring up. Length 1 in solo
	// mode, length N in group mode.
	Backends []*backendConfig

	// GroupFile is non-nil in group mode and carries the rules map that
	// drives the SessionManager's per-destination routing.
	GroupFile *GroupFile

	// SubnetStrings is the union of CIDRs (and CIDR:port-range) the proxy
	// should capture. In group mode this is typically the union of children's
	// subnets; in solo mode it's the CLI positional args (or profile.Subnets).
	SubnetStrings []string

	// Process-wide knobs.
	FirewallMethod string // auto | pf | nft | ipt | tproxy | windivert | socks5
	AutoNets       bool
	DNSEnabled     bool
	DNSTarget      string
	UDPProxy       bool
	NoIPv6         bool
	NoIPv6Lockdown bool
	NoBlockUDP     bool
	ExcludeNets    []string
	TProxyMark     int
	TProxyTable    int
	Verbose        bool
}

// Engine runs one tunnel session: SSH+deploy → mux pool → firewall → proxy.
// One Engine per session; create a fresh one to reconnect.
type Engine struct {
	cfg *EngineConfig

	counters  *stats.Counters
	statsPort int

	// readyCh is closed exactly once when Run() either reaches the
	// "Connected to server." milestone (firewall installed, proxy listening,
	// mux up) or returns early. readyOK distinguishes the two cases: true
	// means we actually made it operational; false means Run errored before
	// the milestone (e.g. firewall setup failed because we're not root).
	readyCh   chan struct{}
	readyOnce sync.Once
	readyOK   atomic.Bool
}

// NewEngine creates an engine and starts the stats HTTP/SSE server so the
// caller (Tauri sidecar or TUI) can discover the port immediately. SSH is
// not yet dialed.
func NewEngine(cfg *EngineConfig) (*Engine, error) {
	cachedPorts := loadPortCache()

	counters := stats.NewCounters()
	statsPort, err := counters.ListenAndServe(cachedPorts.StatsPort)
	if err != nil {
		return nil, fmt.Errorf("stats server: %w", err)
	}
	// Tauri sidecar.rs greps stderr for this exact prefix.
	fmt.Fprintf(os.Stderr, "c : stats-port: %d\n", statsPort)

	// Extract embedded WinDivert DLL on Windows (no-op on other platforms).
	// Files are placed in a fixed, content-addressed directory and deliberately
	// NOT cleaned up — they are reused across restarts and tolerate a locked
	// .sys from a previous crash.
	if _, err := extractWinDivert(); err != nil {
		log.Printf("windivert extract: %v (WinDivert may not be available)", err)
	}

	return &Engine{
		cfg:       cfg,
		counters:  counters,
		statsPort: statsPort,
		readyCh:   make(chan struct{}),
	}, nil
}

// StatsPort returns the port the stats HTTP/SSE server is listening on.
func (e *Engine) StatsPort() int { return e.statsPort }

// Counters returns the live stats counters (for in-process readers like the TUI).
func (e *Engine) Counters() *stats.Counters { return e.counters }

// ReadyCh returns a channel that closes when Run() either reaches the
// "Connected to server." milestone or returns early. After it closes,
// ReadyOK() reports which path was taken — readers waiting on the engine
// should consult ReadyOK to tell "actually operational" from "ended early".
func (e *Engine) ReadyCh() <-chan struct{} { return e.readyCh }

// ReadyOK reports whether the engine reached the operational milestone. Only
// meaningful after ReadyCh() has closed.
func (e *Engine) ReadyOK() bool { return e.readyOK.Load() }

// signalReady marks the engine operational and unblocks ReadyCh() listeners.
// Safe to call multiple times; only the first call has effect.
func (e *Engine) signalReady() {
	e.readyOK.Store(true)
	e.readyOnce.Do(func() { close(e.readyCh) })
}

// signalEnded unblocks ReadyCh() listeners without marking the engine
// operational; used in the Run() defer so callers blocked on ReadyCh do not
// leak when Run returns early.
func (e *Engine) signalEnded() {
	e.readyOnce.Do(func() { close(e.readyCh) })
}

// Run brings up the tunnel and blocks until shutdown.
// stopCh, when closed, requests graceful shutdown (firewall + IPv6 are restored).
// Returns ErrExitForReconnect when the caller should recreate the engine to
// reconnect (firewall rules are intentionally kept in place in this case to
// prevent traffic leaks during the reconnect window).
func (e *Engine) Run(stopCh <-chan struct{}) error {
	defer e.signalEnded()
	cfg := e.cfg
	cachedPorts := loadPortCache()

	// ── Build remote server command (shared across all backends) ────────────
	var serverArgs []string
	if cfg.AutoNets {
		serverArgs = append(serverArgs, "--auto-nets")
	}
	if cfg.DNSTarget != "" {
		serverArgs = append(serverArgs, "--to-ns", cfg.DNSTarget)
	}
	if cfg.Verbose {
		serverArgs = append(serverArgs, "--verbose")
	}

	if cfg.GroupFile != nil {
		e.counters.SetActiveGroup(buildActiveGroupFromFile(cfg.GroupFile))
	}

	// ── Connect backends ────────────────────────────────────────────────────
	muxErrCh := make(chan error, 1)
	backends := make([]*backend, 0, len(cfg.Backends))
	for i, bc := range cfg.Backends {
		b, err := connectBackend(bc, serverArgs, e.counters, muxErrCh, i == 0)
		if err != nil {
			return fmt.Errorf("backend %q: %w", bc.profileID, err)
		}
		backends = append(backends, b)
	}

	// ── Build excludes union ─────────────────────────────────────────────────
	excludes := []string{
		"127.0.0.0/8",
		"169.254.0.0/16",
	}
	if !cfg.NoIPv6 {
		excludes = append(excludes, "::1/128", "fe80::/10")
	}
	for _, cidr := range cfg.ExcludeNets {
		cidr = strings.TrimSpace(cidr)
		if cidr != "" {
			excludes = append(excludes, cidr)
		}
	}
	seenEx := make(map[string]bool, len(excludes))
	for _, x := range excludes {
		seenEx[x] = true
	}
	addEx := func(c string) {
		c = strings.TrimSpace(c)
		if c != "" && !seenEx[c] {
			excludes = append(excludes, c)
			seenEx[c] = true
		}
	}
	for _, b := range backends {
		addEx(b.sshServerIP + "/32")
		for _, x := range b.cfg.extraExcludes {
			addEx(x)
		}
	}

	// ── Build tunnel client ──────────────────────────────────────────────────
	// Single backend → use the mux client directly (legacy path, cheap).
	// Multiple backends → SessionManager routes each destination to the
	// right profile's pool based on stats.routeModes; DNS/UDP go to default.
	var tunnelClient mux.TunnelClient
	if len(backends) == 1 {
		tunnelClient = backends[0].client
	} else {
		sm := mux.NewSessionManager(e.counters)
		for _, b := range backends {
			sm.Register(b.cfg.profileID, b.client.(*mux.MuxPool))
		}
		if cfg.GroupFile != nil {
			sm.SetDefault(cfg.GroupFile.DefaultProfileID)
		}
		tunnelClient = sm
	}
	firstClient := backends[0].firstClient

	// Collect CMD_ROUTES if --auto-nets (arrives within ~200ms of connect).
	var autoNetRoutes []string
	if cfg.AutoNets {
		select {
		case routes := <-firstClient.RoutesCh():
			autoNetRoutes = routes
			log.Printf("auto-nets: %d routes received", len(autoNetRoutes))
		case <-time.After(5 * time.Second):
			log.Printf("auto-nets: timeout waiting for routes")
		case err := <-muxErrCh:
			return fmt.Errorf("mux: %w", err)
		}
	} else {
		// Drain the empty CMD_ROUTES sent by the server unconditionally.
		select {
		case <-firstClient.RoutesCh():
		case <-time.After(3 * time.Second):
		case err := <-muxErrCh:
			return fmt.Errorf("mux: %w", err)
		}
	}

	allSubnetStrings := append([]string(nil), cfg.SubnetStrings...)
	allSubnetStrings = append(allSubnetStrings, autoNetRoutes...)
	if len(allSubnetStrings) == 0 {
		return fmt.Errorf("no subnets to proxy — specify at least one CIDR (e.g. 0.0.0.0/0)")
	}

	// Parse subnet rules (with optional port ranges).
	effectiveSubnets, err := firewall.ParseSubnetRules(allSubnetStrings)
	if err != nil {
		return fmt.Errorf("parse subnets: %w", err)
	}

	// Filter out IPv6 subnets if --no-ipv6.
	if cfg.NoIPv6 {
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
	// Also clean up any stale IPv6-disable state from a prior crashed run, so
	// the user's interfaces aren't left with IPv6 turned off if we got killed
	// before our Restore ran.
	if err := firewall.RestoreSystemIPv6(); err != nil {
		log.Printf("iface_ipv6 stale restore: %v", err)
	}

	var fw firewall.Method
	if cfg.FirewallMethod == "auto" || cfg.FirewallMethod == "" {
		fw = firewall.NewAuto()
	} else {
		fw, err = firewall.New(cfg.FirewallMethod)
		if err != nil {
			return fmt.Errorf("firewall: %w", err)
		}
	}
	// Apply UDP blocking (default on; prevents QUIC leaks on pf).
	firewall.SetUDPBlock(fw, !cfg.NoBlockUDP)
	// Apply IPv6 blocking. Without this the firewall only stops *redirecting*
	// IPv6 — apps still reach AAAA destinations directly and bypass the tunnel.
	firewall.SetIPv6Block(fw, cfg.NoIPv6)
	// Apply TPROXY configuration if applicable.
	firewall.SetTProxyConfig(fw, firewall.TProxyConfig{
		FWMark:     cfg.TProxyMark,
		RouteTable: cfg.TProxyTable,
	})
	// When IPv6 is disabled, also drop AAAA queries at the DNS interceptor so
	// resolvers never hand out IPv6 addresses (avoids the Happy Eyeballs
	// connect-timeout pause that the firewall block would otherwise trigger).
	proxy.FilterAAAA = cfg.NoIPv6
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
		return fmt.Errorf("firewall method %q does not support IPv6; remove IPv6 subnets or use a different method", fw.Name())
	}
	if hasPortRange && !firewall.Supports(fw, firewall.FeaturePortRange) {
		return fmt.Errorf("firewall method %q does not support port ranges; remove port ranges or use a different method", fw.Name())
	}
	if cfg.UDPProxy && !firewall.Supports(fw, firewall.FeatureUDP) {
		return fmt.Errorf("firewall method %q does not support UDP proxy; use --method=tproxy for UDP support", fw.Name())
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
	if cfg.DNSEnabled {
		dnsServers = proxy.DetectDNSServers()
		// Bind the DNS listener BEFORE installing firewall rules so that
		// redirected DNS packets never arrive at an unbound port (which would
		// cause ICMP port-unreachable → "DNS probe failed" in browsers).
		var listenErr error
		if proxy.UseTProxy {
			// TPROXY does not rewrite packet headers — the socket must
			// have IP_TRANSPARENT and bind to 0.0.0.0 to accept packets
			// with non-local destination addresses.
			dnsListener, listenErr = proxy.ListenDNSTProxy(cachedPorts.DNSPort)
			if listenErr != nil && cachedPorts.DNSPort > 0 {
				log.Printf("preferred DNS tproxy port %d in use, picking a new one", cachedPorts.DNSPort)
				dnsListener, listenErr = proxy.ListenDNSTProxy(0)
			}
		} else {
			preferred := cachedPorts.DNSPort
			dnsBindAddr := proxy.BindAddr
			addr := fmt.Sprintf("%s:0", dnsBindAddr)
			if preferred > 0 {
				addr = fmt.Sprintf("%s:%d", dnsBindAddr, preferred)
			}
			dnsListener, listenErr = net.ListenPacket("udp", addr)
			if listenErr != nil && preferred > 0 {
				log.Printf("preferred DNS port %d in use, picking a new one", preferred)
				dnsListener, listenErr = net.ListenPacket("udp", fmt.Sprintf("%s:0", dnsBindAddr))
			}
		}
		if listenErr != nil {
			return fmt.Errorf("dns listen: %w", listenErr)
		}
		dnsPort = dnsListener.LocalAddr().(*net.UDPAddr).Port
		log.Printf("DNS: servers=%v localPort=%d", dnsServers, dnsPort)
	}

	proxyPort := pickFreePort("tcp", cachedPorts.ProxyPort)

	// ── Save ports for next reconnection ────────────────────────────────────
	savePortCache(portCache{
		ProxyPort: proxyPort,
		DNSPort:   dnsPort,
		StatsPort: e.statsPort,
	})

	if err := fw.Setup(effectiveSubnets, excludes, proxyPort, dnsPort, dnsServers); err != nil {
		return fmt.Errorf("firewall setup: %w", err)
	}

	// Interface-level IPv6 disable (layer above firewall block). Required to
	// prevent application-layer leaks: even with the firewall dropping IPv6
	// packets, apps can still read the local GUA from net interfaces and
	// embed it in payloads (WebRTC ICE, P2P DHT, SDP, STUN bindings). The
	// only way to stop that is to remove the GUA from interfaces.
	// Opt-out via --no-ipv6-lockdown for users who only want the firewall
	// layer (e.g. to avoid disturbing other IPv6-using apps on the system).
	lockdownIPv6 := cfg.NoIPv6 && !cfg.NoIPv6Lockdown
	if lockdownIPv6 {
		if err := firewall.DisableSystemIPv6(); err != nil {
			log.Printf("iface_ipv6: disable failed: %v (app-layer IPv6 leaks not prevented)", err)
		}
	}

	// Ensure firewall cleanup on any exit path — unless we are exiting for
	// reconnect, in which case we deliberately keep the rules in place so
	// traffic is blocked (redirected to the dead proxy port) rather than
	// leaking to the public internet during the reconnect window.
	skipFWRestore := false
	defer func() {
		if !skipFWRestore {
			fw.Restore()
			if lockdownIPv6 {
				firewall.RestoreSystemIPv6()
			}
		}
	}()

	// ── Signal tunnel is ready (Tauri sidecar.rs watches for this exact line) ─
	fmt.Fprintln(os.Stderr, "c : Connected to server.")
	e.signalReady()

	// Flush DNS cache BEFORE resetting TCP connections. Order matters:
	// 1. FlushDNSCache — discard stale/poisoned DNS entries
	// 2. FlushExistingConnections — RST existing TCP connections
	// If we reset TCP first, apps reconnect immediately using the still-
	// poisoned DNS cache and hit the wrong IPs. By flushing DNS first,
	// reconnecting apps re-resolve through the tunnel's DNS interceptor.
	firewall.FlushDNSCache()
	firewall.FlushExistingConnections(effectiveSubnets)

	// ── Start DNS proxy ───────────────────────────────────────────────────────
	if cfg.DNSEnabled && dnsListener != nil {
		go func() {
			if err := proxy.ServeDNS(dnsListener, tunnelClient, e.counters); err != nil {
				log.Printf("DNS proxy: %v", err)
			}
		}()
	}

	// ── Start UDP proxy (tproxy only) ────────────────────────────────────────
	if cfg.UDPProxy && proxy.UseTProxy {
		go func() {
			if err := proxy.ListenUDPTProxy(proxyPort, tunnelClient, e.counters); err != nil {
				log.Printf("UDP proxy: %v", err)
			}
		}()
	}

	// ── Start TCP proxy (transparent on Unix, SOCKS5 on Windows) ─────────────
	proxyErrCh := make(chan error, 1)
	go func() {
		proxyErrCh <- proxy.ListenTransparent(proxyPort, tunnelClient, e.counters)
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
		// Mux dying means the SSH connection dropped — keep TCP redirect
		// rules so traffic is blocked rather than leaking during reconnect,
		// but remove DNS redirect rules so the reconnecting tunnel process
		// can resolve the SSH server hostname via normal system DNS.
		firewall.DisableDNSRedirect(fw)
		skipFWRestore = true
		fmt.Fprintln(os.Stderr, "c : exit-for-reconnect")
		return ErrExitForReconnect
	case err := <-proxyErrCh:
		if err != nil {
			log.Printf("proxy closed: %v", err)
		}
		// Local proxy error — not a network issue, restore firewall.
		return nil
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
		return ErrExitForReconnect
	case <-stopCh:
		log.Printf("stop requested, cleaning up")
		return nil
	}
}
