package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/store"
)

// withTempDataDir points NETFERRY_DATA_DIR at a fresh tmp dir for the test
// body and restores the previous value afterward.
func withTempDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, hadPrev := os.LookupEnv("NETFERRY_DATA_DIR")
	t.Setenv("NETFERRY_DATA_DIR", dir)
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("NETFERRY_DATA_DIR", prev)
		} else {
			os.Unsetenv("NETFERRY_DATA_DIR")
		}
	})
	return dir
}

func TestProfilesRoundTrip(t *testing.T) {
	withTempDataDir(t)

	// Empty load on first run.
	got, err := store.LoadProfiles()
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}

	bU := false
	bA := true
	p := profile.Profile{
		ID:             "p1",
		Name:           "test",
		Remote:         "user@host",
		IdentityFile:   "~/.ssh/id_ed25519",
		Subnets:        []string{"0.0.0.0/0"},
		Dns:            profile.DnsAll,
		ExcludeSubnets: []string{},
		Method:         "auto",
		BlockUDP:       &bA,
		AutoExcludeLAN: &bA,
		EnableUDP:      bU,
		PoolSize:       4,
		Notes:          "hello",
	}
	if _, err := store.UpsertProfile(p); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err = store.LoadProfiles()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(got) != 1 || got[0].ID != "p1" || got[0].Notes != "hello" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// Update preserves slot.
	p.Notes = "updated"
	if _, err := store.UpsertProfile(p); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = store.LoadProfiles()
	if got[0].Notes != "updated" {
		t.Fatalf("update did not persist: %+v", got[0])
	}

	// Remove.
	if _, err := store.RemoveProfile("p1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got, _ = store.LoadProfiles()
	if len(got) != 0 {
		t.Fatalf("expected empty after remove, got %d", len(got))
	}
}

// TestProfilesAcceptDesktopShape verifies that a JSON file written in the
// exact shape the Tauri Rust side emits (camelCase, all defaults explicit)
// loads cleanly through our Go decoder. The fixture is the literal
// `serde_json::to_string_pretty` output for a default Profile.
func TestProfilesAcceptDesktopShape(t *testing.T) {
	dir := withTempDataDir(t)
	fixture := `[
  {
    "id": "abc-123",
    "name": "Default",
    "remote": "root@1.2.3.4",
    "identityFile": "~/.ssh/id_ed25519",
    "jumpHosts": [],
    "subnets": ["0.0.0.0/0"],
    "dns": "all",
    "excludeSubnets": [],
    "autoNets": false,
    "method": "auto",
    "disableIpv6": false,
    "enableUdp": false,
    "blockUdp": true,
    "autoExcludeLan": true,
    "poolSize": 4,
    "splitConn": false,
    "tcpBalanceMode": "least-loaded",
    "latencyBufferSize": 2097152,
    "imported": false
  }
]`
	if err := os.WriteFile(filepath.Join(dir, "profiles.json"), []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got, err := store.LoadProfiles()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(got))
	}
	p := got[0]
	if p.ID != "abc-123" || p.Name != "Default" || p.Method != "auto" {
		t.Fatalf("basic fields wrong: %+v", p)
	}
	if !p.BlockUDPOrDefault() {
		t.Fatalf("BlockUDPOrDefault: expected true")
	}
	if !p.AutoExcludeLANOrDefault() {
		t.Fatalf("AutoExcludeLANOrDefault: expected true")
	}
	if p.LatencyBufferSize == nil || *p.LatencyBufferSize != 2097152 {
		t.Fatalf("LatencyBufferSize: %v", p.LatencyBufferSize)
	}
}

func TestGroupsLegacyNormalize(t *testing.T) {
	dir := withTempDataDir(t)
	// Pre-children-ids format: full embedded children, no childrenIds.
	legacy := `{
  "id": "g1",
  "name": "Legacy",
  "children": [
    {
      "id": "p-x",
      "name": "X",
      "remote": "r@h",
      "identityFile": "k",
      "subnets": ["0.0.0.0/0"],
      "dns": "all",
      "excludeSubnets": [],
      "autoNets": false,
      "method": "auto",
      "disableIpv6": false,
      "enableUdp": false
    }
  ]
}`
	groupsDir := filepath.Join(dir, "groups")
	if err := os.MkdirAll(groupsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(groupsDir, "g1.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	g, err := store.LoadGroup("g1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if g == nil {
		t.Fatal("group not loaded")
	}
	if len(g.ChildrenIDs) != 1 || g.ChildrenIDs[0] != "p-x" {
		t.Fatalf("childrenIds not normalized: %+v", g.ChildrenIDs)
	}
	if len(g.LegacyChildren) != 0 {
		t.Fatalf("legacy children should be cleared after normalize: %+v", g.LegacyChildren)
	}

	// File on disk must have been rewritten without the legacy field.
	raw, err := os.ReadFile(filepath.Join(groupsDir, "g1.json"))
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if _, has := generic["children"]; has {
		t.Fatalf("rewritten group should not contain `children`, got: %s", string(raw))
	}
	if ids, ok := generic["childrenIds"].([]any); !ok || len(ids) != 1 {
		t.Fatalf("rewritten group missing childrenIds: %s", string(raw))
	}
}

func TestGroupsCRUD(t *testing.T) {
	withTempDataDir(t)

	g := &store.Group{
		ID:          "g1",
		Name:        "main",
		ChildrenIDs: []string{"p1", "p2"},
		Rules: map[string]store.RouteMode{
			"example.com": {Kind: "tunnel", ProfileID: "p2"},
			"badhost":     {Kind: "blocked"},
		},
		Priorities: map[string]int{"example.com": 5},
		KnownHosts: []string{"example.com", "badhost", "other"},
	}
	if err := store.SaveGroup(g); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.LoadGroup("g1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil || got.Name != "main" {
		t.Fatalf("load mismatch: %+v", got)
	}
	if got.Rules["example.com"].Kind != "tunnel" || got.Rules["example.com"].ProfileID != "p2" {
		t.Fatalf("rules round-trip: %+v", got.Rules)
	}
	if got.Priorities["example.com"] != 5 {
		t.Fatalf("priorities: %+v", got.Priorities)
	}

	all, err := store.ListGroups()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 || all[0].ID != "g1" {
		t.Fatalf("list: %+v", all)
	}

	if err := store.DeleteGroup("g1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := store.LoadGroup("g1"); got != nil {
		t.Fatalf("deleted group still loadable: %+v", got)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	withTempDataDir(t)

	s, err := store.LoadSettings()
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if s.TrayDisplayMode != "speed" {
		t.Fatalf("default tray mode: %+v", s)
	}

	s.AutoConnectProfileID = "p1"
	s.ActiveGroupID = "g1"
	s.TrayDisplayMode = "rtt"
	if err := store.SaveSettings(s); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.LoadSettings()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got != s {
		t.Fatalf("round-trip: %+v vs %+v", got, s)
	}
}

func TestPrioritiesAndRoutes(t *testing.T) {
	withTempDataDir(t)

	if err := store.SavePriorities(map[string]int{"a": 1, "b": -2}); err != nil {
		t.Fatalf("save pri: %v", err)
	}
	pri, _ := store.LoadPriorities()
	if pri["a"] != 1 || pri["b"] != -2 {
		t.Fatalf("priorities: %+v", pri)
	}

	if err := store.SaveRoutes(map[string]string{"a": "tunnel", "b": "blocked"}); err != nil {
		t.Fatalf("save routes: %v", err)
	}
	r, _ := store.LoadRoutes()
	if r["a"] != "tunnel" || r["b"] != "blocked" {
		t.Fatalf("routes: %+v", r)
	}
}
