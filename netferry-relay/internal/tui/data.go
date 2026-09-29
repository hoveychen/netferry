package tui

import (
	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/stats"
	"github.com/hoveychen/netferry/relay/internal/store"
)

// MaxKnownHosts caps a group's persisted knownHosts, keeping the newest
// (ruleStore.ts MAX_KNOWN_HOSTS).
const MaxKnownHosts = 1000

// Data is everything the TUI reads from the desktop's app-data store.
type Data struct {
	Profiles   []profile.Profile
	Groups     []store.Group
	Settings   store.GlobalSettings
	Priorities map[string]int // global priorities.json
}

// LoadData reads profiles, groups, settings and priorities from disk.
func LoadData() (*Data, error) {
	ps, err := store.LoadProfiles()
	if err != nil {
		return nil, err
	}
	gs, err := store.ListGroups()
	if err != nil {
		return nil, err
	}
	st, err := store.LoadSettings()
	if err != nil {
		return nil, err
	}
	pr, err := store.LoadPriorities()
	if err != nil {
		return nil, err
	}
	if pr == nil {
		pr = map[string]int{}
	}
	return &Data{Profiles: ps, Groups: gs, Settings: st, Priorities: pr}, nil
}

// Profile returns the profile with id, or nil.
func (d *Data) Profile(id string) *profile.Profile { return store.FindProfile(d.Profiles, id) }

// Group returns the group with id, or nil.
func (d *Data) Group(id string) *store.Group {
	for i := range d.Groups {
		if d.Groups[i].ID == id {
			return &d.Groups[i]
		}
	}
	return nil
}

// ActiveGroup returns the group selected in settings, or nil.
func (d *Data) ActiveGroup() *store.Group {
	if d.Settings.ActiveGroupID == "" {
		return nil
	}
	return d.Group(d.Settings.ActiveGroupID)
}

// Children resolves a group's childrenIds to profiles in order, skipping
// ids that no longer exist (groupStore.ts joinGroupProfiles).
func (d *Data) Children(g *store.Group) []profile.Profile {
	if g == nil {
		return nil
	}
	var out []profile.Profile
	for _, id := range g.ChildrenIDs {
		if p := d.Profile(id); p != nil {
			out = append(out, *p)
		}
	}
	return out
}

// Rules builds what the desktop pushes to the tunnel: global priorities and
// the active group's route table (per-host overrides, ordered rule groups,
// fallback). Matching itself happens in stats.
func (d *Data) Rules() Rules {
	r := Rules{Priorities: d.Priorities}
	if g := d.ActiveGroup(); g != nil {
		r.Routes = RouteTableFor(g)
	}
	return r
}

// RouteTableFor converts a group's raw rules into the tunnel's route table.
func RouteTableFor(g *store.Group) stats.RouteTable {
	t := stats.RouteTable{
		Overrides: make(map[string]stats.RouteMode, len(g.Rules)),
		Groups:    make([]stats.RouteGroup, 0, len(g.RuleGroups)),
		Final:     toStatsRoute(store.NormalizeFinalRoute(g.FinalRoute)),
	}
	for k, v := range g.Rules {
		t.Overrides[k] = toStatsRoute(v)
	}
	for _, rg := range g.RuleGroups {
		t.Groups = append(t.Groups, stats.RouteGroup{
			Domains: append([]string(nil), rg.Domains...),
			Route:   toStatsRoute(rg.Route),
		})
	}
	return t
}

func toStatsRoute(m store.RouteMode) stats.RouteMode {
	return stats.RouteMode{Kind: stats.NormalizeRouteKind(m.Kind)}
}

// RecordKnownHosts appends newly observed hosts to g.KnownHosts (deduped,
// capped to the newest MaxKnownHosts). Reports whether anything changed, so
// callers only save when needed.
func RecordKnownHosts(g *store.Group, hosts []string) bool {
	seen := make(map[string]bool, len(g.KnownHosts))
	for _, h := range g.KnownHosts {
		seen[h] = true
	}
	changed := false
	for _, h := range hosts {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		g.KnownHosts = append(g.KnownHosts, h)
		changed = true
	}
	if len(g.KnownHosts) > MaxKnownHosts {
		g.KnownHosts = append([]string(nil), g.KnownHosts[len(g.KnownHosts)-MaxKnownHosts:]...)
	}
	return changed
}

// SetRule stores a per-host route override on g (only DeleteRule removes an
// override).
func SetRule(g *store.Group, host string, mode store.RouteMode) {
	if g.Rules == nil {
		g.Rules = map[string]store.RouteMode{}
	}
	g.Rules[host] = mode
}

// DeleteRule removes a per-host override.
func DeleteRule(g *store.Group, host string) { delete(g.Rules, host) }

// SetPriority updates the global priority map; 3 (normal) removes the entry.
func SetPriority(prios map[string]int, host string, p int) {
	if p == stats.DefaultPriority {
		delete(prios, host)
		return
	}
	prios[host] = p
}

// SaveRuleGroup inserts or replaces rg (by id) on g.
func SaveRuleGroup(g *store.Group, rg store.RuleGroup) {
	for i := range g.RuleGroups {
		if g.RuleGroups[i].ID == rg.ID {
			g.RuleGroups[i] = rg
			return
		}
	}
	g.RuleGroups = append(g.RuleGroups, rg)
}

// MoveRuleGroup shifts the rule group with id by delta positions (clamped).
// Order matters: the first matching rule group wins. Reports whether it moved.
func MoveRuleGroup(g *store.Group, id string, delta int) bool {
	from := -1
	for i := range g.RuleGroups {
		if g.RuleGroups[i].ID == id {
			from = i
			break
		}
	}
	if from < 0 {
		return false
	}
	to := from + delta
	if to < 0 {
		to = 0
	}
	if to >= len(g.RuleGroups) {
		to = len(g.RuleGroups) - 1
	}
	if to == from {
		return false
	}
	rg := g.RuleGroups[from]
	g.RuleGroups = append(g.RuleGroups[:from], g.RuleGroups[from+1:]...)
	g.RuleGroups = append(g.RuleGroups[:to], append([]store.RuleGroup{rg}, g.RuleGroups[to:]...)...)
	return true
}

// DeleteRuleGroup removes the rule group with id from g.
func DeleteRuleGroup(g *store.Group, id string) {
	out := g.RuleGroups[:0]
	for _, rg := range g.RuleGroups {
		if rg.ID != id {
			out = append(out, rg)
		}
	}
	g.RuleGroups = out
}
