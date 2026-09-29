package store

import (
	"crypto/rand"
	"fmt"
	"os"
)

// DefaultGroupID / DefaultGroupName mirror migrate_v2.rs.
const (
	DefaultGroupID   = "default"
	DefaultGroupName = "Default"
)

// MigrateV2 is a port of the desktop's migrate_v2::run, which runs on every
// launch: when groups/default.json does not exist it is created from the
// legacy flat files (all profiles as children, routes.json translated, global
// priorities copied), and settings.activeGroupId is pointed at it if unset.
// Idempotent. Routing rules end up in rules.json via LoadRules, which must
// run after this. On a machine that never ran the desktop app this is what gives
// the TUI its first group.
func MigrateV2() error {
	path, err := groupPath(DefaultGroupID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}

	profiles, _ := LoadProfiles()
	legacyRoutes, _ := LoadRoutes()
	legacyPrios, _ := LoadPriorities()
	settings, err := LoadSettings()
	if err != nil {
		settings = DefaultGlobalSettings()
	}

	rules := make(map[string]RouteMode, len(legacyRoutes))
	for host, mode := range legacyRoutes {
		rules[host] = RouteMode{Kind: NormalizeRouteKind(mode)}
	}
	children := make([]string, 0, len(profiles))
	for _, p := range profiles {
		children = append(children, p.ID)
	}
	// Written directly (not via SaveGroup, which would first run the
	// rules.json migration and strip the rules). When rules.json does not
	// exist yet the legacy routes ride on the group file in the pre-rules.json
	// shape, so the rules.json migration (LoadRules) picks them up together
	// with any other group's rules.
	g := legacyDefaultGroup{
		ID:          DefaultGroupID,
		Name:        DefaultGroupName,
		ChildrenIDs: children,
		Priorities:  legacyPrios,
	}
	if rp, err := RulesPath(); err != nil {
		return err
	} else if _, err := os.Stat(rp); os.IsNotExist(err) {
		g.Rules = rules
		g.FinalRoute = &RouteMode{Kind: RouteTunnel}
	}
	if err := writeJSONAtomic(path, g); err != nil {
		return err
	}
	if settings.ActiveGroupID == "" {
		settings.ActiveGroupID = DefaultGroupID
		return SaveSettings(settings)
	}
	return nil
}

// legacyDefaultGroup is the group file MigrateV2 writes: the slim group plus,
// when rules.json has not been created yet, the pre-rules.json rule fields.
type legacyDefaultGroup struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	ChildrenIDs []string             `json:"childrenIds"`
	Priorities  map[string]int       `json:"priorities"`
	Rules       map[string]RouteMode `json:"rules,omitempty"`
	FinalRoute  *RouteMode           `json:"finalRoute,omitempty"`
}

// NewID returns a random RFC 4122 v4 UUID (crypto.randomUUID()).
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
