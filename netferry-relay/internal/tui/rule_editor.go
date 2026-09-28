package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hoveychen/netferry/relay/internal/catalog"
	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/store"
)

// ruleGroupEditor is the DestinationsPage rule-group dialog.
type ruleGroupEditor struct {
	id       string
	exists   bool
	form     *Form
	model    *destModel
	evidence []catalog.Evidence
	banner   string
	onSave   func(store.RuleGroup) tea.Cmd
	onDelete func() tea.Cmd
	onCancel func() tea.Cmd
}

// routeOptions lists Default, one Tunnel per group child, Direct, Blocked.
func routeOptions(children []profile.Profile, withDefaultChild bool) []Option {
	def, prefix := "Default tunnel", ""
	if withDefaultChild {
		def, prefix = "Default", "Tunnel: "
		if len(children) > 0 {
			def = "Default (→ " + children[0].Name + ")"
		}
	}
	opts := []Option{{def, "default"}}
	for _, c := range children {
		opts = append(opts, Option{prefix + c.Name, "tunnel:" + c.ID})
	}
	return append(opts, Option{"Direct", "direct"}, Option{"Blocked", "blocked"})
}

func newRuleGroupEditor(rg store.RuleGroup, exists bool, m *destModel, children []profile.Profile) *ruleGroupEditor {
	e := &ruleGroupEditor{id: rg.ID, exists: exists, model: m}
	name := textField("name", "Name", rg.Name, "e.g. Streaming")
	domains := areaField("domains", "Domain scopes", strings.Join(rg.Domains, "\n"), 6)
	domains.Help = "example.com includes itself and all subdomains; =api.example.com matches only that host."
	route := selectField("route", "Route", routeOptions(children, false), routeKey(rg.Route))
	e.form = newForm(name, domains, route)
	return e
}

func (e *ruleGroupEditor) draft() (rg store.RuleGroup, invalid []string) {
	fl := e.form.Field
	valid, invalid := normalizeDomains(splitDomains(fl("domains").Raw()))
	return store.RuleGroup{ID: e.id, Name: fl("name").Text(), Domains: valid, Route: parseRouteKey(fl("route").Value())}, invalid
}

func (e *ruleGroupEditor) save() tea.Cmd {
	rg, invalid := e.draft()
	switch {
	case rg.Name == "":
		e.banner = "the group needs a name"
		e.form.FocusKey("name")
	case len(invalid) > 0:
		e.banner = "fix the invalid domains first"
		e.form.FocusKey("domains")
	case len(rg.Domains) == 0:
		e.banner = "add at least one domain"
		e.form.FocusKey("domains")
	default:
		e.banner = ""
		return e.onSave(rg)
	}
	return nil
}

func (e *ruleGroupEditor) update(msg tea.Msg) tea.Cmd {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "ctrl+s":
			return e.save()
		case "esc":
			return e.onCancel()
		case "ctrl+d":
			if e.exists {
				return e.onDelete()
			}
			return nil
		}
	}
	cmd, _ := e.form.Update(msg)
	return cmd
}

// preview is the dialog's live summary: matched hosts, overlap with other
// groups and hosts whose per-host rule overrides the group.
func (e *ruleGroupEditor) preview() (hosts []string, overlap, overrides int) {
	rg, _ := e.draft()
	mt := newGroupMatcher([]store.RuleGroup{rg})
	others := make([]store.RuleGroup, 0, len(e.model.ruleGroups))
	for _, g := range e.model.ruleGroups {
		if g.ID != e.id {
			others = append(others, g)
		}
	}
	om := newGroupMatcher(others)
	for _, h := range e.model.hosts {
		if mt.group(h) < 0 {
			continue
		}
		hosts = append(hosts, h)
		if om.group(h) >= 0 {
			overlap++
		}
		if _, ok := e.model.rules[h]; ok {
			overrides++
		}
	}
	return hosts, overlap, overrides
}

func (e *ruleGroupEditor) view(width, height int) string {
	title := "New scope group"
	if e.exists {
		title = "Edit scope group"
	}
	head := sTitle.Render(title)
	if e.banner != "" {
		head += "\n" + sErr.Render("✗ "+e.banner)
	}
	var tail []string
	if _, invalid := e.draft(); len(invalid) > 0 {
		tail = append(tail, sErr.Render("Invalid scopes: "+strings.Join(invalid, ", ")))
	}
	hosts, overlap, overrides := e.preview()
	tail = append(tail, sSection.Render(fmt.Sprintf("MATCHES %d OBSERVED DESTINATIONS", len(hosts))))
	if len(hosts) > 0 {
		shown := hosts
		if len(shown) > 8 {
			shown = shown[:8]
		}
		tail = append(tail, sMuted.Render(truncate(strings.Join(shown, " · "), width)))
	}
	if overlap > 0 || overrides > 0 {
		tail = append(tail, sWarn.Width(width).Render(fmt.Sprintf("%d destinations already belong to other scope groups, and %d have individual rules. Saving may change overlapping routes; individual rules still win.", overlap, overrides)))
	}
	if len(e.evidence) > 0 {
		tail = append(tail, "", sSection.Render("OFFICIAL PRODUCT AVAILABILITY"))
		for _, ev := range e.evidence {
			tail = append(tail, truncate(ev.Product+" · "+strings.TrimPrefix(ev.Domain, "=")+"  "+sDim.Render(ev.URL), width))
		}
	}
	tailS := strings.Join(tail, "\n")
	bodyH := height - strings.Count(head, "\n") - strings.Count(tailS, "\n") - 4
	return head + "\n\n" + e.form.scrollWindow(e.form.View(width), bodyH) + "\n\n" + tailS
}

func (e *ruleGroupEditor) hints() string {
	h := []string{"tab", "field", "←/→", "route", "ctrl+s", "save"}
	if e.exists {
		h = append(h, "ctrl+d", "delete")
	}
	return hints(append(h, "esc", "cancel")...)
}
