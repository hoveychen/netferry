package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/tui"
)

// engineConfigForSpec resolves a TUI connect request into an EngineConfig the
// same way the desktop's sidecar resolves it into CLI flags: process-wide
// knobs, capture subnets and the backend come from the profile; the LAN proxy
// ports come from global settings.
func engineConfigForSpec(spec tui.ConnectSpec, verbose bool) (*EngineConfig, error) {
	seed := spec.Profile
	backend := backendCfgFromProfile(&seed)
	if strings.TrimSpace(backend.remote) == "" {
		return nil, fmt.Errorf("profile %q has no remote", backend.profileID)
	}

	var subnets []string
	for _, s := range seed.Subnets {
		if s = strings.TrimSpace(s); s != "" {
			subnets = append(subnets, s)
		}
	}
	if len(subnets) == 0 {
		subnets = []string{"0.0.0.0/0"}
	}
	method := strings.TrimSpace(seed.Method)
	if method == "" {
		method = "auto"
	}
	cfg := &EngineConfig{
		Backend:        backend,
		SubnetStrings:  subnets,
		FirewallMethod: method,
		AutoNets:       seed.AutoNets,
		DNSEnabled:     seed.Dns != profile.DnsOff, // missing = desktop default "all"
		DNSTarget:      strings.TrimSpace(seed.DnsTarget),
		UDPProxy:       seed.EnableUDP,
		NoIPv6:         seed.DisableIPv6,
		NoBlockUDP:     !seed.BlockUDPOrDefault(),
		TProxyMark:     1,
		TProxyTable:    100,
		Verbose:        verbose,
	}
	if p := spec.Settings.LanSocks5Port; p != nil && *p > 0 {
		cfg.LANSocks5 = strconv.Itoa(int(*p))
	}
	if p := spec.Settings.LanHTTPPort; p != nil && *p > 0 {
		cfg.LANHTTP = strconv.Itoa(int(*p))
	}
	return cfg, nil
}

// tuiStarter adapts NewEngine to the TUI session.
func tuiStarter(verbose bool) tui.Starter {
	return func(spec tui.ConnectSpec) (tui.Engine, error) {
		cfg, err := engineConfigForSpec(spec, verbose)
		if err != nil {
			return nil, err
		}
		eng, err := NewEngine(cfg)
		if err != nil {
			return nil, err
		}
		return eng, nil
	}
}

func isReconnectErr(err error) bool { return errors.Is(err, ErrExitForReconnect) }
