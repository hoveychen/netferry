package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// menu is a titled pick list (dropdowns, action menus, pickers).
type menu struct {
	title string
	items []menuItem
	sel   int
}

type menuItem struct {
	label  string
	detail string
	run    func() tea.Cmd
}

// update handles a key; done reports the menu should close.
func (m *menu) update(km tea.KeyMsg) (cmd tea.Cmd, done bool) {
	switch km.String() {
	case "up", "k":
		if m.sel > 0 {
			m.sel--
		}
	case "down", "j":
		if m.sel < len(m.items)-1 {
			m.sel++
		}
	case "home":
		m.sel = 0
	case "end":
		m.sel = len(m.items) - 1
	case "enter":
		if m.sel >= 0 && m.sel < len(m.items) && m.items[m.sel].run != nil {
			return m.items[m.sel].run(), true
		}
		return nil, true
	case "esc", "q":
		return nil, true
	}
	return nil, false
}

func (m *menu) view(width, height int) string {
	lines := []string{sTitle.Render(m.title), ""}
	if len(m.items) == 0 {
		lines = append(lines, sMuted.Render("(nothing to pick)"))
	}
	var rows []string
	for i, it := range m.items {
		label := it.label
		if it.detail != "" {
			label += "  " + sMuted.Render(it.detail)
		}
		if i == m.sel {
			rows = append(rows, sAccent.Render("▸ ")+sBold.Render(truncate(label, width-2)))
		} else {
			rows = append(rows, "  "+truncate(label, width-2))
		}
	}
	lines = append(lines, windowAround(rows, m.sel, height-len(lines)-2)...)
	lines = append(lines, "", hints("↑/↓", "move", "enter", "choose", "esc", "cancel"))
	return strings.Join(lines, "\n")
}

// windowAround returns at most h rows keeping row sel visible.
func windowAround(rows []string, sel, h int) []string {
	if h < 1 {
		h = 1
	}
	if len(rows) <= h {
		return rows
	}
	top := sel - h/2
	if top < 0 {
		top = 0
	}
	if top > len(rows)-h {
		top = len(rows) - h
	}
	return rows[top : top+h]
}

// prompt is a single-line text input with a submit callback.
type prompt struct {
	title  string
	help   string
	field  *Field
	submit func(string) tea.Cmd
}

func newPrompt(title, help, value string, submit func(string) tea.Cmd) *prompt {
	f := textField("v", "", value, "")
	f.input.CharLimit = 0 // exported profiles run to several KB
	f.input.Focus()
	f.input.CursorEnd()
	return &prompt{title: title, help: help, field: f, submit: submit}
}

func (p *prompt) update(msg tea.Msg) (cmd tea.Cmd, done bool) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "enter":
			return p.submit(p.field.Text()), true
		case "esc":
			return nil, true
		}
	}
	p.field.input, cmd = p.field.input.Update(msg)
	return cmd, false
}

func (p *prompt) view(width int) string {
	p.field.input.Width = width - 4
	out := sTitle.Render(p.title) + "\n\n"
	if p.help != "" {
		out += sMuted.Render(p.help) + "\n\n"
	}
	return out + sAccent.Render("› ") + p.field.input.View() + "\n\n" + hints("enter", "ok", "esc", "cancel")
}
