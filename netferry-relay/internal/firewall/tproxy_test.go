//go:build linux

package firewall

import (
	"strings"
	"testing"
)

// TestBuildTProxyNftScriptFamilyQualifiers verifies that tproxy statements in
// the generated nft script include explicit IP family qualifiers ("tproxy ip to"
// / "tproxy ip6 to").  Without the qualifier, nftables >= 0.9.4 rejects the
// script with "conflicting protocols specified: ip vs. unknown" when the table
// family is "inet".
func TestBuildTProxyNftScriptFamilyQualifiers(t *testing.T) {
	m := &tproxyMethod{
		useNft: true,
		cfg:    DefaultTProxyConfig(),
	}

	t.Run("ipv4_default_route", func(t *testing.T) {
		subnets := []SubnetRule{{CIDR: "0.0.0.0/0"}}
		script := m.buildTProxyNftScript(subnets, nil, 37031, 0, nil)

		if strings.Contains(script, "tproxy to ") {
			t.Errorf("script contains bare 'tproxy to' without family qualifier:\n%s", script)
		}
		if !strings.Contains(script, "tproxy ip to 127.0.0.1:37031") {
			t.Errorf("script missing 'tproxy ip to 127.0.0.1:37031':\n%s", script)
		}
	})

	t.Run("ipv4_with_port_range", func(t *testing.T) {
		subnets := []SubnetRule{{CIDR: "10.0.0.0/8", PortLow: 80, PortHigh: 443}}
		script := m.buildTProxyNftScript(subnets, nil, 37031, 0, nil)

		if strings.Contains(script, "tproxy to ") {
			t.Errorf("script contains bare 'tproxy to' without family qualifier:\n%s", script)
		}
		if !strings.Contains(script, "tproxy ip to 127.0.0.1:37031") {
			t.Errorf("script missing 'tproxy ip to 127.0.0.1:37031':\n%s", script)
		}
	})

	t.Run("ipv6_subnet", func(t *testing.T) {
		subnets := []SubnetRule{{CIDR: "::/0"}}
		script := m.buildTProxyNftScript(subnets, nil, 37031, 0, nil)

		if strings.Contains(script, "tproxy to ") {
			t.Errorf("script contains bare 'tproxy to' without family qualifier:\n%s", script)
		}
		if !strings.Contains(script, "tproxy ip6 to [::1]:37031") {
			t.Errorf("script missing 'tproxy ip6 to [::1]:37031':\n%s", script)
		}
	})

	t.Run("dns_ipv4", func(t *testing.T) {
		subnets := []SubnetRule{{CIDR: "0.0.0.0/0"}}
		script := m.buildTProxyNftScript(subnets, nil, 37031, 5300, []string{"8.8.8.8"})

		if strings.Contains(script, "tproxy to ") {
			t.Errorf("script contains bare 'tproxy to' without family qualifier:\n%s", script)
		}
		if !strings.Contains(script, "tproxy ip to 127.0.0.1:5300") {
			t.Errorf("script missing DNS 'tproxy ip to 127.0.0.1:5300':\n%s", script)
		}
	})

	t.Run("dns_ipv6", func(t *testing.T) {
		subnets := []SubnetRule{{CIDR: "::/0"}}
		script := m.buildTProxyNftScript(subnets, nil, 37031, 5300, []string{"2001:4860:4860::8888"})

		if strings.Contains(script, "tproxy to ") {
			t.Errorf("script contains bare 'tproxy to' without family qualifier:\n%s", script)
		}
		if !strings.Contains(script, "tproxy ip6 to [::1]:5300") {
			t.Errorf("script missing DNS 'tproxy ip6 to [::1]:5300':\n%s", script)
		}
	})
}
