package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/stats"
	"github.com/hoveychen/netferry/relay/internal/store"
)

func TestWildcardResolution(t *testing.T) {
	if got := wildcardCandidates("a.b.example.com"); !reflect.DeepEqual(got, []string{"*.b.example.com", "*.example.com", "*.com"}) {
		t.Fatalf("candidates = %v", got)
	}
	if wildcardCandidates("*.x.com") != nil || wildcardCandidates("localhost") != nil {
		t.Fatal("wildcards and single labels have no candidates")
	}
	routes := map[string]store.RouteMode{"*.example.com": {Kind: "direct"}, "=x": {Kind: "blocked"}, "api.example.com": {Kind: "blocked"}}
	if m, via := resolveRoute("a.b.example.com", routes); m.Kind != "direct" || via != "*.example.com" {
		t.Fatalf("wildcard route = %v via %q", m, via)
	}
	if m, via := resolveRoute("api.example.com", routes); m.Kind != "blocked" || via != "" {
		t.Fatalf("exact route = %v via %q", m, via)
	}
	if m, _ := resolveRoute("example.com", routes); m.Kind != "default" {
		t.Fatal("the apex is not matched by *.example.com")
	}
	if resolvePriority("a.example.com", map[string]int{"*.example.com": 5}) != 5 || resolvePriority("z.org", nil) != 3 {
		t.Fatal("priority resolution")
	}
}

func TestWildcardSuggestion(t *testing.T) {
	cases := map[string]string{
		"api.example.com": "*.example.com",
		"example.com":     "*.example.com",
		"*.example.com":   "",
		"localhost":       "",
		"":                "",
	}
	for in, want := range cases {
		if got := wildcardSuggestion(in, nil); got != want {
			t.Errorf("wildcardSuggestion(%q) = %q, want %q", in, got, want)
		}
	}
	if wildcardSuggestion("api.example.com", map[string]bool{"*.example.com": true}) != "" {
		t.Error("an existing wildcard is not suggested again")
	}
}

func TestNormalizeDomainsSplitsAndDedupes(t *testing.T) {
	valid, invalid := normalizeDomains(splitDomains("Example.com, =API.x.com\nexample.com.\nbad..x\n\n"))
	if !reflect.DeepEqual(valid, []string{"example.com", "=api.x.com"}) || !reflect.DeepEqual(invalid, []string{"bad..x"}) {
		t.Fatalf("valid=%v invalid=%v", valid, invalid)
	}
}

func TestDestModelBuckets(t *testing.T) {
	d := &Data{
		Priorities: map[string]int{"prio.org": 4},
		Groups: []store.Group{{ID: "g", Name: "G",
			RuleGroups: []store.RuleGroup{{ID: "w", Name: "Work", Domains: []string{"example.com"}, Route: store.RouteMode{Kind: "direct"}}},
			Rules:      map[string]store.RouteMode{"rule.net": {Kind: "blocked"}},
			KnownHosts: []string{"a.example.com", "1.2.3.4", "x.foo.co.uk", "y.foo.co.uk", "::1"},
		}},
		Settings: store.GlobalSettings{ActiveGroupID: "g"},
	}
	m := buildDestModel(d, []stats.DestinationSnapshot{{Host: "live.io"}})
	if want := []string{"1.2.3.4", "::1", "a.example.com", "live.io", "prio.org", "rule.net", "x.foo.co.uk", "y.foo.co.uk"}; !reflect.DeepEqual(m.hosts, want) {
		t.Fatalf("hosts = %v", m.hosts)
	}
	if !reflect.DeepEqual(m.addresses, []string{"1.2.3.4", "::1"}) {
		t.Fatalf("addresses = %v", m.addresses)
	}
	if m.sites[0].site != "foo.co.uk" || len(m.sites[0].hosts) != 2 {
		t.Fatalf("sites = %v (the largest site sorts first, by registrable domain)", m.sites)
	}
	for _, s := range m.sites {
		if s.site == "example.com" {
			t.Fatal("hosts covered by a scope group are not ungrouped sites")
		}
	}
	if got := m.groupHosts(0); !reflect.DeepEqual(got, []string{"a.example.com"}) {
		t.Fatalf("group hosts = %v", got)
	}
	if r, via := resolveRoute("a.example.com", m.effective); r.Kind != "direct" || via != "*.example.com" {
		t.Fatalf("compiled group route = %v via %q", r, via)
	}
}

// destApp is an app with two profiles in the Default group, one scope group
// and some observed hosts, on the Destinations page.
func destApp(t *testing.T) *App {
	t.Helper()
	isolate(t)
	if err := store.SaveProfiles([]profile.Profile{{ID: "pa", Name: "Alpha", Remote: "u@a"}, {ID: "pb", Name: "Beta", Remote: "u@b"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateV2(); err != nil {
		t.Fatal(err)
	}
	g, err := store.LoadGroup(store.DefaultGroupID)
	if err != nil || g == nil {
		t.Fatal(err)
	}
	g.RuleGroups = []store.RuleGroup{{ID: "w", Name: "Work", Domains: []string{"example.com"}, Route: store.RouteMode{Kind: "direct"}}}
	g.KnownHosts = []string{"a.example.com", "b.example.com", "www.youtube.com", "i.ytimg.com", "api.openai.com", "1.2.3.4"}
	if err := store.SaveGroup(g); err != nil {
		t.Fatal(err)
	}
	a := loadedApp(t)
	a.cur = pageDestinations
	return a
}

func screen(a *App) string { return ansi.Strip(a.View()) }

func diskGroup(t *testing.T) *store.Group {
	t.Helper()
	g, err := store.LoadGroup(store.DefaultGroupID)
	if err != nil || g == nil {
		t.Fatal(err)
	}
	return g
}

func TestDestinationsOverview(t *testing.T) {
	a := destApp(t)
	s := screen(a)
	for _, want := range []string{"NAMED SCOPE GROUPS · 1", "Work", "example.com", "Direct", "ROUTING SCOPES", "RECOGNIZED SERVICES", "YouTube", "IP addresses", "UNGROUPED SITES"} {
		if !strings.Contains(s, want) {
			t.Errorf("overview is missing %q:\n%s", want, s)
		}
	}
	assertFits(t, a.View(), a.width, a.height)
	a.width, a.height = 60, 20
	assertFits(t, a.View(), a.width, a.height)
	a.width, a.height = 120, 40

	// Search filters every section.
	press(a, "/")
	typeText(a, "youtube")
	s = screen(a)
	if strings.Contains(s, "Work") || !strings.Contains(s, "YouTube") {
		t.Fatalf("filter did not apply:\n%s", s)
	}
	// While typing, global shortcuts are text.
	typeText(a, "q")
	if a.quitting {
		t.Fatal("q in the search box quit the app")
	}
}

func TestDestinationsHostRules(t *testing.T) {
	a := destApp(t)
	p := a.pages[pageDestinations].(*destinationsPage)
	press(a, "enter") // first row: the Work group
	if p.scope != "group:w" {
		t.Fatalf("scope = %q", p.scope)
	}
	s := screen(a)
	if !strings.Contains(s, "a.example.com") || !strings.Contains(s, "via *.example.com") || !strings.Contains(s, "2 / 2 hosts") {
		t.Fatalf("drill-down:\n%s", s)
	}

	// Route menu: Default, Tunnel: Alpha, Tunnel: Beta, Direct(current), Blocked.
	press(a, "r")
	if !strings.Contains(screen(a), "Tunnel: Beta") {
		t.Fatalf("route menu:\n%s", screen(a))
	}
	press(a, "down", "enter")
	if r := diskGroup(t).Rules["a.example.com"]; r.Kind != "blocked" {
		t.Fatalf("rule on disk = %v", r)
	}
	if r := a.session.rules.Routes["a.example.com"]; r.Kind != "blocked" {
		t.Fatalf("rule pushed to the session = %v", r)
	}
	if !strings.Contains(screen(a), "Blocked") {
		t.Fatal("row does not show the override")
	}

	// Pin to a group child.
	press(a, "r", "up", "up", "enter")
	if r := diskGroup(t).Rules["a.example.com"]; r.Kind != "tunnel" || r.ProfileID != "pb" {
		t.Fatalf("pinned rule = %v", r)
	}

	// Priority: Norm → High.
	press(a, "p", "down", "enter")
	prios, _ := store.LoadPriorities()
	if prios["a.example.com"] != 4 {
		t.Fatalf("priorities = %v", prios)
	}
	// Back to Norm removes the entry.
	press(a, "p", "up", "enter")
	prios, _ = store.LoadPriorities()
	if _, ok := prios["a.example.com"]; ok {
		t.Fatal("Norm should delete the priority entry")
	}

	press(a, "x")
	if _, ok := diskGroup(t).Rules["a.example.com"]; ok {
		t.Fatal("x did not remove the override")
	}

	// Draft rule for a wildcard nobody has visited.
	press(a, "/")
	typeText(a, "*.new.dev")
	press(a, "enter")
	if !strings.Contains(screen(a), "[New rule]") {
		t.Fatalf("no draft row:\n%s", screen(a))
	}
	press(a, "r", "end", "enter") // the menu opens on Default; Blocked is last
	if r, ok := diskGroup(t).Rules["*.new.dev"]; !ok || r.Kind != "blocked" {
		t.Fatalf("draft rule = %v %v", r, ok)
	}

	press(a, "esc", "esc")
	if p.scope != "" {
		t.Fatal("esc should return to the overview")
	}
}

func TestDestinationsWildcardSuggestionKey(t *testing.T) {
	a := destApp(t)
	p := a.pages[pageDestinations].(*destinationsPage)
	press(a, "enter", "/")
	typeText(a, "api.shop.example.com")
	press(a, "enter")
	if !strings.Contains(screen(a), "Create *.shop.example.com") {
		t.Fatalf("no wildcard hint:\n%s", screen(a))
	}
	press(a, "w")
	if p.query() != "*.shop.example.com" {
		t.Fatalf("w set the filter to %q", p.query())
	}
}

func TestDestinationsScopeGroupEditor(t *testing.T) {
	a := destApp(t)
	p := a.pages[pageDestinations].(*destinationsPage)

	press(a, "n")
	if p.mode != destEditor {
		t.Fatal("n should open the editor")
	}
	typeText(a, "Video")
	press(a, "tab")
	typeText(a, "youtube.com")
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	typeText(a, "bad..x")
	press(a, "ctrl+s")
	if p.mode != destEditor || !strings.Contains(screen(a), "Invalid scopes: bad..x") {
		t.Fatalf("invalid scope saved:\n%s", screen(a))
	}
	for i := 0; i < 6; i++ {
		a.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if !strings.Contains(screen(a), "MATCHES 1 OBSERVED DESTINATIONS") {
		t.Fatalf("preview:\n%s", screen(a))
	}
	press(a, "ctrl+s")
	if p.mode != destBrowse {
		t.Fatalf("editor still open:\n%s", screen(a))
	}
	g := diskGroup(t)
	if len(g.RuleGroups) != 2 || g.RuleGroups[1].Name != "Video" || !reflect.DeepEqual(g.RuleGroups[1].Domains, []string{"youtube.com"}) || g.RuleGroups[1].Route.Kind != "default" {
		t.Fatalf("rule groups = %+v", g.RuleGroups)
	}
	if r := a.session.rules.Routes["*.youtube.com"]; r.Kind != "default" {
		t.Fatalf("compiled routes not pushed: %v", a.session.rules.Routes)
	}

	// Edit Work: route Direct → Blocked, then delete it.
	press(a, "home", "e")
	if p.mode != destEditor || !p.editor.exists {
		t.Fatal("e should edit the selected group")
	}
	press(a, "tab", "tab", "right")
	press(a, "ctrl+s")
	if g := diskGroup(t); g.RuleGroups[0].Route.Kind != "blocked" {
		t.Fatalf("route = %v", g.RuleGroups[0].Route)
	}
	press(a, "home", "e")
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	press(a, "y")
	if g := diskGroup(t); len(g.RuleGroups) != 1 || g.RuleGroups[0].Name != "Video" {
		t.Fatalf("delete left %+v", g.RuleGroups)
	}
}

func TestDestinationsSuggestionsOpenEditor(t *testing.T) {
	a := destApp(t)
	p := a.pages[pageDestinations].(*destinationsPage)
	m := p.model()

	var region int = -1
	for i, s := range m.routing {
		if s.ID == "region-limited" {
			region = i
		}
	}
	if region < 0 {
		t.Fatalf("api.openai.com should suggest region-limited: %+v", m.routing)
	}
	p.openRouting(m.routing[region])
	if len(p.editor.evidence) == 0 || !strings.Contains(screen(a), "OFFICIAL PRODUCT AVAILABILITY") {
		t.Fatalf("no evidence:\n%s", screen(a))
	}
	press(a, "ctrl+s")
	g := diskGroup(t)
	if len(g.RuleGroups) != 2 || g.RuleGroups[1].Route.Kind != m.routing[region].SuggestedRoute {
		t.Fatalf("routing group = %+v", g.RuleGroups)
	}

	// A service suggestion merges into the same-named group.
	m = p.model()
	var yt int = -1
	for i, s := range m.services {
		if s.ID == "youtube" {
			yt = i
		}
	}
	if yt < 0 {
		t.Fatalf("no YouTube suggestion: %+v", m.services)
	}
	if err := a.data.PutRuleGroup(store.RuleGroup{ID: "yt", Name: "YouTube", Domains: []string{"=keep.me"}, Route: store.RouteMode{Kind: "direct"}}); err != nil {
		t.Fatal(err)
	}
	a.reload()
	p.openService(m.services[yt])
	if !p.editor.exists || p.editor.id != "yt" {
		t.Fatal("service suggestion should open the existing same-named group")
	}
	press(a, "ctrl+s")
	for _, rg := range diskGroup(t).RuleGroups {
		if rg.ID == "yt" && (rg.Domains[0] != "=keep.me" || len(rg.Domains) < 2 || rg.Route.Kind != "direct") {
			t.Fatalf("merge lost data: %+v", rg)
		}
	}

	// A site becomes an exact scope when it is not a valid domain scope.
	p.openEditor(nil, "1.2.3.4")
	if got := p.editor.form.Field("domains").Text(); got != "=1.2.3.4" {
		t.Fatalf("site scope = %q", got)
	}
}

func TestRecordObservedHostsKeepsDiskEdits(t *testing.T) {
	a := destApp(t)
	// Another writer (the desktop, or a page) adds a rule after our load.
	g := diskGroup(t)
	g.Rules = map[string]store.RouteMode{"late.com": {Kind: "direct"}}
	if err := store.SaveGroup(g); err != nil {
		t.Fatal(err)
	}
	a.recordObservedHosts([]stats.DestinationSnapshot{{Host: "fresh.io"}})
	g = diskGroup(t)
	if g.Rules["late.com"].Kind != "direct" {
		t.Fatal("recording hosts overwrote a concurrent rule edit")
	}
	if g.KnownHosts[len(g.KnownHosts)-1] != "fresh.io" {
		t.Fatalf("known hosts = %v", g.KnownHosts)
	}
}

func TestDestinationsNoGroup(t *testing.T) {
	a := newTestApp(nil)
	a.width, a.height = 80, 20
	a.cur = pageDestinations
	if !strings.Contains(screen(a), "No active profile group") {
		t.Fatal(screen(a))
	}
	press(a, "n", "/")
	if a.pages[pageDestinations].capturing() {
		t.Fatal("keys should be inert without a group")
	}
}
