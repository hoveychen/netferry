package store_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hoveychen/netferry/relay/internal/store"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRaw(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, raw)
	}
	return m
}

func TestRulesNoGroupsMigratesToEmpty(t *testing.T) {
	dir := withTempDataDir(t)
	rs, err := store.LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rs, store.EmptyRuleSet()) {
		t.Fatalf("rules = %+v", rs)
	}
	m := readRaw(t, filepath.Join(dir, "rules.json"))
	if fr, _ := m["finalRoute"].(map[string]any); fr["kind"] != "tunnel" {
		t.Fatalf("finalRoute: %v", m)
	}
	for _, k := range []string{"rules", "ruleGroups", "knownHosts"} {
		if v, ok := m[k]; !ok || v == nil {
			t.Fatalf("%s missing or null on disk: %v", k, m)
		}
	}
}

func TestRulesMigrationMergesGroups(t *testing.T) {
	dir := withTempDataDir(t)
	g := filepath.Join(dir, "groups")
	// "b" is active; "a" and "c" are not. Written out of id order on purpose.
	writeFile(t, filepath.Join(g, "c.json"), `{
  "id": "c", "name": "C", "childrenIds": [],
  "rules": {"shared.com": {"kind": "blocked"}, "c.com": {"kind": "default"}, "ac.com": "blocked"},
  "ruleGroups": [{"id": "r2", "name": "R2 from c", "domains": ["c2.com"], "route": {"kind": "blocked"}},
                 {"id": "r3", "name": "R3", "domains": ["c3.com"], "route": {"kind": "default"}}],
  "finalRoute": {"kind": "direct"},
  "knownHosts": ["h3", "h1", "c-only"]
}`)
	writeFile(t, filepath.Join(g, "a.json"), `{
  "id": "a", "name": "A", "childrenIds": [],
  "rules": {"shared.com": {"kind": "direct"}, "a.com": {"kind": "tunnel", "profileId": "p"}, "ac.com": "direct"},
  "ruleGroups": [{"id": "r4", "name": "R4", "domains": ["a4.com"], "route": {"kind": "direct"}},
                 {"id": "r1", "name": "R1 from a", "domains": ["dup.com"], "route": {"kind": "direct"}}],
  "knownHosts": ["h1", "h2"]
}`)
	writeFile(t, filepath.Join(g, "b.json"), `{
  "id": "b", "name": "B", "childrenIds": ["p1"],
  "rules": {"shared.com": {"kind": "tunnel"}},
  "ruleGroups": [{"id": "r2", "name": "R2 from b", "domains": ["b2.com"], "route": {"kind": "direct"}},
                 {"id": "r1", "name": "R1 from b", "domains": ["b1.com"], "route": {"kind": "tunnel"}}],
  "finalRoute": {"kind": "blocked"},
  "knownHosts": ["h2", "b-only", "h1"]
}`)
	writeFile(t, filepath.Join(dir, "settings.json"), `{"activeGroupId": "b"}`)
	groupBefore, _ := os.ReadFile(filepath.Join(g, "b.json"))

	rs, err := store.LoadRules()
	if err != nil {
		t.Fatal(err)
	}

	wantRules := map[string]store.RouteMode{
		"shared.com": {Kind: "tunnel"},  // active group wins
		"ac.com":     {Kind: "blocked"}, // c overrides a (id order among non-active)
		"a.com":      {Kind: "tunnel"},
		"c.com":      {Kind: "tunnel"}, // legacy "default" normalized
	}
	if !reflect.DeepEqual(rs.Rules, wantRules) {
		t.Fatalf("rules = %+v", rs.Rules)
	}

	var ids, names, kinds []string
	for _, rg := range rs.RuleGroups {
		ids = append(ids, rg.ID)
		names = append(names, rg.Name)
		kinds = append(kinds, rg.Route.Kind)
	}
	if want := []string{"r2", "r1", "r4", "r3"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ruleGroups ids = %v, want %v", ids, want)
	}
	if names[0] != "R2 from b" || names[1] != "R1 from b" {
		t.Fatalf("duplicate ids should keep the active group's: %v", names)
	}
	if want := []string{"direct", "tunnel", "direct", "tunnel"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("ruleGroup routes = %v", kinds)
	}

	// Active group's finalRoute ("blocked" is not a valid fallback -> tunnel),
	// not c's "direct".
	if rs.FinalRoute.Kind != "tunnel" {
		t.Fatalf("finalRoute = %+v", rs.FinalRoute)
	}

	// a, c (non-active by id), then b; first occurrence kept.
	if want := []string{"h1", "h2", "h3", "c-only", "b-only"}; !reflect.DeepEqual(rs.KnownHosts, want) {
		t.Fatalf("knownHosts = %v", rs.KnownHosts)
	}

	// rules.json written; group files untouched.
	if _, err := os.Stat(filepath.Join(dir, "rules.json")); err != nil {
		t.Fatal(err)
	}
	groupAfter, _ := os.ReadFile(filepath.Join(g, "b.json"))
	if string(groupBefore) != string(groupAfter) {
		t.Fatal("migration rewrote a group file")
	}

	// Once rules.json exists, group rules are no longer consulted.
	writeFile(t, filepath.Join(g, "b.json"), `{"id":"b","name":"B","rules":{"new.com":{"kind":"direct"}}}`)
	again, err := store.LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, rs) {
		t.Fatalf("second load differs:\n%+v\n%+v", again, rs)
	}
}

func TestRulesMigrationActiveFinalRouteDirect(t *testing.T) {
	dir := withTempDataDir(t)
	writeFile(t, filepath.Join(dir, "groups", "x.json"), `{"id":"x","name":"X","finalRoute":{"kind":"direct"}}`)
	writeFile(t, filepath.Join(dir, "groups", "y.json"), `{"id":"y","name":"Y","finalRoute":{"kind":"tunnel"}}`)
	writeFile(t, filepath.Join(dir, "settings.json"), `{"activeGroupId":"x"}`)
	rs, err := store.LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	if rs.FinalRoute.Kind != "direct" {
		t.Fatalf("finalRoute = %+v", rs.FinalRoute)
	}
}

func TestRulesMigrationNoActiveGroup(t *testing.T) {
	dir := withTempDataDir(t)
	writeFile(t, filepath.Join(dir, "groups", "x.json"), `{"id":"x","name":"X","finalRoute":{"kind":"direct"},
  "rules":{"k.com":"direct"},"ruleGroups":[{"id":"g","name":"G","domains":["g.com"],"route":"direct"}]}`)
	rs, err := store.LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	// Non-active groups contribute rules/ruleGroups/knownHosts but not the
	// fallback.
	if rs.FinalRoute.Kind != "tunnel" || rs.Rules["k.com"].Kind != "direct" || len(rs.RuleGroups) != 1 {
		t.Fatalf("rules = %+v", rs)
	}
}

func TestRulesMigrationKnownHostsCap(t *testing.T) {
	dir := withTempDataDir(t)
	mk := func(prefix string, n int) string {
		hs := make([]string, n)
		for i := range hs {
			hs[i] = fmt.Sprintf("%s%d", prefix, i)
		}
		b, _ := json.Marshal(hs)
		return string(b)
	}
	writeFile(t, filepath.Join(dir, "groups", "a.json"), `{"id":"a","name":"A","knownHosts":`+mk("old", 800)+`}`)
	writeFile(t, filepath.Join(dir, "groups", "z.json"), `{"id":"z","name":"Z","knownHosts":`+mk("act", 700)+`}`)
	writeFile(t, filepath.Join(dir, "settings.json"), `{"activeGroupId":"z"}`)
	rs, err := store.LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.KnownHosts) != store.MaxKnownHosts {
		t.Fatalf("len = %d", len(rs.KnownHosts))
	}
	// Newest (tail) kept: last 300 of a, then all 700 of the active group.
	if rs.KnownHosts[0] != "old500" || rs.KnownHosts[299] != "old799" || rs.KnownHosts[300] != "act0" || rs.KnownHosts[999] != "act699" {
		t.Fatalf("cap kept wrong slice: first=%s last=%s", rs.KnownHosts[0], rs.KnownHosts[999])
	}
}

func TestRulesRoundTrip(t *testing.T) {
	dir := withTempDataDir(t)
	in := store.RuleSet{
		Rules:      map[string]store.RouteMode{"a.com": {Kind: "blocked"}, "*.b.com": {Kind: "direct"}},
		RuleGroups: []store.RuleGroup{{ID: "w", Name: "Work", Domains: []string{"example.com", "=api.x.com"}, Route: store.RouteMode{Kind: "direct"}}},
		FinalRoute: store.RouteMode{Kind: "direct"},
		KnownHosts: []string{"a.com", "c.com"},
	}
	if err := store.SaveRules(in); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip:\n%+v\n%+v", got, in)
	}

	// Nil collections are written as empty; blocked fallback becomes tunnel;
	// profileId never leaks.
	if err := store.SaveRules(store.RuleSet{FinalRoute: store.RouteMode{Kind: "blocked"}}); err != nil {
		t.Fatal(err)
	}
	m := readRaw(t, filepath.Join(dir, "rules.json"))
	if fr, _ := m["finalRoute"].(map[string]any); fr["kind"] != "tunnel" {
		t.Fatalf("finalRoute on disk: %v", m)
	}
	if r, _ := m["rules"].(map[string]any); r == nil {
		t.Fatalf("rules not an object: %v", m)
	}
	if r, _ := m["ruleGroups"].([]any); r == nil {
		t.Fatalf("ruleGroups not an array: %v", m)
	}
	if r, _ := m["knownHosts"].([]any); r == nil {
		t.Fatalf("knownHosts not an array: %v", m)
	}
}

func TestRulesReadNormalizesLegacyKinds(t *testing.T) {
	dir := withTempDataDir(t)
	writeFile(t, filepath.Join(dir, "rules.json"), `{
  "rules": {"a.com": {"kind": "default"}, "b.com": {"kind": "tunnel", "profileId": "p1"}, "c.com": "direct", "d.com": {"kind": "weird"}},
  "ruleGroups": [{"id": "r", "name": "R", "domains": ["x.com"], "route": {"kind": "default"}}]
}`)
	rs, err := store.LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]store.RouteMode{
		"a.com": {Kind: "tunnel"}, "b.com": {Kind: "tunnel"}, "c.com": {Kind: "direct"}, "d.com": {Kind: "tunnel"},
	}
	if !reflect.DeepEqual(rs.Rules, want) || rs.RuleGroups[0].Route.Kind != "tunnel" || rs.FinalRoute.Kind != "tunnel" {
		t.Fatalf("rules = %+v", rs)
	}
	if rs.KnownHosts == nil {
		t.Fatal("knownHosts should default to empty, not nil")
	}
}

// Saving a group before rules.json exists must migrate first, otherwise the
// rewrite would drop the group's legacy rules.
func TestSaveGroupMigratesRulesFirst(t *testing.T) {
	dir := withTempDataDir(t)
	writeFile(t, filepath.Join(dir, "groups", "g.json"), `{"id":"g","name":"G","rules":{"keep.com":"direct"}}`)
	writeFile(t, filepath.Join(dir, "settings.json"), `{"activeGroupId":"g"}`)
	g, err := store.LoadGroup("g")
	if err != nil || g == nil {
		t.Fatalf("load: %v", err)
	}
	g.Name = "Renamed"
	if err := store.SaveGroup(g); err != nil {
		t.Fatal(err)
	}
	if _, has := readRaw(t, filepath.Join(dir, "groups", "g.json"))["rules"]; has {
		t.Fatal("rules not dropped from group file")
	}
	rs, err := store.LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	if rs.Rules["keep.com"].Kind != "direct" {
		t.Fatalf("rules lost: %+v", rs)
	}
}

// A fresh install: MigrateV2 builds groups/default.json from the legacy flat
// files, then LoadRules moves the legacy routes into rules.json.
func TestMigrateV2ThenRulesMigration(t *testing.T) {
	dir := withTempDataDir(t)
	if err := store.SaveRoutes(map[string]string{"x.com": "default", "y.com": "direct"}); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateV2(); err != nil {
		t.Fatal(err)
	}
	rs, err := store.LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]store.RouteMode{"x.com": {Kind: "tunnel"}, "y.com": {Kind: "direct"}}
	if !reflect.DeepEqual(rs.Rules, want) || rs.FinalRoute.Kind != "tunnel" {
		t.Fatalf("rules = %+v", rs)
	}
	g, err := store.LoadGroup(store.DefaultGroupID)
	if err != nil || g == nil {
		t.Fatalf("default group: %v", err)
	}
	g.Name = "Default!"
	if err := store.SaveGroup(g); err != nil {
		t.Fatal(err)
	}
	if _, has := readRaw(t, filepath.Join(dir, "groups", "default.json"))["rules"]; has {
		t.Fatal("rules still on the group after save")
	}
	if rs2, _ := store.LoadRules(); !reflect.DeepEqual(rs2.Rules, want) {
		t.Fatalf("rules after group save = %+v", rs2.Rules)
	}
}

// If rules.json already exists, MigrateV2 writes a slim default group.
func TestMigrateV2WithExistingRulesWritesSlimGroup(t *testing.T) {
	dir := withTempDataDir(t)
	if err := store.SaveRules(store.EmptyRuleSet()); err != nil {
		t.Fatal(err)
	}
	_ = store.SaveRoutes(map[string]string{"x.com": "direct"})
	if err := store.MigrateV2(); err != nil {
		t.Fatal(err)
	}
	m := readRaw(t, filepath.Join(dir, "groups", "default.json"))
	if _, has := m["rules"]; has {
		t.Fatalf("slim group expected: %v", m)
	}
}
