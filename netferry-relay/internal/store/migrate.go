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
// Idempotent. On a machine that never ran the desktop app this is what gives
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
	g := &Group{
		ID:          DefaultGroupID,
		Name:        DefaultGroupName,
		ChildrenIDs: children,
		Rules:       rules,
		Priorities:  legacyPrios,
		FinalRoute:  RouteMode{Kind: RouteTunnel},
	}
	if err := SaveGroup(g); err != nil {
		return err
	}
	if settings.ActiveGroupID == "" {
		settings.ActiveGroupID = DefaultGroupID
		return SaveSettings(settings)
	}
	return nil
}

// NewID returns a random RFC 4122 v4 UUID (crypto.randomUUID()).
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
