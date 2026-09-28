package tui

import tea "github.com/charmbracelet/bubbletea"

type settingsPage struct{ app *App }

func newSettingsPage(a *App) *settingsPage { return &settingsPage{app: a} }

func (p *settingsPage) title() string                 { return "Settings" }
func (p *settingsPage) update(msg tea.Msg) tea.Cmd    { return nil }
func (p *settingsPage) view(width, height int) string { return "" }
func (p *settingsPage) hints() string                 { return "" }
func (p *settingsPage) capturing() bool               { return false }
