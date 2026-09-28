//go:build linux

package firewall

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hoveychen/netferry/relay/internal/sockmark"
)

// The output chain must let the tunnel's own SO_MARK-tagged direct dials
// through before any redirect/mark rule, and must not exempt uid 0 — that
// would leave every process of a root user unproxied.
func assertOutputChainBypass(t *testing.T, rules string) {
	t.Helper()
	i := strings.Index(rules, "chain output {")
	if i < 0 {
		t.Fatalf("no output chain in rules:\n%s", rules)
	}
	out := rules[i:]
	out = out[:strings.Index(out, "  }\n")]

	if strings.Contains(rules, "skuid") {
		t.Errorf("rules must not exempt by uid, got:\n%s", rules)
	}
	bypass := fmt.Sprintf("meta mark 0x%x return", sockmark.Bypass)
	bi := strings.Index(out, bypass)
	if bi < 0 {
		t.Fatalf("output chain missing %q, got:\n%s", bypass, out)
	}
	for _, action := range []string{"redirect to", "meta mark set"} {
		if ai := strings.Index(out, action); ai >= 0 && ai < bi {
			t.Errorf("%q appears before the bypass rule in output chain:\n%s", action, out)
		}
	}
}

func TestNftBuildNftRules_OutputBypassesMarkedSockets(t *testing.T) {
	subnets, err := ParseSubnetRules([]string{"0.0.0.0/0", "::/0"})
	if err != nil {
		t.Fatalf("ParseSubnetRules: %v", err)
	}
	n := &nftMethod{}
	assertOutputChainBypass(t, string(n.buildNftRules(subnets, nil, 12300, 12301, []string{"1.1.1.1"})))
}

func TestTProxyBuildNftRules_OutputBypassesMarkedSockets(t *testing.T) {
	subnets, err := ParseSubnetRules([]string{"0.0.0.0/0", "::/0"})
	if err != nil {
		t.Fatalf("ParseSubnetRules: %v", err)
	}
	tp := &tproxyMethod{cfg: DefaultTProxyConfig()}
	assertOutputChainBypass(t, string(tp.buildNftRules(subnets, nil, 12300, 12301, []string{"1.1.1.1"})))
}

// The bypass mark must not collide with the TPROXY policy-routing mark, or
// marked direct dials would be routed to loopback anyway.
func TestBypassMarkDiffersFromTProxyMark(t *testing.T) {
	if sockmark.Bypass == DefaultTProxyConfig().FWMark {
		t.Fatalf("sockmark.Bypass (%#x) equals the default TPROXY fwmark", sockmark.Bypass)
	}
}
