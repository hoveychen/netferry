package main

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hoveychen/netferry/relay/internal/store"
)

// destinationsModel renders the active group's known_hosts plus its rules,
// and lets the user cycle each entry's RouteMode through tunnel/direct/blocked.
//
// In single-profile mode (no active group) we display routes.json (the
// pre-group host->mode map) instead.
type destinationsModel struct {
	root      *rootModel
	cursor    int
	lastError string
}

func newDestinationsModel(root *rootModel) destinationsModel {
	return destinationsModel{root: root}
}

func (d destinationsModel) Init() tea.Cmd { return nil }

func (d *destinationsModel) update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	hosts := d.hosts()
	switch km.String() {
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
	case "down", "j":
		if d.cursor+1 < len(hosts) {
			d.cursor++
		}
	case "t", "T":
		return d.cycleMode(hosts, "tunnel")
	case "d", "D":
		return d.cycleMode(hosts, "direct")
	case "b", "B":
		return d.cycleMode(hosts, "blocked")
	}
	return nil
}

// hosts returns the union of (a) active group's known_hosts + rules keys, or
// (b) routes.json keys when there's no active group, sorted alphabetically.
func (d *destinationsModel) hosts() []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	if g := d.activeGroup(); g != nil {
		for _, h := range g.KnownHosts {
			add(h)
		}
		for h := range g.Rules {
			add(h)
		}
	} else {
		for h := range d.root.data.routes {
			add(h)
		}
	}
	sort.Strings(out)
	return out
}

func (d *destinationsModel) activeGroup() *store.Group {
	id := d.root.data.settings.ActiveGroupID
	if id == "" {
		return nil
	}
	return findGroupByID(d.root.data.groups, id)
}

func (d *destinationsModel) cycleMode(hosts []string, target string) tea.Cmd {
	if len(hosts) == 0 {
		return nil
	}
	host := hosts[d.cursor]
	if g := d.activeGroup(); g != nil {
		if g.Rules == nil {
			g.Rules = map[string]store.RouteMode{}
		}
		cur := g.Rules[host].Kind
		if cur == target {
			delete(g.Rules, host)
		} else {
			g.Rules[host] = store.RouteMode{Kind: target}
		}
		if err := store.SaveGroup(g); err != nil {
			return d.root.setFlash(false, "save group: "+err.Error())
		}
		d.root.reload()
		return d.root.setFlash(true, "rule updated")
	}
	if d.root.data.routes == nil {
		d.root.data.routes = map[string]string{}
	}
	if d.root.data.routes[host] == target {
		delete(d.root.data.routes, host)
	} else {
		d.root.data.routes[host] = target
	}
	if err := store.SaveRoutes(d.root.data.routes); err != nil {
		return d.root.setFlash(false, "save routes: "+err.Error())
	}
	return d.root.setFlash(true, "route updated")
}

// ── view ─────────────────────────────────────────────────────────────────────

func (d *destinationsModel) view(width, height int) string {
	var b strings.Builder
	g := d.activeGroup()
	if g != nil {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render("Group: " + g.Name))
	} else {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render("Solo (routes.json)"))
	}
	b.WriteByte('\n')
	hosts := d.hosts()
	if len(hosts) == 0 {
		b.WriteString(dimText.Render("(no destinations recorded yet — connect, then revisit this tab)"))
		b.WriteByte('\n')
		return b.String()
	}
	// Reserve 3 lines for the title, blank line, and footer hint.
	listHeight := height - 3
	if listHeight < 3 {
		listHeight = 3
	}
	start, end := windowedRange(len(hosts), d.cursor, listHeight)
	if start > 0 {
		b.WriteString(dimText.Render(fmt.Sprintf("  ↑ %d more above", start)))
		b.WriteByte('\n')
	}
	for i := start; i < end; i++ {
		h := hosts[i]
		mode := d.modeFor(g, h)
		row := fmt.Sprintf("%-50s %s", h, modeChip(mode))
		if i == d.cursor {
			row = "▶ " + listSelected.Render(fmt.Sprintf("%-50s %s", h, mode))
		} else {
			row = "  " + row
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
	if end < len(hosts) {
		b.WriteString(dimText.Render(fmt.Sprintf("  ↓ %d more below", len(hosts)-end)))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(dimText.Render("[↑/↓] move  [t] tunnel  [d] direct  [b] blocked  (toggle off when same)"))
	return b.String()
}

func (d *destinationsModel) modeFor(g *store.Group, host string) string {
	if g != nil {
		if rm, ok := g.Rules[host]; ok {
			return rm.Kind
		}
		return "default"
	}
	if v, ok := d.root.data.routes[host]; ok {
		return v
	}
	return "tunnel"
}

func modeChip(mode string) string {
	switch mode {
	case "tunnel":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("tunnel")
	case "direct":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("direct")
	case "blocked":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("blocked")
	default:
		return dimText.Render(mode)
	}
}
