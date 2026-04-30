package store

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// GlobalSettings mirrors models.rs::GlobalSettings.
//
// Defaults applied via DefaultGlobalSettings():
//   - TrayDisplayMode: "speed"
//
// AutoConnectProfileID and ActiveGroupID stay empty when missing.
type GlobalSettings struct {
	AutoConnectProfileID string `json:"autoConnectProfileId,omitempty"`
	TrayDisplayMode      string `json:"trayDisplayMode,omitempty"`
	ActiveGroupID        string `json:"activeGroupId,omitempty"`
}

// DefaultGlobalSettings returns the desktop default-on-first-launch values.
func DefaultGlobalSettings() GlobalSettings {
	return GlobalSettings{TrayDisplayMode: "speed"}
}

// LoadSettings reads settings.json, returning defaults if missing/empty.
func LoadSettings() (GlobalSettings, error) {
	path, err := SettingsPath()
	if err != nil {
		return GlobalSettings{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultGlobalSettings(), nil
		}
		return GlobalSettings{}, fmt.Errorf("read settings.json: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return DefaultGlobalSettings(), nil
	}
	var s GlobalSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		return GlobalSettings{}, fmt.Errorf("parse settings.json: %w", err)
	}
	if s.TrayDisplayMode == "" {
		s.TrayDisplayMode = "speed"
	}
	return s, nil
}

// SaveSettings writes settings.json.
func SaveSettings(s GlobalSettings) error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}
	return writeJSONAtomic(path, s)
}
