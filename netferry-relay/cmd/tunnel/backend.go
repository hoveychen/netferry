package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoveychen/netferry/relay/internal/deploy"
	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/sshconn"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// backendConfig holds the per-profile SSH + pool parameters needed to bring up
// one tunnel backend. Populated either from CLI flags (single-profile mode) or
// from a ProfileGroup child (--group mode).
type backendConfig struct {
	profileID    string
	remote       string
	identityFile string
	identityPEM  string
	extraSSHOpts string
	jumpHosts    []sshconn.JumpHostSpec
	poolSize     int
	splitConn    bool
	tcpBalance   string

	// extraExcludes is the union of this profile's ExcludeSubnets and the
	// auto-LAN CIDRs (when autoExcludeLAN is enabled). The caller merges
	// these into the firewall's exclude list alongside the SSH server IP
	// collected from the live connection.
	extraExcludes []string
}

// backend is the fully-connected runtime state for one backendConfig.
// Lives for the duration of the tunnel process. The SSH client objects are
// not exposed; the process exits (and the OS reaps them) on shutdown.
type backend struct {
	cfg         *backendConfig
	client      mux.TunnelClient // *MuxClient when poolSize==1, *MuxPool otherwise
	firstClient *mux.MuxClient   // used by the primary backend to read CMD_ROUTES
	sshServerIP string           // for firewall exclude
}

// backendCfgFromProfile builds a backendConfig from a ProfileGroup child.
// Applies the same defaults the desktop app uses when a field is missing.
func backendCfgFromProfile(p *profile.Profile) *backendConfig {
	n := p.PoolSize
	if n <= 0 {
		n = 4
	}
	bal := p.TcpBalance
	if bal == "" {
		bal = "least-loaded"
	}
	jumpHosts := make([]sshconn.JumpHostSpec, 0, len(p.JumpHosts))
	for _, jh := range p.JumpHosts {
		spec := sshconn.JumpHostSpec{Remote: jh.Remote}
		if jh.IdentityKey != "" {
			spec.IdentityPEM = jh.IdentityKey
		} else {
			spec.IdentityFile = jh.IdentityFile
		}
		jumpHosts = append(jumpHosts, spec)
	}
	identityFile := p.IdentityFile
	if p.IdentityKey != "" {
		identityFile = ""
	}
	cfg := &backendConfig{
		profileID:    p.ID,
		remote:       p.Remote,
		identityFile: identityFile,
		identityPEM:  p.IdentityKey,
		extraSSHOpts: p.ExtraSSHOpts,
		jumpHosts:    jumpHosts,
		poolSize:     n,
		splitConn:    p.SplitConn,
		tcpBalance:   bal,
	}
	if p.AutoExcludeLANOrDefault() {
		cfg.extraExcludes = append(cfg.extraExcludes, profile.AutoExcludeLANCIDRs()...)
	}
	cfg.extraExcludes = append(cfg.extraExcludes, p.ExcludeSubnets...)
	return cfg
}

// connectBackend performs the full SSH-dial → deploy → mux-pool → reconnect
// flow for one backendConfig. The returned backend is ready to serve traffic.
//
// primaryRTT controls whether this backend's first tunnel feeds the global
// keepalive RTT on stats.Counters — used for the legacy single-number display.
// In multi-profile mode, only one backend (conventionally the first) should
// pass true.
func connectBackend(
	cfg *backendConfig,
	serverArgs []string,
	counters *stats.Counters,
	muxErrCh chan<- error,
	primaryRTT bool,
) (*backend, error) {
	hc, err := sshconn.ParseSSHConfig(cfg.remote)
	if err != nil {
		return nil, fmt.Errorf("ssh config %q: %w", cfg.remote, err)
	}
	ac := sshconn.AuthConfig{
		IdentityFile: cfg.identityFile,
		IdentityPEM:  cfg.identityPEM,
		ExtraOptions: cfg.extraSSHOpts,
	}

	log.Printf("[%s] connecting to %s@%s:%d", cfg.profileID, hc.User, hc.HostName, hc.Port)
	first, err := sshconn.Dial(hc, ac, cfg.jumpHosts...)
	if err != nil {
		return nil, fmt.Errorf("ssh connect: %w", err)
	}

	sshServerIP := deploy.RemoteIP(first)

	remotePath, err := deploy.EnsureServer(first, Version)
	if err != nil {
		first.Close()
		return nil, fmt.Errorf("deploy server: %w", err)
	}
	log.Printf("[%s] remote server: %s", cfg.profileID, remotePath)

	remoteCmd := remotePath
	if len(serverArgs) > 0 {
		remoteCmd += " " + strings.Join(serverArgs, " ")
	}

	n := cfg.poolSize
	if n < 1 {
		n = 1
	}
	sshClients := make([]*ssh.Client, n)
	sshClients[0] = first
	for i := 1; i < n; i++ {
		extra, err := sshconn.Dial(hc, ac, cfg.jumpHosts...)
		if err != nil {
			for j := 0; j < i; j++ {
				sshClients[j].Close()
			}
			return nil, fmt.Errorf("ssh connect (pool %d/%d): %w", i+1, n, err)
		}
		sshClients[i] = extra
	}

	clients := make([]*mux.MuxClient, n)
	tunnelCounters := make([]*stats.TunnelCounters, n)
	for i, sc := range sshClients {
		tc := counters.RegisterTunnel(cfg.profileID, i+1)
		tunnelCounters[i] = tc
		rttCb := buildRTTCallback(counters, tc, primaryRTT && i == 0)
		sshconn.StartSSHKeepalive(sc, 30*time.Second, rttCb)

		var c *mux.MuxClient
		if cfg.splitConn {
			c, err = trySplitMuxClient(sc, hc, ac, cfg.jumpHosts, remoteCmd, i+1, n)
		} else {
			c, err = tryMuxClient(sc, remoteCmd, i+1, n)
		}
		if err != nil {
			for _, s := range sshClients {
				s.Close()
			}
			return nil, fmt.Errorf("mux handshake: %w", err)
		}
		c.SetCounters(counters)
		c.SetTunnelIndex(i+1, tc)
		clients[i] = c
	}
	if cfg.splitConn {
		log.Printf("[%s] mux: split-conn enabled", cfg.profileID)
	}
	if n > 1 {
		log.Printf("[%s] mux pool: %d parallel TCP connections", cfg.profileID, n)
	}

	firstClient := clients[0]
	strategy := mux.LBLeastLoaded
	if cfg.tcpBalance == "round-robin" {
		strategy = mux.LBRoundRobin
	}
	pool := mux.NewMuxPoolWithStrategy(clients, strategy)
	for i := 0; i < n; i++ {
		idx := i
		go reconnectPoolMember(pool, idx, n, clients[idx], hc, ac, cfg.jumpHosts, remoteCmd, cfg.splitConn, counters, tunnelCounters[idx], muxErrCh)
	}

	return &backend{
		cfg:         cfg,
		client:      pool,
		firstClient: firstClient,
		sshServerIP: sshServerIP,
	}, nil
}
