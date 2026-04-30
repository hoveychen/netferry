package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hoveychen/netferry/relay/internal/store"
)

// settingsField indexes the editable settings fields.
type settingsField int

const (
	sfAutoConnect settingsField = iota
	sfActiveGroup
	sfTrayDisplay
	sfFieldCount
)

func (f settingsField) label() string {
	return [...]string{
		"Auto-connect profile",
		"Active group",
		"Tray display mode",
	}[f]
}

type settingsModel struct {
	root   *rootModel
	cursor int
}

func newSettingsModel(root *rootModel) settingsModel {
	return settingsModel{root: root}
}

func (s settingsModel) Init() tea.Cmd { return nil }

func (s *settingsModel) update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor+1 < int(sfFieldCount) {
			s.cursor++
		}
	case "right", "l", "enter", " ":
		return s.advance(+1)
	case "left", "h":
		return s.advance(-1)
	case "x":
		// Clear the current field.
		return s.clearField()
	}
	return nil
}

func (s *settingsModel) advance(direction int) tea.Cmd {
	current := s.root.data.settings
	switch settingsField(s.cursor) {
	case sfAutoConnect:
		ids := []string{""}
		for _, p := range s.root.data.profiles {
			ids = append(ids, p.ID)
		}
		current.AutoConnectProfileID = pickNext(ids, current.AutoConnectProfileID, direction)
	case sfActiveGroup:
		ids := []string{""}
		for _, g := range s.root.data.groups {
			ids = append(ids, g.ID)
		}
		current.ActiveGroupID = pickNext(ids, current.ActiveGroupID, direction)
	case sfTrayDisplay:
		opts := []string{"speed", "rtt", "active", "off"}
		current.TrayDisplayMode = pickNext(opts, current.TrayDisplayMode, direction)
	}
	if err := store.SaveSettings(current); err != nil {
		return s.root.setFlash(false, "save: "+err.Error())
	}
	s.root.data.settings = current
	return s.root.setFlash(true, "settings saved")
}

func (s *settingsModel) clearField() tea.Cmd {
	current := s.root.data.settings
	switch settingsField(s.cursor) {
	case sfAutoConnect:
		current.AutoConnectProfileID = ""
	case sfActiveGroup:
		current.ActiveGroupID = ""
	case sfTrayDisplay:
		current.TrayDisplayMode = "speed"
	}
	if err := store.SaveSettings(current); err != nil {
		return s.root.setFlash(false, "save: "+err.Error())
	}
	s.root.data.settings = current
	return s.root.setFlash(true, "cleared")
}

func pickNext(opts []string, cur string, dir int) string {
	idx := 0
	for i, v := range opts {
		if v == cur {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(opts)) % len(opts)
	return opts[idx]
}

// ── view ─────────────────────────────────────────────────────────────────────

func (s *settingsModel) view(width, height int) string {
	settings := s.root.data.settings
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Settings"))
	b.WriteByte('\n')
	b.WriteByte('\n')

	auto := "(none)"
	if settings.AutoConnectProfileID != "" {
		if p := findProfileByID(s.root.data.profiles, settings.AutoConnectProfileID); p != nil {
			auto = p.Name
		} else {
			auto = "<missing: " + settings.AutoConnectProfileID + ">"
		}
	}
	active := "(none)"
	if settings.ActiveGroupID != "" {
		if g := findGroupByID(s.root.data.groups, settings.ActiveGroupID); g != nil {
			active = g.Name
		} else {
			active = "<missing: " + settings.ActiveGroupID + ">"
		}
	}
	tray := settings.TrayDisplayMode
	if tray == "" {
		tray = "speed"
	}

	rows := []string{
		fmt.Sprintf("%-25s  %s", sfAutoConnect.label()+":", auto),
		fmt.Sprintf("%-25s  %s", sfActiveGroup.label()+":", active),
		fmt.Sprintf("%-25s  %s", sfTrayDisplay.label()+":", tray),
	}
	for i, row := range rows {
		if i == s.cursor {
			b.WriteString("▶ " + listSelected.Render(row))
		} else {
			b.WriteString("  " + row)
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(dimText.Render("[↑/↓] move  [←/→] cycle value  [x] clear  [Enter] next"))
	return b.String()
}
