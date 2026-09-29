package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxKnownHosts caps the persisted knownHosts list, keeping the newest (tail).
const MaxKnownHosts = 1000

// RuleSet is the single global routing configuration stored in
// <DataDir>/rules.json. It applies to whichever profile is connected; profile
// groups no longer carry rules. Routing precedence: Rules (per-host
// overrides) > RuleGroups (ordered, first match wins) > FinalRoute.
type RuleSet struct {
	Rules      map[string]RouteMode `json:"rules"`
	RuleGroups []RuleGroup          `json:"ruleGroups"`
	// FinalRoute is the fallback for traffic no rule matches (Clash MATCH).
	// Only tunnel/direct; missing reads as tunnel.
	FinalRoute RouteMode `json:"finalRoute"`
	KnownHosts []string  `json:"knownHosts"`
}

// EmptyRuleSet returns a rule set with no rules and a tunnel fallback.
func EmptyRuleSet() RuleSet {
	return RuleSet{
		Rules:      map[string]RouteMode{},
		RuleGroups: []RuleGroup{},
		FinalRoute: RouteMode{Kind: RouteTunnel},
		KnownHosts: []string{},
	}
}

// normalize applies read/write defaults: non-nil collections, a tunnel/direct
// fallback and the knownHosts cap.
func (r *RuleSet) normalize() {
	if r.Rules == nil {
		r.Rules = map[string]RouteMode{}
	}
	r.RuleGroups = nilToEmpty(r.RuleGroups)
	for i := range r.RuleGroups {
		r.RuleGroups[i].Domains = nilToEmpty(r.RuleGroups[i].Domains)
	}
	r.FinalRoute = NormalizeFinalRoute(r.FinalRoute)
	r.KnownHosts = capKnownHosts(nilToEmpty(r.KnownHosts))
}

func capKnownHosts(h []string) []string {
	if len(h) > MaxKnownHosts {
		return append([]string(nil), h[len(h)-MaxKnownHosts:]...)
	}
	return h
}

// LoadRules reads rules.json. When the file does not exist it is created by
// migrating the rules that used to live on profile groups (see migrateRules).
func LoadRules() (RuleSet, error) {
	path, err := RulesPath()
	if err != nil {
		return RuleSet{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return RuleSet{}, fmt.Errorf("read rules.json: %w", err)
		}
		rs, err := migrateRules()
		if err != nil {
			return RuleSet{}, err
		}
		if err := SaveRules(rs); err != nil {
			return RuleSet{}, err
		}
		return rs, nil
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return EmptyRuleSet(), nil
	}
	var rs RuleSet
	if err := json.Unmarshal(raw, &rs); err != nil {
		return RuleSet{}, fmt.Errorf("parse rules.json: %w", err)
	}
	rs.normalize()
	return rs, nil
}

// SaveRules atomically writes rules.json. All four fields are always written.
func SaveRules(rs RuleSet) error {
	path, err := RulesPath()
	if err != nil {
		return err
	}
	rs.normalize()
	return writeJSONAtomic(path, rs)
}

// ensureRulesMigrated runs the group→rules.json migration if rules.json does
// not exist yet. Called before any group file is rewritten, since a rewrite
// drops the legacy rule fields the migration reads.
func ensureRulesMigrated() error {
	path, err := RulesPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil || !os.IsNotExist(err) {
		return nil
	}
	_, err = LoadRules()
	return err
}

// legacyGroupRules is the rule-carrying part of a pre-rules.json group file.
type legacyGroupRules struct {
	ID         string               `json:"id"`
	Rules      map[string]RouteMode `json:"rules"`
	RuleGroups []RuleGroup          `json:"ruleGroups"`
	FinalRoute *RouteMode           `json:"finalRoute"`
	KnownHosts []string             `json:"knownHosts"`
}

// migrateRules merges the rules of every groups/*.json into one RuleSet:
//   - rules: non-active groups by id ascending, then the active group, later
//     writers overriding the same key (the active group wins conflicts);
//   - ruleGroups: the active group's first (in order), then the other groups
//     by id ascending, skipping ids already taken;
//   - finalRoute: the active group's, else tunnel;
//   - knownHosts: non-active groups (id ascending) then the active group,
//     deduped keeping the first occurrence, then the newest MaxKnownHosts.
//
// Group files are not modified. No groups yields an empty RuleSet.
func migrateRules() (RuleSet, error) {
	rs := EmptyRuleSet()
	dir, err := GroupsDir()
	if err != nil {
		return rs, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return rs, fmt.Errorf("read groups dir: %w", err)
	}
	var groups []legacyGroupRules
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return rs, fmt.Errorf("read %s: %w", path, err)
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var g legacyGroupRules
		if err := json.Unmarshal(raw, &g); err != nil {
			return rs, fmt.Errorf("parse %s: %w", path, err)
		}
		if g.ID == "" {
			g.ID = strings.TrimSuffix(e.Name(), ".json")
		}
		groups = append(groups, g)
	}
	if len(groups) == 0 {
		return rs, nil
	}

	settings, err := LoadSettings()
	if err != nil {
		settings = DefaultGlobalSettings()
	}
	var active *legacyGroupRules
	var others []legacyGroupRules
	for i := range groups {
		if active == nil && settings.ActiveGroupID != "" && groups[i].ID == settings.ActiveGroupID {
			active = &groups[i]
			continue
		}
		others = append(others, groups[i])
	}
	sort.SliceStable(others, func(i, j int) bool { return others[i].ID < others[j].ID })

	// Precedence order: non-active by id, active last.
	ordered := others
	if active != nil {
		ordered = append(append([]legacyGroupRules(nil), others...), *active)
	}

	for _, g := range ordered {
		for host, mode := range g.Rules {
			rs.Rules[host] = RouteMode{Kind: NormalizeRouteKind(mode.Kind)}
		}
	}

	seenGroup := map[string]bool{}
	addGroups := func(g legacyGroupRules) {
		for _, rg := range g.RuleGroups {
			if seenGroup[rg.ID] {
				continue
			}
			seenGroup[rg.ID] = true
			rg.Route = RouteMode{Kind: NormalizeRouteKind(rg.Route.Kind)}
			rg.Domains = append([]string{}, rg.Domains...)
			rs.RuleGroups = append(rs.RuleGroups, rg)
		}
	}
	if active != nil {
		addGroups(*active)
	}
	for _, g := range others {
		addGroups(g)
	}

	if active != nil && active.FinalRoute != nil {
		rs.FinalRoute = NormalizeFinalRoute(*active.FinalRoute)
	}

	seenHost := map[string]bool{}
	for _, g := range ordered {
		for _, h := range g.KnownHosts {
			if seenHost[h] {
				continue
			}
			seenHost[h] = true
			rs.KnownHosts = append(rs.KnownHosts, h)
		}
	}
	rs.KnownHosts = capKnownHosts(rs.KnownHosts)
	return rs, nil
}
