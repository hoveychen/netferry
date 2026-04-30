// Package store reads and writes the desktop app's persisted state
// (profiles.json, groups/*.json, settings.json, priorities.json, routes.json)
// so the Go-side TUI shares one source of truth with the Tauri UI.
//
// The on-disk layout mirrors what Tauri 2's app_data_dir() resolves to with
// identifier "com.hoveychen.netferry".
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Identifier is the Tauri bundle identifier; do not change without also
// migrating the desktop app.
const Identifier = "com.hoveychen.netferry"

// DataDir returns the directory that mirrors Tauri 2's app_data_dir() for the
// NetFerry desktop bundle. The directory is created if missing.
func DataDir() (string, error) {
	dir, err := dataDirPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create data dir %s: %w", dir, err)
	}
	return dir, nil
}

func dataDirPath() (string, error) {
	if env := os.Getenv("NETFERRY_DATA_DIR"); env != "" {
		return env, nil
	}
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", Identifier), nil
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			return "", errors.New("APPDATA not set")
		}
		return filepath.Join(appdata, Identifier), nil
	default:
		// Linux / *BSD: Tauri 2 uses XDG_DATA_HOME (default ~/.local/share).
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, Identifier), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", Identifier), nil
	}
}

// ProfilesPath returns the path to profiles.json under DataDir.
func ProfilesPath() (string, error) {
	d, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "profiles.json"), nil
}

// GroupsDir returns the directory containing per-group JSON files.
func GroupsDir() (string, error) {
	d, err := DataDir()
	if err != nil {
		return "", err
	}
	sub := filepath.Join(d, "groups")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return "", fmt.Errorf("create groups dir: %w", err)
	}
	return sub, nil
}

// SettingsPath returns the path to settings.json.
func SettingsPath() (string, error) {
	d, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "settings.json"), nil
}

// PrioritiesPath returns the path to priorities.json (global, non-group).
func PrioritiesPath() (string, error) {
	d, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "priorities.json"), nil
}

// RoutesPath returns the path to routes.json (global, non-group).
func RoutesPath() (string, error) {
	d, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "routes.json"), nil
}
