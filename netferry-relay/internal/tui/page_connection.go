package tui

import tea "github.com/charmbracelet/bubbletea"

type connectionPage struct{ app *App }

func newConnectionPage(a *App) *connectionPage { return &connectionPage{app: a} }

func (p *connectionPage) title() string                 { return "Connection" }
func (p *connectionPage) update(msg tea.Msg) tea.Cmd     { return nil }
func (p *connectionPage) view(width, height int) string { return "" }
func (p *connectionPage) hints() string                 { return "" }
func (p *connectionPage) capturing() bool               { return false }
