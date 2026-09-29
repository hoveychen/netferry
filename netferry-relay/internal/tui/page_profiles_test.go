package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoveychen/netferry/relay/internal/firewall"
	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/store"
)

const testExportKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// isolate points the store and ~ at temp dirs and stubs platform lookups.
func isolate(t *testing.T) (home string) {
	t.Helper()
	t.Setenv("NETFERRY_DATA_DIR", t.TempDir())
	home = t.TempDir()
	oldHome, oldFeat, oldOut, oldKey := homeDir, methodFeatures, stdoutWriter, profile.ExportKey
	homeDir = func() string { return home }
	methodFeatures = func() map[string][]firewall.Feature {
		return map[string][]firewall.Feature{
			"nft":    {firewall.FeatureIPv6, firewall.FeatureBlockUDP, firewall.FeatureDNS},
			"tproxy": {firewall.FeatureIPv6, firewall.FeatureUDP, firewall.FeatureDNS},
		}
	}
	profile.ExportKey = testExportKey
	t.Cleanup(func() { homeDir, methodFeatures, stdoutWriter, profile.ExportKey = oldHome, oldFeat, oldOut, oldKey })
	return home
}

// loadedApp migrates + loads the isolated store into a test App.
func loadedApp(t *testing.T) *App {
	t.Helper()
	if err := store.MigrateV2(); err != nil {
		t.Fatal(err)
	}
	d, err := LoadData()
	if err != nil {
		t.Fatal(err)
	}
	a := newTestApp(d)
	a.width, a.height = 120, 40
	return a
}

// press sends keys through the root model (modal handling included).
func press(a *App, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "ctrl+s":
			msg = tea.KeyMsg{Type: tea.KeyCtrlS}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		default:
			msg = key(k)
		}
		_, cmd := a.Update(msg)
		_ = cmd
	}
}

func typeText(a *App, s string) {
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func TestMigrateV2BootstrapsDefaultGroup(t *testing.T) {
	isolate(t)
	ps := []profile.Profile{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	if err := store.SaveProfiles(ps); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRoutes(map[string]string{"x.com": "tunnel", "y.com": "direct"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSettings(store.GlobalSettings{AutoConnectProfileID: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateV2(); err != nil {
		t.Fatal(err)
	}
	g, err := store.LoadGroup(store.DefaultGroupID)
	if err != nil || g == nil {
		t.Fatalf("default group: %v %v", g, err)
	}
	if strings.Join(g.ChildrenIDs, ",") != "a,b" || g.Name != "Default" {
		t.Fatalf("group = %+v", g)
	}
	rs, err := store.LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	if rs.Rules["x.com"] != (store.RouteMode{Kind: "tunnel"}) || rs.Rules["y.com"].Kind != "direct" {
		t.Fatalf("rules = %+v", rs.Rules)
	}
	s, _ := store.LoadSettings()
	if s.ActiveGroupID != store.DefaultGroupID {
		t.Fatalf("active group = %q", s.ActiveGroupID)
	}
	// Idempotent: a deleted child stays deleted on the next run.
	g.ChildrenIDs = []string{"a"}
	_ = store.SaveGroup(g)
	_ = store.MigrateV2()
	g2, _ := store.LoadGroup(store.DefaultGroupID)
	if len(g2.ChildrenIDs) != 1 {
		t.Fatal("MigrateV2 rewrote an existing default group")
	}
}

func TestSaveProfilesWritesDesktopReadableJSON(t *testing.T) {
	isolate(t)
	if err := store.SaveProfiles([]profile.Profile{{ID: "a", Name: "A"}}); err != nil {
		t.Fatal(err)
	}
	path, _ := store.ProfilesPath()
	raw, _ := os.ReadFile(path)
	var out []map[string]any
	_ = json.Unmarshal(raw, &out)
	if out[0]["subnets"] == nil || out[0]["excludeSubnets"] == nil || out[0]["dns"] != "off" || out[0]["method"] != "auto" {
		t.Fatalf("required desktop fields missing/null: %s", raw)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	home := isolate(t)
	keyPath := filepath.Join(home, ".ssh", "id_test")
	_ = os.MkdirAll(filepath.Dir(keyPath), 0o700)
	_ = os.WriteFile(keyPath, []byte("-----BEGIN KEY-----\nabc\n"), 0o600)
	p := NewProfile("")
	p.Name, p.Remote, p.IdentityFile = "srv", "me@srv", "~/.ssh/id_test"
	p.JumpHosts = []profile.JumpHost{{Remote: "me@jump", IdentityKey: "PEMJUMP"}}
	if !CanShare(p) {
		t.Fatal("should be shareable")
	}
	data, err := ExportProfile(p, home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeProfile(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == p.ID || !got.Imported {
		t.Fatal("import must assign a new id and mark imported")
	}
	if got.IdentityKey != "-----BEGIN KEY-----\nabc\n" || got.IdentityFile != "" || got.JumpHosts[0].IdentityKey != "PEMJUMP" {
		t.Fatalf("identity not inlined: %+v", got)
	}
	if got.PoolSize != 4 || !got.BlockUDPOrDefault() || got.Remote != "me@srv" {
		t.Fatalf("fields lost: %+v", got)
	}
	p.JumpHosts[0].IdentityKey = ""
	if CanShare(p) {
		t.Fatal("jump host without identity must block sharing")
	}
	chunks := QRChunks(strings.Repeat("a", 2500))
	if len(chunks) != 3 || !strings.HasPrefix(chunks[0], "NF:1/3:") || len(chunks[2]) != len("NF:3/3:")+500 {
		t.Fatalf("chunks = %d %q", len(chunks), chunks[2][:10])
	}
}

func TestEditorValidationAndFeatureLinkage(t *testing.T) {
	isolate(t)
	e := newProfileEditor(NewProfile(""), true)
	saved := false
	e.onSave = func(profile.Profile) tea.Cmd { saved = true; return nil }
	e.save()
	if saved || e.form.Errors["remote"] == "" {
		t.Fatalf("empty remote must fail: %v", e.form.Errors)
	}
	e.form.Field("remote").SetText("me@host:2222")
	e.form.Field("subnets").SetText("10.0.0.0/8, bad")
	e.save()
	if saved || !strings.Contains(e.form.Errors["subnets"], "bad") {
		t.Fatalf("bad subnet must fail: %v", e.form.Errors)
	}
	e.form.Field("subnets").SetText("10.0.0.0/8,192.168.0.0/16")
	e.form.Field("fectun").SetOn(true)
	e.form.Field("fectunK").SetText("250")
	e.save()
	if saved || e.form.Errors["fectunK"] == "" {
		t.Fatal("k+m>255 must fail")
	}
	e.form.Field("fectunK").SetText("20")
	e.save()
	if !saved {
		t.Fatalf("valid profile should save: %v", e.form.Errors)
	}
	p := e.collect()
	if p.Fectun == nil || p.Fectun.Port != 55700 || p.Fectun.M != 15 || len(p.Subnets) != 2 {
		t.Fatalf("collect: %+v %+v", p, p.Fectun)
	}

	// Method features: nft has blockUdp but not udp; tproxy the reverse.
	e.form.Field("method").SetValue("nft")
	if !e.form.Field("blockUdp").visible() || e.form.Field("enableUdp").visible() {
		t.Fatal("nft: blockUdp shown, enableUdp hidden")
	}
	e.form.Field("method").SetValue("tproxy")
	if e.form.Field("blockUdp").visible() || !e.form.Field("enableUdp").visible() {
		t.Fatal("tproxy: enableUdp shown, blockUdp hidden")
	}
	e.form.Field("method").SetValue("auto")
	if !e.form.Field("blockUdp").visible() || !e.form.Field("enableUdp").visible() {
		t.Fatal("auto = union of features")
	}
	// tcpBalance only when pool > 1.
	e.form.Field("poolSize").SetText("1")
	if e.form.Field("tcpBalance").visible() {
		t.Fatal("tcpBalance hidden at pool 1")
	}
}

func TestEditorJumpHostsAndNoDataLoss(t *testing.T) {
	isolate(t)
	lb := uint32(123)
	p := profile.Profile{ID: "x", Name: "n", Remote: "a@b", Subnets: []string{"0.0.0.0/0"}, Dns: "specific", DnsTarget: "1.1.1.1",
		Method: "auto", PoolSize: 2, TcpBalance: "round-robin", RemotePython: "/usr/bin/python3", LatencyBufferSize: &lb,
		Notes: "hello", BlockUDP: boolPtr(true), AutoExcludeLAN: boolPtr(false), JumpHosts: []profile.JumpHost{{Remote: "j1@h", IdentityFile: "~/k1"}, {Remote: "j2@h", IdentityKey: "PEM2"}}}
	e := newProfileEditor(p, false)
	got := e.collect()
	gj, _ := json.Marshal(store.NormalizeProfile(got))
	wj, _ := json.Marshal(store.NormalizeProfile(p))
	if string(gj) != string(wj) {
		t.Fatalf("untouched edit changed the profile:\n got %s\nwant %s", gj, wj)
	}
	e.form.Field("jh1.remote").SetText("changed@h")
	e.addJump()
	if len(e.draft.JumpHosts) != 3 || e.form.Field("jh1.remote").Text() != "changed@h" {
		t.Fatal("add jump host lost edits")
	}
	e.removeJump(0)
	got = e.collect()
	if len(got.JumpHosts) != 2 || got.JumpHosts[0].Remote != "changed@h" || got.JumpHosts[0].IdentityKey != "PEM2" {
		t.Fatalf("remove jump host: %+v", got.JumpHosts)
	}
	// Imported profiles keep their locked fields.
	p.Imported = true
	e = newProfileEditor(p, false)
	if e.form.Field("remote").visible() {
		t.Fatal("remote must be hidden for imported profiles")
	}
	if e.collect().JumpHosts[1].IdentityKey != "PEM2" {
		t.Fatal("imported profile lost its jump host key")
	}
}

func TestProfilesPageFlows(t *testing.T) {
	home := isolate(t)
	a := loadedApp(t)
	a.cur = pageProfiles
	pp := a.pages[pageProfiles].(*profilesPage)
	if g := a.data.ActiveGroup(); g == nil || g.ID != "default" {
		t.Fatal("bootstrap should activate the Default group")
	}

	// New profile via the editor.
	press(a, "n")
	if pp.mode != profEdit {
		t.Fatal("n opens the editor")
	}
	pp.editor.form.Field("name").SetText("tokyo")
	pp.editor.form.Field("remote").SetText("me@tokyo")
	press(a, "ctrl+s")
	if pp.mode != profList || len(a.data.Children(a.data.ActiveGroup())) != 1 {
		t.Fatalf("save should add to the group; mode=%v errs=%v", pp.mode, pp.editor)
	}
	assertFits(t, a.View(), a.width, a.height)

	// Import from ssh config → editor prefilled, not imported.
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	_ = os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host bastion\n  HostName 1.2.3.4\n  User ops\n\nHost fra\n  HostName fra.example.com\n  User me\n  Port 2200\n  ProxyJump bastion\n"), 0o600)
	press(a, "i", "enter") // From ~/.ssh/config
	if pp.mode != profMenu || len(pp.menu.items) != 2 {
		t.Fatalf("ssh picker: mode=%v", pp.mode)
	}
	press(a, "down", "enter") // fra
	if pp.mode != profEdit || pp.editor.draft.Remote != "me@fra.example.com:2200" || pp.editor.draft.Imported {
		t.Fatalf("ssh import: %+v", pp.editor.draft)
	}
	if len(pp.editor.draft.JumpHosts) != 1 || pp.editor.draft.JumpHosts[0].Remote != "ops@1.2.3.4" {
		t.Fatalf("jump hosts: %+v", pp.editor.draft.JumpHosts)
	}
	press(a, "ctrl+s")
	children := a.data.Children(a.data.ActiveGroup())
	if len(children) != 2 {
		t.Fatalf("children = %d", len(children))
	}
	// Multi-profile "Connect all" is gone: every row is a profile.
	if rows := pp.rows(); len(rows) != 2 || rows[0].profile == nil {
		t.Fatalf("rows = %+v", rows)
	}
	if strings.Contains(ansi.Strip(a.View()), "Connect all") {
		t.Fatal("Connect all card should not be rendered")
	}

	// Export tokyo to a file and import it back (PEM text identity).
	pp.sel = 0
	tokyo := *pp.selected().profile
	tokyo.IdentityKey = "PEM"
	_ = a.data.UpdateProfile(tokyo)
	a.reload()
	out := filepath.Join(t.TempDir(), "t.nfprofile")
	press(a, "x", "down", "enter") // Save to file
	if pp.mode != profPrompt {
		t.Fatal("export → file prompt")
	}
	pp.prompt.field.SetText(out)
	press(a, "enter")
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("export file: %v (flash %q)", err, a.flash)
	}
	press(a, "i", "down", "enter")
	pp.prompt.field.SetText(out)
	press(a, "enter")
	if n := len(a.data.Children(a.data.ActiveGroup())); n != 3 {
		t.Fatalf("after import children = %d (flash %q)", n, a.flash)
	}

	// Clipboard export writes OSC 52.
	var wrote string
	stdoutWriter = func(s string) { wrote = s }
	press(a, "x", "enter")
	if !strings.HasPrefix(wrote, "\x1b]52;c;") {
		t.Fatal("clipboard export should emit OSC 52")
	}
	// QR view.
	press(a, "x", "down", "down", "enter")
	if pp.mode != profQR || len(pp.qr.chunks) == 0 {
		t.Fatal("QR view")
	}
	a.width, a.height = 200, 80
	if !strings.Contains(a.View(), "█") {
		t.Fatal("QR not drawn at 200x80")
	}
	a.width, a.height = 120, 40
	press(a, "esc")

	// Remove from group keeps the profile.
	pp.sel = 0
	id := pp.selected().profile.ID
	press(a, "R", "y")
	if a.data.Profile(id) == nil || len(a.data.Children(a.data.ActiveGroup())) != 2 {
		t.Fatal("remove from group")
	}

	// New group, rename, switch back, delete the Default group (cascade).
	press(a, "N")
	newID := a.data.Settings.ActiveGroupID
	if newID == "default" || len(a.data.Groups) != 2 {
		t.Fatal("N creates and activates a group")
	}
	press(a, "r")
	pp.prompt.field.SetText("Work")
	press(a, "enter")
	if a.data.ActiveGroup().Name != "Work" {
		t.Fatal("rename")
	}
	press(a, "g")
	for i, it := range pp.menu.items {
		if strings.HasPrefix(it.label, "Default") {
			pp.menu.sel = i
		}
	}
	press(a, "enter")
	if a.data.Settings.ActiveGroupID != "default" {
		t.Fatal("switch group")
	}
	doomed := append([]string(nil), a.data.ActiveGroup().ChildrenIDs...)
	press(a, "X", "y")
	if a.data.Settings.ActiveGroupID != newID || a.data.Group("default") != nil {
		t.Fatalf("delete group: active=%q", a.data.Settings.ActiveGroupID)
	}
	for _, id := range doomed {
		if a.data.Profile(id) != nil {
			t.Fatal("deleting a group deletes its profiles")
		}
	}
	if a.data.Profile(id) == nil {
		t.Fatal("a profile only removed from the group must survive")
	}
	assertFits(t, a.View(), a.width, a.height)
}

func TestGroupEditsReadFreshState(t *testing.T) {
	isolate(t)
	a := loadedApp(t)
	d := a.data // deliberately never reloaded
	p1, p2 := NewProfile(""), NewProfile("")
	if err := d.AddProfile(p1); err != nil {
		t.Fatal(err)
	}
	if err := d.AddProfile(p2); err != nil {
		t.Fatal(err)
	}
	g, _ := store.LoadGroup(store.DefaultGroupID)
	if len(g.ChildrenIDs) != 2 {
		t.Fatalf("second add overwrote the first: %v", g.ChildrenIDs)
	}
}
