package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hoveychen/netferry/relay/internal/catalog"
	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/stats"
	"github.com/hoveychen/netferry/relay/internal/store"
)

type destMode int

const (
	destBrowse destMode = iota
	destEditor
	destMenu
)

const overviewSiteLimit = 20

// destinationsPage is DestinationsPage: an overview of scope groups,
// suggestions, IPs and ungrouped sites, drilling into per-host rules.
type destinationsPage struct {
	app  *App
	mode destMode

	scope        string // "" overview, "group:<id>", "site:<site>", "ip"
	filter       textinput.Model
	filterOn     bool
	showAllSites bool
	sel          int
	selKey       string
	overviewKey  string // selection to restore when leaving a drill-down

	menu   *menu
	editor *ruleGroupEditor

	cache      *destModel
	cacheData  *Data
	cacheDests []stats.DestinationSnapshot
}

func newDestinationsPage(a *App) *destinationsPage {
	f := textinput.New()
	f.Prompt = ""
	f.Placeholder = "Search scopes, services, or destinations…"
	f.CharLimit = 256
	return &destinationsPage{app: a, filter: f}
}

func (p *destinationsPage) title() string { return "Destinations" }

func (p *destinationsPage) capturing() bool { return p.mode != destBrowse || p.filterOn }

// model returns the derived view model, rebuilt when the store or the live
// snapshot changed.
func (p *destinationsPage) model() *destModel {
	a := p.app
	d := a.live.dests
	same := len(d) == len(p.cacheDests) && (len(d) == 0 || &d[0] == &p.cacheDests[0])
	if p.cache == nil || p.cacheData != a.data || !same {
		p.cache, p.cacheData, p.cacheDests = buildDestModel(a.data, d), a.data, d
	}
	return p.cache
}

func (p *destinationsPage) children() []profile.Profile {
	return p.app.data.Children(p.app.data.ActiveGroup())
}

func (p *destinationsPage) query() string { return strings.TrimSpace(p.filter.Value()) }

// ── rows ─────────────────────────────────────────────────────────────────────

// destItem is one line (or card) of the list. Headers and notes are not
// selectable.
type destItem struct {
	key    string
	lines  func(selected bool, width int) []string
	enter  func() tea.Cmd
	edit   func() tea.Cmd // e
	create func() tea.Cmd // +
	host   string         // drill-down rows
	draft  bool
}

func (it destItem) selectable() bool { return it.key != "" }

func header(text string) destItem {
	return destItem{lines: func(bool, int) []string { return []string{"", sSection.Render(text)} }}
}

func note(style lipgloss.Style, text string) destItem {
	return destItem{lines: func(_ bool, w int) []string { return []string{style.Render(truncate(text, w))} }}
}

func marker(selected bool) string {
	if selected {
		return sAccent.Render("▸ ")
	}
	return "  "
}

// rightAlign lays left and right on one line of width w, truncating left.
func rightAlign(left, right string, w int) string {
	rw := lipgloss.Width(right)
	lw := w - rw - 1
	if lw < 4 {
		lw = 4
	}
	left = truncate(left, lw)
	return left + strings.Repeat(" ", max(1, w-lipgloss.Width(left)-rw)) + right
}

func contains(s, q string) bool { return strings.Contains(strings.ToLower(s), q) }

func anyContains(list []string, q string) bool {
	for _, s := range list {
		if contains(s, q) {
			return true
		}
	}
	return false
}

func (p *destinationsPage) routeLabel(m store.RouteMode) string {
	for _, o := range routeOptions(p.children(), true) {
		if o.Value == routeKey(m) {
			return o.Label
		}
	}
	if m.Kind == "tunnel" {
		return "Tunnel"
	}
	return "Default"
}

func routeStyle(m store.RouteMode) lipgloss.Style {
	switch m.Kind {
	case "direct":
		return sOK
	case "blocked":
		return sErr
	}
	return sAccent
}

func priorityStyle(v int) lipgloss.Style {
	switch v {
	case 1, 2:
		return sMuted
	case 4:
		return sWarn
	case 5:
		return sErr
	}
	return sAccent
}

func (p *destinationsPage) overviewItems(m *destModel) []destItem {
	q := strings.ToLower(p.query())
	var items []destItem

	items = append(items, header(fmt.Sprintf("NAMED SCOPE GROUPS · %d", len(m.ruleGroups))))
	if len(m.ruleGroups) == 0 {
		items = append(items, note(sMuted, "No scope groups yet. Review a routing suggestion below to create one."))
	}
	for i, g := range m.ruleGroups {
		if q != "" && !contains(g.Name, q) && !anyContains(g.Domains, q) {
			continue
		}
		i, g := i, g
		n := len(m.groupHosts(i))
		items = append(items, destItem{
			key: "g:" + g.ID,
			lines: func(sel bool, w int) []string {
				right := routeStyle(g.Route).Render(p.routeLabel(g.Route)) + sMuted.Render(fmt.Sprintf("%6d", n))
				return []string{
					rightAlign(marker(sel)+sBold.Render(g.Name), right, w),
					"    " + sMuted.Render(truncate(strings.Join(g.Domains, " · "), w-4)),
				}
			},
			enter: func() tea.Cmd { p.drill("group:" + g.ID); return nil },
			edit:  func() tea.Cmd { p.openEditor(&g, ""); return nil },
		})
	}

	if len(m.routing) > 0 {
		items = append(items, header(fmt.Sprintf("ROUTING SCOPES · %d", len(m.routing))+sDim.Render("   V2Fly · GFWList · official regions")),
			note(sDim, "Mainland access and GFWList membership are separate signals; review and save before routing changes."))
		for _, s := range m.routing {
			if q != "" && !contains(s.Name, q) && !contains(s.NameZh, q) && !anyContains(s.Hosts, q) {
				continue
			}
			s := s
			detail := s.Source + " · " + map[bool]string{true: "Direct", false: "Default tunnel"}[s.SuggestedRoute == "direct"]
			if s.CoveredHosts > 0 {
				detail += fmt.Sprintf(" · %d already grouped", s.CoveredHosts)
			}
			items = append(items, destItem{
				key: "r:" + s.ID,
				lines: func(sel bool, w int) []string {
					return []string{
						rightAlign(marker(sel)+sBold.Render(s.Name), sMuted.Render(fmt.Sprintf("%d", len(s.Hosts)))+sAccent.Render("  review"), w),
						"    " + sMuted.Render(truncate(detail, w-4)),
					}
				},
				enter: func() tea.Cmd { p.openRouting(s); return nil },
			})
		}
	}

	if len(m.services) > 0 {
		rev := catalog.Services().Revision
		if len(rev) > 7 {
			rev = rev[:7]
		}
		items = append(items, header(fmt.Sprintf("RECOGNIZED SERVICES · %d", len(m.services))+sDim.Render("   V2Fly · "+rev)),
			note(sDim, "Suggested from observed destinations and an offline service catalog; routing never changes automatically."))
		for _, s := range m.services {
			if q != "" && !contains(s.Name, q) && !contains(s.NameZh, q) && !anyContains(s.Hosts, q) {
				continue
			}
			s := s
			shown := s.Hosts
			if len(shown) > 3 {
				shown = shown[:3]
			}
			items = append(items, destItem{
				key: "s:" + s.ID,
				lines: func(sel bool, w int) []string {
					return []string{
						rightAlign(marker(sel)+sBold.Render(s.Name), sMuted.Render(fmt.Sprintf("%d", len(s.Hosts)))+sAccent.Render("  review"), w),
						"    " + sMuted.Render(truncate(strings.Join(shown, " · "), w-4)),
					}
				},
				enter: func() tea.Cmd { p.openService(s); return nil },
			})
		}
	}

	if len(m.addresses) > 0 && (q == "" || anyContains(m.addresses, q)) {
		items = append(items, header("UNATTRIBUTED DESTINATIONS"), destItem{
			key: "ip",
			lines: func(sel bool, w int) []string {
				return []string{rightAlign(marker(sel)+"IP addresses", sMuted.Render(fmt.Sprintf("%d", len(m.addresses))), w)}
			},
			enter: func() tea.Cmd { p.drill("ip"); return nil },
		}, note(sDim, "An IP may be shared by several services; it is never assigned to a service automatically."))
	}

	items = append(items, header(fmt.Sprintf("UNGROUPED SITES · %d", len(m.sites))))
	var sites []siteGroup
	for _, sg := range m.sites {
		if q == "" || contains(sg.site, q) || anyContains(sg.hosts, q) {
			sites = append(sites, sg)
		}
	}
	if q == "" && !p.showAllSites && len(sites) > overviewSiteLimit {
		sites = sites[:overviewSiteLimit]
	}
	for _, sg := range sites {
		sg := sg
		items = append(items, destItem{
			key: "site:" + sg.site,
			lines: func(sel bool, w int) []string {
				return []string{rightAlign(marker(sel)+sg.site, sMuted.Render(fmt.Sprintf("%d", len(sg.hosts))), w)}
			},
			enter:  func() tea.Cmd { p.drill("site:" + sg.site); return nil },
			create: func() tea.Cmd { p.openEditor(nil, sg.site); return nil },
		})
	}
	if q == "" && len(m.sites) > overviewSiteLimit {
		label := fmt.Sprintf("Show all %d sites", len(m.sites))
		if p.showAllSites {
			label = "Collapse site list"
		}
		items = append(items, destItem{
			key:   "more",
			lines: func(sel bool, w int) []string { return []string{marker(sel) + sAccent.Render(label)} },
			enter: func() tea.Cmd { p.showAllSites = !p.showAllSites; return nil },
		})
	}
	if len(m.sites) == 0 && len(m.hosts) == 0 {
		items = append(items, note(sMuted, "No destinations observed yet. Connect the group and traffic will populate this list."))
	}
	return items
}

// scopeHosts are the hosts of the current drill-down scope.
func (p *destinationsPage) scopeHosts(m *destModel) []string {
	switch {
	case strings.HasPrefix(p.scope, "group:"):
		id := strings.TrimPrefix(p.scope, "group:")
		for i, g := range m.ruleGroups {
			if g.ID == id {
				return m.groupHosts(i)
			}
		}
		return nil
	case strings.HasPrefix(p.scope, "site:"):
		site := strings.TrimPrefix(p.scope, "site:")
		for _, sg := range m.sites {
			if sg.site == site {
				return sg.hosts
			}
		}
		return nil
	case p.scope == "ip":
		return m.addresses
	}
	return nil
}

func (p *destinationsPage) filteredHosts(m *destModel) []string {
	hosts := p.scopeHosts(m)
	q := strings.ToLower(p.query())
	if q == "" {
		return hosts
	}
	var out []string
	for _, h := range hosts {
		if strings.Contains(strings.ToLower(h), q) {
			out = append(out, h)
		}
	}
	return out
}

// showDraft: the typed query looks like a rule target that is not known yet.
func (p *destinationsPage) showDraft(m *destModel) bool {
	q := p.query()
	return m.group != nil && p.scope != "" && q != "" && (strings.HasPrefix(q, "*.") || strings.Contains(q, ".")) && !m.known[q]
}

func (p *destinationsPage) drillItems(m *destModel) []destItem {
	var items []destItem
	if p.showDraft(m) {
		items = append(items, p.hostItem(m, p.query(), true))
	}
	for _, h := range p.filteredHosts(m) {
		items = append(items, p.hostItem(m, h, false))
	}
	return items
}

func (p *destinationsPage) hostItem(m *destModel, host string, draft bool) destItem {
	key := "h:" + host
	if draft {
		key = "draft"
	}
	return destItem{key: key, host: host, draft: draft, lines: func(sel bool, w int) []string {
		return []string{p.renderHost(m, host, draft, sel, w)}
	}, enter: func() tea.Cmd { p.openRouteMenu(host); return nil }}
}

func (p *destinationsPage) renderHost(m *destModel, host string, draft, sel bool, w int) string {
	route, via := resolveRoute(host, m.effective)
	prio := resolvePriority(host, m.priorities)
	blocked, direct := route.Kind == "blocked", route.Kind == "direct"

	left := marker(sel)
	nameS := lipgloss.NewStyle()
	switch {
	case draft:
		left += "  "
	case blocked:
		left += sErr.Render("● ")
		nameS = sStrike
	case direct:
		left += sOK.Render("● ")
	default:
		left += "  "
	}
	if sel {
		nameS = nameS.Bold(true)
	}
	left += nameS.Render(host)

	var badges []string
	_, override := m.rules[host]
	switch {
	case draft:
		badges = append(badges, sAccent.Render("[New rule]"))
	case via != "":
		badges = append(badges, sDim.Render("via "+via))
	case !override:
		if gi := m.matcher.group(host); gi >= 0 {
			badges = append(badges, sDim.Render(m.ruleGroups[gi].Name))
		}
	}
	if children := p.children(); !draft && len(children) > 1 && !blocked && !direct {
		if live, ok := m.live[host]; ok && live.ActiveProfileID != "" {
			for i, c := range children {
				if c.ID == live.ActiveProfileID {
					badges = append(badges, tunnelStyle(i).Render("live: "+c.Name))
				}
			}
		}
	}
	if len(badges) > 0 {
		left += "  " + strings.Join(badges, " ")
	}

	right := routeStyle(route).Render(p.routeLabel(route)) + "  " + priorityStyle(prio).Render(padRight(priorityLabels[prio], 4))
	if override {
		right += sMuted.Render(" ✎")
	} else {
		right += "  "
	}
	return rightAlign(left, right, w)
}

func (p *destinationsPage) items() []destItem {
	m := p.model()
	if m.group == nil {
		return nil
	}
	if p.scope == "" {
		return p.overviewItems(m)
	}
	return p.drillItems(m)
}

// selected resolves the selection (tracked by key, so live updates that
// reorder rows keep the cursor on the same entry).
func (p *destinationsPage) selected(items []destItem) int {
	for i, it := range items {
		if it.selectable() && it.key == p.selKey {
			p.sel = i
			return i
		}
	}
	if len(items) == 0 {
		p.sel = 0
		return -1
	}
	if p.sel >= len(items) {
		p.sel = len(items) - 1
	}
	if p.sel < 0 {
		p.sel = 0
	}
	for i := p.sel; i < len(items); i++ {
		if items[i].selectable() {
			p.sel, p.selKey = i, items[i].key
			return i
		}
	}
	for i := p.sel; i >= 0; i-- {
		if items[i].selectable() {
			p.sel, p.selKey = i, items[i].key
			return i
		}
	}
	return -1
}

func (p *destinationsPage) moveSel(items []destItem, dir, steps int) {
	i := p.selected(items)
	if i < 0 {
		return
	}
	for ; steps > 0; steps-- {
		j := i + dir
		for j >= 0 && j < len(items) && !items[j].selectable() {
			j += dir
		}
		if j < 0 || j >= len(items) {
			break
		}
		i = j
	}
	p.sel, p.selKey = i, items[i].key
}

// ── navigation ───────────────────────────────────────────────────────────────

func (p *destinationsPage) drill(scope string) {
	p.overviewKey = p.selKey
	p.scope, p.sel, p.selKey = scope, 0, ""
	p.filter.SetValue("")
}

func (p *destinationsPage) back() {
	p.scope, p.sel, p.selKey = "", 0, p.overviewKey
	p.filter.SetValue("")
}

func (p *destinationsPage) closeSub() tea.Cmd {
	p.mode, p.menu, p.editor = destBrowse, nil, nil
	return nil
}

// do reloads after a store write and flashes the outcome.
func (p *destinationsPage) do(err error, ok string) tea.Cmd {
	p.app.reload()
	if err != nil {
		return p.app.setFlash(false, err.Error())
	}
	return p.app.setFlash(true, ok)
}

func (p *destinationsPage) openRouteMenu(host string) {
	m := p.model()
	cur, _ := resolveRoute(host, m.effective)
	mn := &menu{title: "Route for " + host}
	for i, o := range routeOptions(p.children(), true) {
		o := o
		detail := ""
		if o.Value == routeKey(cur) {
			detail, mn.sel = "✓", i
		}
		mn.items = append(mn.items, menuItem{label: o.Label, detail: detail, run: func() tea.Cmd {
			return p.do(p.app.data.SetHostRule(host, parseRouteKey(o.Value)), "Rule saved for "+host)
		}})
	}
	p.menu, p.mode = mn, destMenu
}

func (p *destinationsPage) openPriorityMenu(host string) {
	cur := resolvePriority(host, p.model().priorities)
	mn := &menu{title: "Priority for " + host}
	for v := 1; v <= 5; v++ {
		v := v
		detail := ""
		if v == cur {
			detail, mn.sel = "✓", v-1
		}
		mn.items = append(mn.items, menuItem{label: priorityStyle(v).Render(priorityLabels[v]), detail: detail, run: func() tea.Cmd {
			return p.do(p.app.data.SetHostPriority(host, v), "Priority saved for "+host)
		}})
	}
	p.menu, p.mode = mn, destMenu
}

// ── rule-group editor ────────────────────────────────────────────────────────

func (p *destinationsPage) findGroupByName(names ...string) *store.RuleGroup {
	for _, g := range p.model().ruleGroups {
		for _, n := range names {
			if n != "" && g.Name == n {
				g := g
				return &g
			}
		}
	}
	return nil
}

// openEditor edits group, or starts a new one (optionally from a site, which
// becomes an exact scope when it is not a valid domain scope).
func (p *destinationsPage) openEditor(group *store.RuleGroup, site string) {
	rg := store.RuleGroup{ID: store.NewID(), Route: store.RouteMode{Kind: "default"}}
	if group != nil {
		rg = *group
		rg.Domains = append([]string(nil), group.Domains...)
	} else if site != "" {
		rg.Name = site
		scope := site
		if _, ok := store.NormalizeDomain(site); !ok {
			scope = "=" + site
		}
		rg.Domains = []string{scope}
	}
	p.startEditor(rg, group != nil, nil)
}

func (p *destinationsPage) openService(s catalog.ServiceSuggestion) {
	g := p.findGroupByName(s.Name, s.NameZh)
	rg := store.RuleGroup{ID: store.NewID(), Name: s.Name, Route: store.RouteMode{Kind: "default"}}
	if g != nil {
		rg = *g
	}
	rg.Domains = mergeDomains(rg.Domains, s.Domains)
	p.startEditor(rg, g != nil, nil)
}

func (p *destinationsPage) openRouting(s catalog.RoutingSuggestion) {
	g := p.findGroupByName(s.Name, s.NameZh)
	rg := store.RuleGroup{ID: store.NewID(), Name: s.Name, Route: parseRouteKey(s.SuggestedRoute)}
	if g != nil {
		rg = *g
	}
	rg.Domains = mergeDomains(rg.Domains, s.Domains)
	p.startEditor(rg, g != nil, s.Evidence)
}

func (p *destinationsPage) startEditor(rg store.RuleGroup, exists bool, evidence []catalog.Evidence) {
	a := p.app
	e := newRuleGroupEditor(rg, exists, p.model(), p.children())
	e.evidence = evidence
	e.onCancel = p.closeSub
	e.onSave = func(saved store.RuleGroup) tea.Cmd {
		p.closeSub()
		return p.do(a.data.PutRuleGroup(saved), "Scope group saved")
	}
	e.onDelete = func() tea.Cmd {
		a.confirm("Delete scope group?", "Delete this scope group? Its destinations will use their individual rules or the default route.", func() tea.Cmd {
			p.closeSub()
			if p.scope == "group:"+rg.ID {
				p.back()
			}
			return p.do(a.data.RemoveRuleGroup(rg.ID), "Scope group deleted")
		})
		return nil
	}
	p.editor, p.mode = e, destEditor
}

// ── update ───────────────────────────────────────────────────────────────────

func (p *destinationsPage) update(msg tea.Msg) tea.Cmd {
	switch p.mode {
	case destEditor:
		return p.editor.update(msg)
	case destMenu:
		if km, ok := msg.(tea.KeyMsg); ok {
			mn := p.menu
			cmd, done := mn.update(km)
			if done && p.menu == mn {
				p.closeSub()
			}
			return cmd
		}
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if p.app.data.ActiveGroup() == nil {
		return nil
	}
	if p.filterOn {
		switch km.String() {
		case "esc", "enter", "up", "down":
			p.filterOn = false
			p.filter.Blur()
			return nil
		}
		var cmd tea.Cmd
		p.filter, cmd = p.filter.Update(msg)
		return cmd
	}

	items := p.items()
	cur := p.selected(items)
	var it *destItem
	if cur >= 0 {
		it = &items[cur]
	}
	switch km.String() {
	case "up", "k":
		p.moveSel(items, -1, 1)
	case "down", "j":
		p.moveSel(items, 1, 1)
	case "pgup":
		p.moveSel(items, -1, 10)
	case "pgdown":
		p.moveSel(items, 1, 10)
	case "home":
		p.sel, p.selKey = 0, ""
	case "end":
		p.sel, p.selKey = len(items)-1, ""
	case "/":
		p.filterOn = true
		p.filter.CursorEnd()
		return p.filter.Focus()
	case "esc":
		if p.scope != "" {
			p.back()
		} else {
			p.filter.SetValue("")
		}
	case "enter":
		if it != nil && it.enter != nil {
			return it.enter()
		}
	}
	if p.scope == "" {
		switch km.String() {
		case "n":
			p.openEditor(nil, "")
		case "e":
			if it != nil && it.edit != nil {
				return it.edit()
			}
		case "+", "a":
			if it != nil && it.create != nil {
				return it.create()
			}
		}
		return nil
	}
	switch km.String() {
	case "w":
		if s := wildcardSuggestion(p.query(), p.model().known); s != "" {
			p.filter.SetValue(s)
			p.sel, p.selKey = 0, ""
		}
	}
	if it == nil || it.host == "" {
		return nil
	}
	switch km.String() {
	case "r":
		p.openRouteMenu(it.host)
	case "p":
		p.openPriorityMenu(it.host)
	case "x", "delete":
		if _, ok := p.model().rules[it.host]; ok {
			return p.do(p.app.data.DeleteHostRule(it.host), "Override removed for "+it.host)
		}
	}
	return nil
}

// ── view ─────────────────────────────────────────────────────────────────────

func (p *destinationsPage) hints() string {
	switch p.mode {
	case destEditor:
		return p.editor.hints()
	case destMenu:
		return ""
	}
	if p.app.data.ActiveGroup() == nil {
		return ""
	}
	if p.filterOn {
		return hints("enter/esc", "done")
	}
	if p.scope == "" {
		return hints("enter", "open", "e", "edit group", "+", "group from site", "n", "new scope group", "/", "search")
	}
	return hints("enter/r", "route", "p", "priority", "x", "reset override", "w", "wildcard", "/", "search", "esc", "back")
}

func (p *destinationsPage) view(width, height int) string {
	switch p.mode {
	case destEditor:
		return p.editor.view(width, height)
	case destMenu:
		return p.menu.view(width, height)
	}
	m := p.model()
	if m.group == nil {
		return sSection.Render("DESTINATION RULES") + "\n\n" +
			sMuted.Render("No active profile group. Rules cannot be edited until a group is selected (Profiles page, g).")
	}

	var head []string
	if p.scope == "" {
		head = append(head, sTitle.Render("Destination Rules")+"  "+sMuted.Render("Manage routing scopes before services and individual hosts."))
	} else {
		head = append(head, sMuted.Render("← ")+sTitle.Render(p.scopeTitle(m)))
		info := sMuted.Render(fmt.Sprintf("%d / %d hosts", len(p.filteredHosts(m)), len(p.scopeHosts(m))))
		if s := wildcardSuggestion(p.query(), m.known); s != "" {
			info += "   " + sKey.Render("w") + sAccent.Render(" Create "+s)
		} else if len(m.hosts) == 0 {
			info += "   " + sDim.Render("Tip: type *.example.com to set one rule for all subdomains.")
		}
		head[0] += "   " + info
	}
	p.filter.Width = width - 8
	search := sMuted.Render("/ ")
	if p.filterOn {
		search = sAccent.Render("/ ")
	}
	head = append(head, search+p.filter.View())

	items := p.items()
	cur := p.selected(items)
	var lines []string
	selLine := 0
	for i, it := range items {
		if i == cur {
			selLine = len(lines)
		}
		lines = append(lines, it.lines(i == cur, width)...)
	}
	if p.scope != "" && len(items) == 0 {
		if len(m.hosts) == 0 {
			lines = append(lines, sMuted.Render("No destinations observed yet. Connect the group and traffic will populate this list."))
		} else {
			lines = append(lines, sMuted.Render("No hosts match the filter."))
		}
	}
	bodyH := height - len(head) - 1
	// Keep a card's second line in view with its first.
	return strings.Join(head, "\n") + "\n" + strings.Join(windowAround(lines, selLine+1, bodyH), "\n")
}

func (p *destinationsPage) scopeTitle(m *destModel) string {
	switch {
	case strings.HasPrefix(p.scope, "group:"):
		id := strings.TrimPrefix(p.scope, "group:")
		for _, g := range m.ruleGroups {
			if g.ID == id {
				return g.Name
			}
		}
		return "(deleted group)"
	case strings.HasPrefix(p.scope, "site:"):
		return strings.TrimPrefix(p.scope, "site:")
	}
	return "IP addresses"
}
