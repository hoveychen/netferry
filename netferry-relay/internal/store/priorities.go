package store

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// LoadPriorities reads priorities.json (a global host -> int map). Used as
// fallback before per-group priorities were introduced.
func LoadPriorities() (map[string]int, error) {
	return loadStringMapInt("priorities.json", PrioritiesPath)
}

// SavePriorities writes priorities.json.
func SavePriorities(p map[string]int) error {
	path, err := PrioritiesPath()
	if err != nil {
		return err
	}
	return writeJSONAtomic(path, p)
}

// LoadRoutes reads routes.json (host -> "tunnel"|"direct"|"blocked"). Used by
// the desktop's pre-group flow; per-group routes live in Group.Rules.
func LoadRoutes() (map[string]string, error) {
	return loadStringMapStr("routes.json", RoutesPath)
}

// SaveRoutes writes routes.json.
func SaveRoutes(r map[string]string) error {
	path, err := RoutesPath()
	if err != nil {
		return err
	}
	return writeJSONAtomic(path, r)
}

func loadStringMapInt(label string, pathFn func() (string, error)) (map[string]int, error) {
	path, err := pathFn()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]int{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]int{}, nil
	}
	var m map[string]int
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", label, err)
	}
	if m == nil {
		m = map[string]int{}
	}
	return m, nil
}

func loadStringMapStr(label string, pathFn func() (string, error)) (map[string]string, error) {
	path, err := pathFn()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", label, err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}
