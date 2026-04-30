package main

import (
	"fmt"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/sshconn"
	"github.com/hoveychen/netferry/relay/internal/store"
)

// engineConfigFromProfile builds an EngineConfig for a single-profile session.
// Mirrors the resolution that cliconfig.go does for `--profile` plus the
// implicit single-backend layout.
func engineConfigFromProfile(p *profile.Profile, verbose bool) (*EngineConfig, error) {
	if p == nil {
		return nil, fmt.Errorf("profile is nil")
	}
	if strings.TrimSpace(p.Remote) == "" {
		return nil, fmt.Errorf("profile %q has no remote", p.Name)
	}

	ac := sshconn.AuthConfig{
		IdentityFile: p.IdentityFile,
		IdentityPEM:  p.IdentityKey,
		ExtraOptions: p.ExtraSSHOpts,
	}
	if ac.IdentityPEM != "" {
		ac.IdentityFile = ""
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
	pool := p.PoolSize
	if pool <= 0 {
		pool = 4
	}
	bal := p.TcpBalance
	if bal == "" {
		bal = "least-loaded"
	}
	bc := &backendConfig{
		profileID:    p.ID,
		remote:       p.Remote,
		identityFile: ac.IdentityFile,
		identityPEM:  ac.IdentityPEM,
		extraSSHOpts: ac.ExtraOptions,
		jumpHosts:    jumpHosts,
		poolSize:     pool,
		splitConn:    p.SplitConn,
		tcpBalance:   bal,
	}
	if p.AutoExcludeLANOrDefault() {
		bc.extraExcludes = append(bc.extraExcludes, profile.AutoExcludeLANCIDRs()...)
	}
	bc.extraExcludes = append(bc.extraExcludes, p.ExcludeSubnets...)

	subnets := append([]string(nil), p.Subnets...)
	if len(subnets) == 0 {
		subnets = []string{"0.0.0.0/0"}
	}
	method := p.Method
	if method == "" {
		method = "auto"
	}
	cfg := &EngineConfig{
		Backends:       []*backendConfig{bc},
		SubnetStrings:  subnets,
		FirewallMethod: method,
		AutoNets:       p.AutoNets,
		DNSEnabled:     p.Dns != "" && p.Dns != profile.DnsOff,
		DNSTarget:      p.DnsTarget,
		UDPProxy:       p.EnableUDP,
		NoIPv6:         p.DisableIPv6,
		NoBlockUDP:     !p.BlockUDPOrDefault(),
		TProxyMark:     1,
		TProxyTable:    100,
		Verbose:        verbose,
	}
	return cfg, nil
}

// engineConfigFromGroup builds an EngineConfig for a group session by
// resolving children_ids against the loaded profiles list.
func engineConfigFromGroup(g *store.Group, profiles []profile.Profile, verbose bool) (*EngineConfig, error) {
	if g == nil || len(g.ChildrenIDs) == 0 {
		return nil, fmt.Errorf("group has no children")
	}
	children := make([]profile.Profile, 0, len(g.ChildrenIDs))
	for _, id := range g.ChildrenIDs {
		p := store.FindProfile(profiles, id)
		if p == nil {
			return nil, fmt.Errorf("group %q references missing profile %q", g.Name, id)
		}
		children = append(children, *p)
	}
	defaultID := children[0].ID
	if rule, ok := g.Rules["default"]; ok && rule.ProfileID != "" {
		defaultID = rule.ProfileID
	}

	gf := &GroupFile{
		ID:               g.ID,
		Name:             g.Name,
		DefaultProfileID: defaultID,
		Children:         children,
	}

	var defaultChild *profile.Profile
	for i := range children {
		if children[i].ID == defaultID {
			defaultChild = &children[i]
			break
		}
	}
	if defaultChild == nil {
		defaultChild = &children[0]
	}

	backendCfgs := make([]*backendConfig, 0, len(children))
	subnetSet := map[string]bool{}
	var subnets []string
	for i := range children {
		bc := backendCfgFromProfile(&children[i])
		backendCfgs = append(backendCfgs, bc)
		for _, s := range children[i].Subnets {
			s = strings.TrimSpace(s)
			if s != "" && !subnetSet[s] {
				subnets = append(subnets, s)
				subnetSet[s] = true
			}
		}
	}
	if len(subnets) == 0 {
		subnets = []string{"0.0.0.0/0"}
	}

	method := defaultChild.Method
	if method == "" {
		method = "auto"
	}
	cfg := &EngineConfig{
		Backends:       backendCfgs,
		GroupFile:      gf,
		SubnetStrings:  subnets,
		FirewallMethod: method,
		AutoNets:       defaultChild.AutoNets,
		DNSEnabled:     defaultChild.Dns != "" && defaultChild.Dns != profile.DnsOff,
		DNSTarget:      defaultChild.DnsTarget,
		UDPProxy:       defaultChild.EnableUDP,
		NoIPv6:         defaultChild.DisableIPv6,
		NoBlockUDP:     !defaultChild.BlockUDPOrDefault(),
		TProxyMark:     1,
		TProxyTable:    100,
		Verbose:        verbose,
	}
	return cfg, nil
}
