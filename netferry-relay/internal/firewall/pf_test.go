//go:build darwin

package firewall

import (
	"strings"
	"testing"
)

func TestPfBuildRules_BlockIPv6_AddsBlockOutQuickInet6(t *testing.T) {
	p := &pfMethod{blockIPv6: true}
	subnets, err := ParseSubnetRules([]string{"0.0.0.0/0"})
	if err != nil {
		t.Fatalf("ParseSubnetRules: %v", err)
	}
	rules := string(p.buildRules(subnets, []string{"127.0.0.0/8"}, 12345, 0, nil))

	if !strings.Contains(rules, "block return out quick inet6 all") {
		t.Errorf("expected 'block return out quick inet6 all' in rules when blockIPv6=true (bare 'block' = block drop, causes TCP SYN to silently disappear → apps hang on connect timeout instead of falling back to IPv4), got:\n%s", rules)
	}
	// Link-local (fe80::/10) and loopback (::1) must remain reachable so NDP /
	// DHCPv6 / local services keep working — verify they are passed before the
	// blanket block.
	if !strings.Contains(rules, "fe80::/10") {
		t.Errorf("expected fe80::/10 pass-out rule (link-local) when blockIPv6=true, got:\n%s", rules)
	}
	if !strings.Contains(rules, "::1") {
		t.Errorf("expected ::1 pass-out rule (loopback) when blockIPv6=true, got:\n%s", rules)
	}
	// Order matters in pf: pass rules for fe80::/10 and ::1 must appear BEFORE
	// the block to avoid being overridden (pf is last-match-wins, but block
	// quick short-circuits — keep pass-quick on link-local before block).
	idxBlock := strings.Index(rules, "block return out quick inet6 all")
	idxLinkLocal := strings.Index(rules, "fe80::/10")
	if idxLinkLocal < 0 || idxBlock < 0 || idxLinkLocal > idxBlock {
		t.Errorf("link-local pass rule must precede block in rules:\n%s", rules)
	}
}

func TestPfBuildRules_NoBlockIPv6(t *testing.T) {
	p := &pfMethod{blockIPv6: false}
	subnets, err := ParseSubnetRules([]string{"0.0.0.0/0"})
	if err != nil {
		t.Fatalf("ParseSubnetRules: %v", err)
	}
	rules := string(p.buildRules(subnets, []string{"127.0.0.0/8"}, 12345, 0, nil))

	if strings.Contains(rules, "block return out quick inet6 all") {
		t.Errorf("did not expect block IPv6 rule when blockIPv6=false, got:\n%s", rules)
	}
}

func TestPfMethod_SetBlockIPv6(t *testing.T) {
	p := &pfMethod{}
	// Compile-time assertion: pfMethod implements IPv6Blocker.
	var _ IPv6Blocker = p
	p.SetBlockIPv6(true)
	if !p.blockIPv6 {
		t.Errorf("SetBlockIPv6(true) did not set blockIPv6 field")
	}
	p.SetBlockIPv6(false)
	if p.blockIPv6 {
		t.Errorf("SetBlockIPv6(false) did not clear blockIPv6 field")
	}
}

func TestPfBuildRules_AllowRootUDP_PassesBeforeBlock(t *testing.T) {
	subnets, err := ParseSubnetRules([]string{"0.0.0.0/0"})
	if err != nil {
		t.Fatalf("ParseSubnetRules: %v", err)
	}
	const pass = "pass out quick inet proto udp user root keep state"
	const block = "block out quick inet proto udp all"

	rules := string((&pfMethod{blockUDP: true, allowRootUDP: true}).buildRules(subnets, []string{"127.0.0.0/8"}, 12345, 0, nil))
	if i, j := strings.Index(rules, pass), strings.Index(rules, block); i < 0 || j < 0 || i > j {
		t.Errorf("root UDP pass must precede the UDP block, got:\n%s", rules)
	}
	rules = string((&pfMethod{blockUDP: true}).buildRules(subnets, []string{"127.0.0.0/8"}, 12345, 0, nil))
	if strings.Contains(rules, "proto udp user root") {
		t.Errorf("root UDP pass must be absent without LAN SOCKS5, got:\n%s", rules)
	}
}
