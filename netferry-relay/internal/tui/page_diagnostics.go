package tui

import tea "github.com/charmbracelet/bubbletea"

type diagnosticsPage struct{ app *App }

func newDiagnosticsPage(a *App) *diagnosticsPage { return &diagnosticsPage{app: a} }

func (p *diagnosticsPage) title() string                 { return "Diagnostics" }
func (p *diagnosticsPage) update(msg tea.Msg) tea.Cmd     { return nil }
func (p *diagnosticsPage) view(width, height int) string { return "" }
func (p *diagnosticsPage) hints() string                 { return "" }
func (p *diagnosticsPage) capturing() bool               { return false }
