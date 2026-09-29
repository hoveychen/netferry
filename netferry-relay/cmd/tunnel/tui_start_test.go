package main

import (
	"testing"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/sshconn"
	"github.com/hoveychen/netferry/relay/internal/store"
	"github.com/hoveychen/netferry/relay/internal/tui"
)

func TestEngineConfigForSoloSpecKeepsFectunAndLANPorts(t *testing.T) {
	socks := uint16(1080)
	spec := tui.ConnectSpec{
		Profile: profile.Profile{
			ID: "p1", Remote: "u@h", Dns: profile.DnsOff, Subnets: []string{"10.0.0.0/8"},
			Fectun: &sshconn.FectunConfig{Port: 55700, K: 20, M: 15, RateMbps: 25},
		},
		Settings: store.GlobalSettings{LanSocks5Port: &socks},
	}
	cfg, err := engineConfigForSpec(spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backend.fectun == nil || cfg.Backend.fectun.Port != 55700 {
		t.Fatalf("backend lost fectun: %+v", cfg.Backend)
	}
	if cfg.LANSocks5 != "1080" || cfg.LANHTTP != "" {
		t.Fatalf("LAN ports: socks=%q http=%q", cfg.LANSocks5, cfg.LANHTTP)
	}
	if cfg.DNSEnabled || cfg.FirewallMethod != "auto" || cfg.SubnetStrings[0] != "10.0.0.0/8" {
		t.Fatalf("knobs: %+v", cfg)
	}
}

func TestEngineConfigUsesProfileKnobs(t *testing.T) {
	a := profile.Profile{ID: "a", Remote: "u@a", Method: "nft", Subnets: []string{"1.0.0.0/8"}}
	cfg, err := engineConfigForSpec(tui.ConnectSpec{Profile: a}, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backend == nil || cfg.Backend.profileID != "a" {
		t.Fatalf("backend: %+v", cfg.Backend)
	}
	if cfg.FirewallMethod != "nft" || len(cfg.SubnetStrings) != 1 || cfg.SubnetStrings[0] != "1.0.0.0/8" {
		t.Fatalf("profile knobs not used: method=%s subnets=%v", cfg.FirewallMethod, cfg.SubnetStrings)
	}
	if !cfg.DNSEnabled {
		t.Fatal("missing dns mode should mean the desktop default (all)")
	}
	if _, err := engineConfigForSpec(tui.ConnectSpec{Profile: profile.Profile{ID: "x"}}, false); err == nil {
		t.Fatal("a profile without remote must be rejected")
	}
}
