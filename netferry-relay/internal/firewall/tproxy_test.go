//go:build linux

package firewall

import (
	"strings"
	"testing"
)

// In an nftables `inet` table, the `tproxy` statement requires an explicit
// address-family qualifier (`ip` or `ip6`); bare `tproxy to <addr>:<port>`
// errors out with "conflicting protocols specified: ip vs. unknown. You must
// specify ip or ip6 family in tproxy statement".
func TestTProxyBuildNftRules_TProxyHasFamilyQualifier(t *testing.T) {
	tp := &tproxyMethod{cfg: DefaultTProxyConfig()}
	subnets, err := ParseSubnetRules([]string{"0.0.0.0/0", "::/0"})
	if err != nil {
		t.Fatalf("ParseSubnetRules: %v", err)
	}
	rules := string(tp.buildNftRules(subnets, nil, 37031, 43128, []string{"1.1.1.1", "2606:4700:4700::1111"}))

	// Bare `tproxy to` is rejected by nft in inet tables.
	for _, line := range strings.Split(rules, "\n") {
		if !strings.Contains(line, "tproxy ") {
			continue
		}
		if strings.Contains(line, "tproxy ip to ") || strings.Contains(line, "tproxy ip6 to ") {
			continue
		}
		t.Errorf("tproxy statement missing ip/ip6 family qualifier:\n  %s", strings.TrimSpace(line))
	}

	// Positive checks: both families must appear somewhere.
	if !strings.Contains(rules, "tproxy ip to 127.0.0.1:37031") {
		t.Errorf("expected `tproxy ip to 127.0.0.1:37031` for IPv4 subnet, got:\n%s", rules)
	}
	if !strings.Contains(rules, "tproxy ip6 to [::1]:37031") {
		t.Errorf("expected `tproxy ip6 to [::1]:37031` for IPv6 subnet, got:\n%s", rules)
	}
}

func TestTProxyBuildNftRules_BlockIPv6(t *testing.T) {
	tp := &tproxyMethod{cfg: DefaultTProxyConfig(), blockIPv6: true}
	subnets, err := ParseSubnetRules([]string{"0.0.0.0/0"})
	if err != nil {
		t.Fatalf("ParseSubnetRules: %v", err)
	}
	rules := string(tp.buildNftRules(subnets, nil, 12345, 0, nil))

	if !strings.Contains(rules, "chain block_ipv6") {
		t.Errorf("expected block_ipv6 chain in rules, got:\n%s", rules)
	}
	if !strings.Contains(rules, "reject with icmpv6") {
		t.Errorf("expected icmpv6 reject in rules, got:\n%s", rules)
	}
	for _, want := range []string{"::1/128", "fe80::/10", "ff00::/8"} {
		if !strings.Contains(rules, want) {
			t.Errorf("expected whitelist for %s, got:\n%s", want, rules)
		}
	}
}

func TestTProxyBuildNftRules_NoBlockIPv6(t *testing.T) {
	tp := &tproxyMethod{cfg: DefaultTProxyConfig(), blockIPv6: false}
	subnets, err := ParseSubnetRules([]string{"0.0.0.0/0"})
	if err != nil {
		t.Fatalf("ParseSubnetRules: %v", err)
	}
	rules := string(tp.buildNftRules(subnets, nil, 12345, 0, nil))

	if strings.Contains(rules, "block_ipv6") {
		t.Errorf("did not expect block_ipv6 chain when blockIPv6=false, got:\n%s", rules)
	}
}

func TestIPv6Blocker_TProxyAndIptAndNft(t *testing.T) {
	var _ IPv6Blocker = &tproxyMethod{}
	var _ IPv6Blocker = &nftMethod{}
	var _ IPv6Blocker = &iptMethod{}
}
