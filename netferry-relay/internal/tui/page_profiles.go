package tui

import tea "github.com/charmbracelet/bubbletea"

type profilesPage struct{ app *App }

func newProfilesPage(a *App) *profilesPage { return &profilesPage{app: a} }

func (p *profilesPage) title() string                 { return "Profiles" }
func (p *profilesPage) update(msg tea.Msg) tea.Cmd     { return nil }
func (p *profilesPage) view(width, height int) string { return "" }
func (p *profilesPage) hints() string                 { return "" }
func (p *profilesPage) capturing() bool               { return false }
