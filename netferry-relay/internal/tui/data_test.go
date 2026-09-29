package tui

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/stats"
	"github.com/hoveychen/netferry/relay/internal/store"
)

func TestRulesBuildsRouteTableFromGlobalRules(t *testing.T) {
	rs := store.RuleSet{
		Rules: map[string]store.RouteMode{"x.example.com": {Kind: "blocked"}},
		RuleGroups: []store.RuleGroup{
			{ID: "r", Name: "R", Domains: []string{"example.com"}, Route: store.RouteMode{Kind: "direct"}},
			{ID: "s", Name: "S", Domains: []string{"=a.example.com"}, Route: store.RouteMode{Kind: "blocked"}},
		},
		FinalRoute: store.RouteMode{Kind: "direct"},
	}
	// The route table does not depend on groups or which one is active.
	d := &Data{RuleSet: rs, Priorities: map[string]int{"h": 5}}
	r := d.Rules()
	want := stats.RouteTable{
		Overrides: map[string]stats.RouteMode{"x.example.com": {Kind: stats.RouteBlocked}},
		Groups: []stats.RouteGroup{
			{Domains: []string{"example.com"}, Route: stats.RouteMode{Kind: stats.RouteDirect}},
			{Domains: []string{"=a.example.com"}, Route: stats.RouteMode{Kind: stats.RouteBlocked}},
		},
		Final: stats.RouteMode{Kind: stats.RouteDirect},
	}
	if !reflect.DeepEqual(r.Routes, want) || r.Priorities["h"] != 5 {
		t.Fatalf("rules: %+v", r)
	}
	d2 := &Data{RuleSet: rs, Groups: []store.Group{{ID: "g", Name: "G"}}, Settings: store.GlobalSettings{ActiveGroupID: "g"}}
	if !reflect.DeepEqual(d2.Rules().Routes, want) {
		t.Fatalf("active group changed the route table: %+v", d2.Rules().Routes)
	}
	// Applied to a tunnel, the first matching group wins over the later exact one.
	c := stats.NewCounters()
	c.SetRouteTable(r.Routes)
	if got := c.LookupRouteMode("", "a.example.com").Kind; got != stats.RouteDirect {
		t.Fatalf("a.example.com = %q, want direct (first group)", got)
	}
	if got := c.LookupRouteMode("", "other.org").Kind; got != stats.RouteDirect {
		t.Fatalf("fallback = %q, want direct", got)
	}
}

func TestMoveRuleGroup(t *testing.T) {
	g := &store.RuleSet{RuleGroups: []store.RuleGroup{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	ids := func() string {
		s := ""
		for _, rg := range g.RuleGroups {
			s += rg.ID
		}
		return s
	}
	if !MoveRuleGroup(g, "c", -1) || ids() != "acb" {
		t.Fatalf("up: %s", ids())
	}
	if !MoveRuleGroup(g, "a", 1) || ids() != "cab" {
		t.Fatalf("down: %s", ids())
	}
	if MoveRuleGroup(g, "c", -1) || MoveRuleGroup(g, "b", 1) || MoveRuleGroup(g, "zz", 1) || ids() != "cab" {
		t.Fatalf("clamped moves must be no-ops: %s", ids())
	}
}

func TestChildrenSkipsMissingProfiles(t *testing.T) {
	d := &Data{Profiles: []profile.Profile{{ID: "b"}}}
	got := d.Children(&store.Group{ChildrenIDs: []string{"a", "b"}})
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("children: %+v", got)
	}
}

func TestRecordKnownHostsDedupsAndCaps(t *testing.T) {
	g := &store.RuleSet{KnownHosts: []string{"a"}}
	if RecordKnownHosts(g, []string{"a", ""}) {
		t.Fatal("no new hosts should report unchanged")
	}
	var many []string
	for i := 0; i < MaxKnownHosts+5; i++ {
		many = append(many, fmt.Sprintf("h%d", i))
	}
	if !RecordKnownHosts(g, many) || len(g.KnownHosts) != MaxKnownHosts || g.KnownHosts[len(g.KnownHosts)-1] != many[len(many)-1] {
		t.Fatalf("cap: len=%d last=%s", len(g.KnownHosts), g.KnownHosts[len(g.KnownHosts)-1])
	}
}

func TestSetPriorityNormalDeletes(t *testing.T) {
	p := map[string]int{"a": 5}
	SetPriority(p, "a", 3)
	if _, ok := p["a"]; ok {
		t.Fatal("priority 3 should delete")
	}
}
