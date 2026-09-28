package tui

import (
	"errors"

	"github.com/hoveychen/netferry/relay/internal/store"
)

var errNoGroup = errors.New("no active profile group")

// editActiveGroup re-reads the active group, applies fn and saves it.
func (d *Data) editActiveGroup(fn func(g *store.Group)) error {
	g, err := d.freshActiveGroup()
	if err != nil {
		return err
	}
	if g == nil {
		return errNoGroup
	}
	fn(g)
	return store.SaveGroup(g)
}

// SetHostRule stores a per-host route override (ruleStore.setRule).
func (d *Data) SetHostRule(host string, mode store.RouteMode) error {
	return d.editActiveGroup(func(g *store.Group) { SetRule(g, host, mode) })
}

// DeleteHostRule removes a per-host override (ruleStore.deleteRule).
func (d *Data) DeleteHostRule(host string) error {
	return d.editActiveGroup(func(g *store.Group) { DeleteRule(g, host) })
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

// PutRuleGroup inserts or replaces a rule group on the active group.
func (d *Data) PutRuleGroup(rg store.RuleGroup) error {
	return d.editActiveGroup(func(g *store.Group) { SaveRuleGroup(g, rg) })
}

// RemoveRuleGroup deletes a rule group from the active group.
func (d *Data) RemoveRuleGroup(id string) error {
	return d.editActiveGroup(func(g *store.Group) { DeleteRuleGroup(g, id) })
}
