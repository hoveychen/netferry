package main

import (
	"context"
	"sync"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/store"
)

// tunnelState is the live state of the running engine, exposed to the TUI's
// Connection and Destinations tabs. Nil engine = disconnected.
type tunnelState struct {
	mu sync.Mutex

	engine    *Engine
	cancel    context.CancelFunc
	stopCh    chan struct{}
	doneCh    chan error
	profileID string // active profile ID (single-profile mode)
	groupID   string // active group ID (group mode)
	lastErr   error
}

func (s *tunnelState) snapshot() (engine *Engine, profileID, groupID string, lastErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine, s.profileID, s.groupID, s.lastErr
}

func (s *tunnelState) connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine != nil
}

func (s *tunnelState) setActive(eng *Engine, profileID, groupID string, stopCh chan struct{}, doneCh chan error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engine = eng
	s.profileID = profileID
	s.groupID = groupID
	s.stopCh = stopCh
	s.doneCh = doneCh
	s.lastErr = nil
}

func (s *tunnelState) clearActive(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engine = nil
	s.cancel = nil
	s.stopCh = nil
	s.doneCh = nil
	s.lastErr = err
}

// requestStop closes the engine's stop channel without holding the lock past
// the channel close (so the engine goroutine can complete and clearActive).
func (s *tunnelState) requestStop() {
	s.mu.Lock()
	stop := s.stopCh
	s.stopCh = nil
	s.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

// loadedData is everything the TUI loads from the desktop store on startup.
// Re-loaded on demand whenever the user saves a change.
type loadedData struct {
	profiles []profile.Profile
	groups   []store.Group
	settings store.GlobalSettings
	routes   map[string]string
}

func findProfileByID(ps []profile.Profile, id string) *profile.Profile {
	for i := range ps {
		if ps[i].ID == id {
			return &ps[i]
		}
	}
	return nil
}

func findGroupByID(gs []store.Group, id string) *store.Group {
	for i := range gs {
		if gs[i].ID == id {
			return &gs[i]
		}
	}
	return nil
}

// activeGroup returns a pointer into d.groups for the currently-active group,
// or nil when no group is active. The desktop app treats this as the scope for
// the Profiles list, so the TUI does the same.
func (d *loadedData) activeGroup() *store.Group {
	if d == nil || d.settings.ActiveGroupID == "" {
		return nil
	}
	return findGroupByID(d.groups, d.settings.ActiveGroupID)
}

// scopedProfiles returns the profile list to show in the Profiles tab:
//
//   - When a group is active, the children of that group in childrenIds order
//     (skipping any IDs whose profile no longer exists).
//   - When no group is active, every profile in profiles.json.
func (d *loadedData) scopedProfiles() []profile.Profile {
	g := d.activeGroup()
	if g == nil {
		return d.profiles
	}
	out := make([]profile.Profile, 0, len(g.ChildrenIDs))
	for _, id := range g.ChildrenIDs {
		if pr := findProfileByID(d.profiles, id); pr != nil {
			out = append(out, *pr)
		}
	}
	return out
}

// attachToActiveGroup appends profileID to the active group's ChildrenIDs and
// persists. No-op when no group is active or the profile is already a child.
// Returns true when the group was modified.
func (d *loadedData) attachToActiveGroup(profileID string) (bool, error) {
	g := d.activeGroup()
	if g == nil {
		return false, nil
	}
	for _, id := range g.ChildrenIDs {
		if id == profileID {
			return false, nil
		}
	}
	updated := *g
	updated.ChildrenIDs = append(append([]string{}, g.ChildrenIDs...), profileID)
	if err := store.SaveGroup(&updated); err != nil {
		return false, err
	}
	return true, nil
}

// detachFromActiveGroup removes profileID from the active group's ChildrenIDs
// (does NOT delete the profile). No-op when no group is active.
func (d *loadedData) detachFromActiveGroup(profileID string) (bool, error) {
	g := d.activeGroup()
	if g == nil {
		return false, nil
	}
	out := make([]string, 0, len(g.ChildrenIDs))
	found := false
	for _, id := range g.ChildrenIDs {
		if id == profileID {
			found = true
			continue
		}
		out = append(out, id)
	}
	if !found {
		return false, nil
	}
	updated := *g
	updated.ChildrenIDs = out
	if err := store.SaveGroup(&updated); err != nil {
		return false, err
	}
	return true, nil
}

func loadAll() (*loadedData, error) {
	ps, err := store.LoadProfiles()
	if err != nil {
		return nil, err
	}
	gs, err := store.ListGroups()
	if err != nil {
		return nil, err
	}
	settings, err := store.LoadSettings()
	if err != nil {
		return nil, err
	}
	routes, err := store.LoadRoutes()
	if err != nil {
		return nil, err
	}
	return &loadedData{
		profiles: ps,
		groups:   gs,
		settings: settings,
		routes:   routes,
	}, nil
}
