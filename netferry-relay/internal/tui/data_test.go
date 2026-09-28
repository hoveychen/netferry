package tui

import (
	"fmt"
	"testing"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/store"
)

func TestRulesCompilesActiveGroupAndGroupPayload(t *testing.T) {
	g := store.Group{
		ID: "g", Name: "G", ChildrenIDs: []string{"a", "b"},
		Rules:      map[string]store.RouteMode{"x.example.com": {Kind: "blocked"}},
		RuleGroups: []store.RuleGroup{{ID: "r", Domains: []string{"example.com"}, Route: store.RouteMode{Kind: "direct"}}},
	}
	d := &Data{Groups: []store.Group{g}, Settings: store.GlobalSettings{ActiveGroupID: "g"}, Priorities: map[string]int{"h": 5}}
	solo := d.Rules(false)
	if solo.Group != nil || solo.Routes["*.example.com"].Kind != "direct" || solo.Routes["x.example.com"].Kind != "blocked" || solo.Priorities["h"] != 5 {
		t.Fatalf("solo rules: %+v", solo)
	}
	grp := d.Rules(true)
	if grp.Group == nil || grp.Group.DefaultProfileID != "a" || len(grp.Group.ProfileIDs) != 2 {
		t.Fatalf("group payload: %+v", grp.Group)
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
	g := &store.Group{KnownHosts: []string{"a"}}
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
