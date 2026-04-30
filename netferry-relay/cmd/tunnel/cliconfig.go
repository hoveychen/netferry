package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/firewall"
	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/sshconn"
)

// parseAndBuildConfig parses CLI args and builds the resolved EngineConfig.
//
// Returns (nil, true, nil) when --version or --list-features ran (caller
// should exit 0 immediately). Returns (cfg, false, nil) on success.
// fatalf is used for hard-stop errors (matches legacy behavior).
func parseAndBuildConfig(args []string) (*EngineConfig, bool) {
	fs := flag.NewFlagSet("netferry-tunnel", flag.ExitOnError)
	var (
		remote         = fs.String("remote", "", "SSH target: [user@]host[:port]")
		identity       = fs.String("identity", "", "SSH private key path")
		autoNets       = fs.Bool("auto-nets", false, "add remote routes to proxy subnets")
		dns            = fs.Bool("dns", false, "intercept DNS requests")
		dnsTarget      = fs.String("dns-target", "", "remote DNS server IP[@port]")
		method         = fs.String("method", "auto", "firewall method: auto|pf|nft|ipt|tproxy|windivert|socks5")
		noIPv6         = fs.Bool("no-ipv6", false, "disable IPv6 handling")
		noIPv6Lockdown = fs.Bool("no-ipv6-lockdown", false, "with --no-ipv6, skip interface-level IPv6 disable (only firewall block); leaves apps able to read the local GUA and leak it via WebRTC/P2P payloads")
		noBlockUDP     = fs.Bool("no-block-udp", false, "allow non-DNS UDP (disables QUIC leak prevention)")
		udpProxy       = fs.Bool("udp", false, "enable generic UDP proxy (tproxy only)")
		tproxyMark     = fs.Int("tproxy-mark", 1, "TPROXY fwmark value")
		tproxyTable    = fs.Int("tproxy-table", 100, "TPROXY routing table number")
		verbose        = fs.Bool("v", false, "verbose logging")
		extraSSHOpts   = fs.String("extra-ssh-opts", "", "extra SSH options")
		jumpHostsJSON  = fs.String("jump", "", "explicit jump hosts as JSON array: [{\"remote\":\"user@host:port\",\"identityFile\":\"/path/to/key\"}]")
		excludeNets    = fs.String("exclude", "", "comma-separated CIDRs to exclude from tunnel")
		poolSize       = fs.Int("pool", 1, "number of parallel SSH TCP connections for connection bonding (1 = disabled; use 2-4 for high-concurrency workloads)")
		splitConn      = fs.Bool("split", false, "open a second SSH connection per pool member to carry smux control frames (SYN/NOP/UPD) separately from data frames (PSH/FIN), preventing bulk data from delaying window updates")
		tcpBalance     = fs.String("tcp-balance", "least-loaded", "TCP load-balancing strategy across pool members: round-robin|least-loaded")
		showVersion    = fs.Bool("version", false, "print version and exit")
		listFeatures   = fs.Bool("list-features", false, "print method features as JSON and exit")
		profilePath    = fs.String("profile", "", "path to encrypted .nfprofile file (all values are used unless overridden by explicit flags)")
		groupPath      = fs.String("group", "", "path to plaintext JSON profile-group file (supersedes --profile; engages multi-backend SessionManager)")
		tuiMode        = fs.Bool("tui", false, "launch the interactive TUI (reads desktop profiles/groups from app data dir)")
	)
	fs.Parse(args)
	subnets := fs.Args()

	if *showVersion {
		fmt.Println(Version)
		return nil, true
	}
	if *tuiMode {
		if err := runTUI(*verbose); err != nil {
			fatalf("tui: %v", err)
		}
		return nil, true
	}
	if *listFeatures {
		features := firewall.ListMethodFeatures()
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(features)
		return nil, true
	}

	// Track which flags were explicitly set on the command line so that
	// values loaded from --profile only fill in the gaps.
	setFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	// ── Profile loading (optional) ───────────────────────────────────────────
	var loadedProfile *profile.Profile
	if *profilePath != "" {
		p, err := profile.LoadFile(*profilePath)
		if err != nil {
			fatalf("profile: %v", err)
		}
		loadedProfile = p

		applyStrDefault := func(flagName string, dst *string, src string) {
			if !setFlags[flagName] && src != "" {
				*dst = src
			}
		}
		applyBoolDefault := func(flagName string, dst *bool, src bool) {
			if !setFlags[flagName] {
				*dst = src
			}
		}

		applyStrDefault("remote", remote, p.Remote)
		// identity-file is only useful when there is no inline PEM key.
		if p.IdentityKey == "" {
			applyStrDefault("identity", identity, p.IdentityFile)
		}
		applyStrDefault("method", method, p.Method)
		applyStrDefault("dns-target", dnsTarget, p.DnsTarget)
		applyStrDefault("extra-ssh-opts", extraSSHOpts, p.ExtraSSHOpts)
		applyStrDefault("tcp-balance", tcpBalance, p.TcpBalance)
		applyBoolDefault("auto-nets", autoNets, p.AutoNets)
		applyBoolDefault("no-ipv6", noIPv6, p.DisableIPv6)
		applyBoolDefault("udp", udpProxy, p.EnableUDP)
		// Desktop profile defaults block_udp=true; tunnel flag is --no-block-udp
		// (inverse). Only flip the flag when profile explicitly opts out.
		applyBoolDefault("no-block-udp", noBlockUDP, !p.BlockUDPOrDefault())
		applyBoolDefault("split", splitConn, p.SplitConn)
		// pool: mirror desktop default_pool_size (4) when the profile either
		// predates the field or was saved with 0/1.
		if !setFlags["pool"] {
			ps := p.PoolSize
			if ps <= 0 {
				ps = 4
			}
			*poolSize = ps
		}
		// DNS mode: off → --dns=false; all|specific → --dns=true.
		if !setFlags["dns"] && p.Dns != "" {
			*dns = p.Dns != profile.DnsOff
		}

		// Positional subnets: only fall back to profile when CLI gave none.
		if len(subnets) == 0 && len(p.Subnets) > 0 {
			subnets = append([]string(nil), p.Subnets...)
		}

		// Jump hosts: CLI --jump JSON, if given, wins; otherwise use profile.
		if !setFlags["jump"] && *jumpHostsJSON == "" && len(p.JumpHosts) > 0 {
			encoded := make([]sshconn.JumpHostSpec, 0, len(p.JumpHosts))
			for _, jh := range p.JumpHosts {
				spec := sshconn.JumpHostSpec{Remote: jh.Remote}
				if jh.IdentityKey == "" {
					spec.IdentityFile = jh.IdentityFile
				}
				encoded = append(encoded, spec)
			}
			if raw, err := json.Marshal(encoded); err == nil {
				*jumpHostsJSON = string(raw)
			}
		}
	}

	if *remote == "" && *profilePath != "" {
		fatalf("profile %q did not supply a remote", *profilePath)
	}

	// ── Group mode (optional) ────────────────────────────────────────────────
	// When --group is given, children drive SSH bring-up; --profile and CLI
	// SSH-level flags become inapplicable. Global-scope flags (firewall method,
	// DNS, UDP, IPv6 lockdown, --auto-nets, --to-ns, --verbose) still apply
	// because they configure the single shared firewall / proxy / stats layer.
	var groupFile *GroupFile
	if *groupPath != "" {
		if *profilePath != "" {
			fatalf("--group and --profile are mutually exclusive")
		}
		gf, err := loadGroupFile(*groupPath)
		if err != nil {
			fatalf("group: %v", err)
		}
		groupFile = gf
		// Global settings come from the group's default child when not
		// explicitly overridden on the CLI. This mirrors the single-profile
		// behaviour: children[0] (or explicit defaultProfileId) supplies the
		// process-level firewall/DNS/UDP/IPv6 knobs.
		var defaultChild *profile.Profile
		for i := range gf.Children {
			if gf.Children[i].ID == gf.DefaultProfileID {
				defaultChild = &gf.Children[i]
				break
			}
		}
		if defaultChild == nil {
			defaultChild = &gf.Children[0]
		}
		if !setFlags["method"] && defaultChild.Method != "" {
			*method = defaultChild.Method
		}
		if !setFlags["dns-target"] && defaultChild.DnsTarget != "" {
			*dnsTarget = defaultChild.DnsTarget
		}
		if !setFlags["auto-nets"] {
			*autoNets = defaultChild.AutoNets
		}
		if !setFlags["no-ipv6"] {
			*noIPv6 = defaultChild.DisableIPv6
		}
		if !setFlags["udp"] {
			*udpProxy = defaultChild.EnableUDP
		}
		if !setFlags["no-block-udp"] {
			*noBlockUDP = !defaultChild.BlockUDPOrDefault()
		}
		if !setFlags["dns"] && defaultChild.Dns != "" {
			*dns = defaultChild.Dns != profile.DnsOff
		}
		if len(subnets) == 0 {
			// Union children subnets for the proxy scope.
			seen := map[string]bool{}
			for i := range gf.Children {
				for _, s := range gf.Children[i].Subnets {
					s = strings.TrimSpace(s)
					if s != "" && !seen[s] {
						subnets = append(subnets, s)
						seen[s] = true
					}
				}
			}
		}
		// Sanity: --remote is meaningless in group mode; suppress the later
		// "required" check by picking any non-empty sentinel. We don't dial it.
		if *remote == "" {
			*remote = defaultChild.Remote
		}
	}

	if *remote == "" {
		fmt.Fprintln(os.Stderr, "fatal: --remote is required")
		fs.Usage()
		os.Exit(1)
	}

	// ── Build backend configs ────────────────────────────────────────────────
	// Group mode: one backend per child, each with its own SSH + mux pool.
	// Legacy mode: one backend synthesised from CLI flags + optional --profile.
	var backendCfgs []*backendConfig
	if groupFile != nil {
		for i := range groupFile.Children {
			child := &groupFile.Children[i]
			if child.Remote == "" {
				fatalf("group %q child %q missing remote", groupFile.ID, child.ID)
			}
			bc := backendCfgFromProfile(child)
			// Inline PEM for each child's jump hosts is pushed in via
			// NETFERRY_JUMP_KEY_<profileID>_<i> so secrets never hit disk.
			for j := range bc.jumpHosts {
				if pem := os.Getenv(fmt.Sprintf("NETFERRY_JUMP_KEY_%s_%d", child.ID, j)); pem != "" {
					bc.jumpHosts[j].IdentityPEM = pem
				}
			}
			backendCfgs = append(backendCfgs, bc)
		}
	} else {
		ac := sshconn.AuthConfig{
			IdentityFile: *identity,
			IdentityPEM:  os.Getenv("NETFERRY_IDENTITY_PEM"),
			ExtraOptions: *extraSSHOpts,
		}
		if ac.IdentityPEM == "" && loadedProfile != nil && loadedProfile.IdentityKey != "" {
			ac.IdentityPEM = loadedProfile.IdentityKey
		}
		var jumpHosts []sshconn.JumpHostSpec
		if *jumpHostsJSON != "" {
			if err := json.Unmarshal([]byte(*jumpHostsJSON), &jumpHosts); err != nil {
				fatalf("--jump JSON: %v", err)
			}
		}
		for i := range jumpHosts {
			if pem := os.Getenv(fmt.Sprintf("NETFERRY_JUMP_KEY_%d", i)); pem != "" {
				jumpHosts[i].IdentityPEM = pem
			}
		}
		if loadedProfile != nil && len(jumpHosts) == len(loadedProfile.JumpHosts) {
			for i, jh := range loadedProfile.JumpHosts {
				if jumpHosts[i].IdentityPEM == "" && jh.IdentityKey != "" {
					jumpHosts[i].IdentityPEM = jh.IdentityKey
				}
			}
		}
		bc := &backendConfig{
			remote:       *remote,
			identityFile: ac.IdentityFile,
			identityPEM:  ac.IdentityPEM,
			extraSSHOpts: ac.ExtraOptions,
			jumpHosts:    jumpHosts,
			poolSize:     *poolSize,
			splitConn:    *splitConn,
			tcpBalance:   *tcpBalance,
		}
		if loadedProfile != nil {
			bc.profileID = loadedProfile.ID
			if loadedProfile.AutoExcludeLANOrDefault() {
				bc.extraExcludes = append(bc.extraExcludes, profile.AutoExcludeLANCIDRs()...)
			}
			bc.extraExcludes = append(bc.extraExcludes, loadedProfile.ExcludeSubnets...)
		}
		backendCfgs = []*backendConfig{bc}
	}

	var excludeList []string
	if *excludeNets != "" {
		for _, c := range strings.Split(*excludeNets, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				excludeList = append(excludeList, c)
			}
		}
	}

	cfg := &EngineConfig{
		Backends:       backendCfgs,
		GroupFile:      groupFile,
		SubnetStrings:  subnets,
		FirewallMethod: *method,
		AutoNets:       *autoNets,
		DNSEnabled:     *dns,
		DNSTarget:      *dnsTarget,
		UDPProxy:       *udpProxy,
		NoIPv6:         *noIPv6,
		NoIPv6Lockdown: *noIPv6Lockdown,
		NoBlockUDP:     *noBlockUDP,
		ExcludeNets:    excludeList,
		TProxyMark:     *tproxyMark,
		TProxyTable:    *tproxyTable,
		Verbose:        *verbose,
	}
	return cfg, false
}
