package tui

import (
	"github.com/hoveychen/netferry/relay/internal/store"
)

// editRules re-reads rules.json, applies fn and saves it, so read-modify-write
// edits never overwrite a change made since d was loaded (the desktop may be
// editing the same store).
func (d *Data) editRules(fn func(rs *store.RuleSet)) error {
	rs, err := store.LoadRules()
	if err != nil {
		return err
	}
	fn(&rs)
	return store.SaveRules(rs)
}

// SetHostRule stores a per-host route override (ruleStore.setRule).
func (d *Data) SetHostRule(host string, mode store.RouteMode) error {
	return d.editRules(func(g *store.RuleSet) { SetRule(g, host, mode) })
}

// DeleteHostRule removes a per-host override (ruleStore.deleteRule).
func (d *Data) DeleteHostRule(host string) error {
	return d.editRules(func(g *store.RuleSet) { DeleteRule(g, host) })
}

// SetHostPriority updates priorities.json (ruleStore.setPriority).
func (d *Data) SetHostPriority(host string, p int) error {
	prios, err := store.LoadPriorities()
	if err != nil {
		return err
	}
	if prios == nil {
		prios = map[string]int{}
	}
	SetPriority(prios, host, p)
	return store.SavePriorities(prios)
}

// PutRuleGroup inserts or replaces a rule group in the global rules.
func (d *Data) PutRuleGroup(rg store.RuleGroup) error {
	return d.editRules(func(g *store.RuleSet) { SaveRuleGroup(g, rg) })
}

// MoveRuleGroup reorders a rule group in the global rules by delta.
func (d *Data) MoveRuleGroup(id string, delta int) error {
	return d.editRules(func(g *store.RuleSet) { MoveRuleGroup(g, id, delta) })
}

// SetFinalRoute sets the global fallback route (tunnel/direct).
func (d *Data) SetFinalRoute(mode store.RouteMode) error {
	return d.editRules(func(g *store.RuleSet) { g.FinalRoute = store.NormalizeFinalRoute(mode) })
}

// RemoveRuleGroup deletes a rule group from the global rules.
func (d *Data) RemoveRuleGroup(id string) error {
	return d.editRules(func(g *store.RuleSet) { DeleteRuleGroup(g, id) })
}
