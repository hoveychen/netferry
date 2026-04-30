package store

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/profile"
)

// LoadProfiles reads profiles.json into a slice. Missing/empty file → empty
// slice (matches the desktop's first-run behavior).
func LoadProfiles() ([]profile.Profile, error) {
	path, err := ProfilesPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read profiles.json: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var ps []profile.Profile
	if err := json.Unmarshal(raw, &ps); err != nil {
		return nil, fmt.Errorf("parse profiles.json: %w", err)
	}
	return ps, nil
}

// SaveProfiles writes profiles.json (pretty-printed, two-space indent — same
// as serde_json::to_string_pretty).
func SaveProfiles(ps []profile.Profile) error {
	path, err := ProfilesPath()
	if err != nil {
		return err
	}
	return writeJSONAtomic(path, ps)
}

// UpsertProfile inserts or replaces a profile by id, returning the new full
// list. The list order is preserved on update; new profiles are appended.
func UpsertProfile(p profile.Profile) ([]profile.Profile, error) {
	ps, err := LoadProfiles()
	if err != nil {
		return nil, err
	}
	for i := range ps {
		if ps[i].ID == p.ID {
			ps[i] = p
			if err := SaveProfiles(ps); err != nil {
				return nil, err
			}
			return ps, nil
		}
	}
	ps = append(ps, p)
	if err := SaveProfiles(ps); err != nil {
		return nil, err
	}
	return ps, nil
}

// RemoveProfile deletes the profile with the given id and returns the
// remaining list. No-op if id is not present.
func RemoveProfile(id string) ([]profile.Profile, error) {
	ps, err := LoadProfiles()
	if err != nil {
		return nil, err
	}
	out := ps[:0]
	for _, p := range ps {
		if p.ID != id {
			out = append(out, p)
		}
	}
	if err := SaveProfiles(out); err != nil {
		return nil, err
	}
	return out, nil
}

// FindProfile returns the profile with the given id, or nil if not found.
func FindProfile(ps []profile.Profile, id string) *profile.Profile {
	for i := range ps {
		if ps[i].ID == id {
			return &ps[i]
		}
	}
	return nil
}
