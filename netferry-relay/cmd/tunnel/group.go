package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// GroupFile is the on-disk shape of the plaintext JSON group file passed via
// --group. The desktop sidecar writes it to a 0600 temp file, spawns the
// tunnel, then unlinks it. Format intentionally mirrors the desktop
// ProfileGroup so the same children can be reused verbatim.
type GroupFile struct {
	ID               string            `json:"id"`
	Name             string            `json:"name,omitempty"`
	DefaultProfileID string            `json:"defaultProfileId"`
	Children         []profile.Profile `json:"children"`
}

// loadGroupFile reads and parses a plaintext JSON group file. It also
// validates that DefaultProfileID resolves to one of the children (falling
// back to children[0] when the field is empty).
func loadGroupFile(path string) (*GroupFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read group file: %w", err)
	}
	var g GroupFile
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("parse group JSON: %w", err)
	}
	if len(g.Children) == 0 {
		return nil, fmt.Errorf("group %q has no children", g.ID)
	}
	if g.DefaultProfileID == "" {
		g.DefaultProfileID = g.Children[0].ID
	}
	found := false
	for _, c := range g.Children {
		if c.ID == g.DefaultProfileID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("group %q default %q not in children", g.ID, g.DefaultProfileID)
	}
	return &g, nil
}

// buildActiveGroupFromFile projects a GroupFile into the stats.ActiveGroup
// shape pushed via POST /group. The relay stores this snapshot so connection
// events can be tagged with the owning profile id.
func buildActiveGroupFromFile(g *GroupFile) *stats.ActiveGroup {
	ids := make([]string, 0, len(g.Children))
	for _, c := range g.Children {
		ids = append(ids, c.ID)
	}
	return &stats.ActiveGroup{
		ID:               g.ID,
		Name:             g.Name,
		DefaultProfileID: g.DefaultProfileID,
		ProfileIDs:       ids,
	}
}
