package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/profile"
)

// RouteMode mirrors the desktop RouteMode enum-as-tagged-struct. Kind is one
// of "tunnel" | "default" | "direct" | "blocked"; ProfileID is required when
// Kind == "tunnel".
type RouteMode struct {
	Kind      string `json:"kind"`
	ProfileID string `json:"profileId,omitempty"`
}

// Group mirrors models.rs::ProfileGroup. ChildrenIDs[0] is the default
// profile when Rules contain a "default" entry without an explicit ProfileID.
//
// LegacyChildren is the pre-children-ids form: full Profile objects embedded
// in the group. We accept it on read but never write it back; NormalizeLegacy
// fills ChildrenIDs from it on the first load.
type Group struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	ChildrenIDs    []string             `json:"childrenIds,omitempty"`
	LegacyChildren []profile.Profile    `json:"children,omitempty"`
	Rules          map[string]RouteMode `json:"rules,omitempty"`
	Priorities     map[string]int       `json:"priorities,omitempty"`
	KnownHosts     []string             `json:"knownHosts,omitempty"`
}

// NormalizeLegacy fills ChildrenIDs from LegacyChildren if the group is in
// the pre-children-ids on-disk format, and clears LegacyChildren so it does
// not round-trip back to disk. Returns true if anything changed.
func (g *Group) NormalizeLegacy() bool {
	migrated := len(g.ChildrenIDs) == 0 && len(g.LegacyChildren) > 0
	if migrated {
		g.ChildrenIDs = make([]string, 0, len(g.LegacyChildren))
		for _, p := range g.LegacyChildren {
			g.ChildrenIDs = append(g.ChildrenIDs, p.ID)
		}
	}
	hadLegacy := len(g.LegacyChildren) > 0
	g.LegacyChildren = nil
	return migrated || hadLegacy
}

// groupOnDisk is the wire shape used when writing — identical to Group but
// with LegacyChildren omitted unconditionally (mirrors serde's
// skip_serializing on the `children` field).
type groupOnDisk struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	ChildrenIDs []string             `json:"childrenIds"`
	Rules       map[string]RouteMode `json:"rules"`
	Priorities  map[string]int       `json:"priorities"`
	KnownHosts  []string             `json:"knownHosts"`
}

func (g *Group) marshalForDisk() any {
	return groupOnDisk{
		ID:          g.ID,
		Name:        g.Name,
		ChildrenIDs: nilToEmpty(g.ChildrenIDs),
		Rules:       g.Rules,
		Priorities:  g.Priorities,
		KnownHosts:  nilToEmpty(g.KnownHosts),
	}
}

func nilToEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func validateGroupID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid group id: %q", id)
	}
	return nil
}

func groupPath(id string) (string, error) {
	if err := validateGroupID(id); err != nil {
		return "", err
	}
	dir, err := GroupsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
}

// ListGroups loads every <id>.json under GroupsDir, sorted by name. Legacy
// groups are normalized in-place and rewritten so subsequent reads are clean.
func ListGroups() ([]Group, error) {
	dir, err := GroupsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read groups dir: %w", err)
	}
	var out []Group
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var g Group
		if err := json.Unmarshal(raw, &g); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if g.NormalizeLegacy() {
			_ = SaveGroup(&g)
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LoadGroup loads a single group by id; returns (nil, nil) if missing.
func LoadGroup(id string) (*Group, error) {
	path, err := groupPath(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var g Group
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if g.NormalizeLegacy() {
		_ = SaveGroup(&g)
	}
	return &g, nil
}

// SaveGroup writes the given group to its <id>.json file. The legacy
// `children` field is never written back.
func SaveGroup(g *Group) error {
	path, err := groupPath(g.ID)
	if err != nil {
		return err
	}
	return writeJSONAtomic(path, g.marshalForDisk())
}

// DeleteGroup removes the <id>.json file. No-op if missing.
func DeleteGroup(id string) error {
	path, err := groupPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	return nil
}
