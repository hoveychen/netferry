package tui

import tea "github.com/charmbracelet/bubbletea"

type destinationsPage struct{ app *App }

func newDestinationsPage(a *App) *destinationsPage { return &destinationsPage{app: a} }

func (p *destinationsPage) title() string                 { return "Destinations" }
func (p *destinationsPage) update(msg tea.Msg) tea.Cmd     { return nil }
func (p *destinationsPage) view(width, height int) string { return "" }
func (p *destinationsPage) hints() string                 { return "" }
func (p *destinationsPage) capturing() bool               { return false }
