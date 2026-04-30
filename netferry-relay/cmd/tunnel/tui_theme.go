package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Modern terminal palette. Sticking to 256-color codes so the theme renders
// the same on plain xterm-256color, iTerm2, Windows Terminal and Alacritty.
const (
	colorTextFg = "230"
	colorMuted  = "244"
	colorDim    = "240"
	colorOK     = "42"
	colorWarn   = "214"
	colorErr    = "196"
	colorAccent = "208" // amber, used for keyboard hint keys
)

// tabTheme returns the accent color used by a tab's title, panel border, and
// active chip. Picked so adjacent tabs are clearly distinguishable.
func tabTheme(t tabIndex) lipgloss.Color {
	switch t {
	case tabProfiles:
		return lipgloss.Color("63") // indigo
	case tabGroups:
		return lipgloss.Color("33") // bright blue
	case tabConnection:
		return lipgloss.Color("42") // green
	case tabDestinations:
		return lipgloss.Color("208") // amber
	case tabDiagnostics:
		return lipgloss.Color("171") // pink
	case tabSettings:
		return lipgloss.Color("141") // violet
	}
	return lipgloss.Color("63")
}

// asciiLogoFull is the ANSI-Shadow rendering of "NETFERRY". Used at the top
// of the screen when the terminal is wide enough.
const asciiLogoFull = ` ███╗   ██╗███████╗████████╗███████╗███████╗██████╗ ██████╗ ██╗   ██╗
 ████╗  ██║██╔════╝╚══██╔══╝██╔════╝██╔════╝██╔══██╗██╔══██╗╚██╗ ██╔╝
 ██╔██╗ ██║█████╗     ██║   █████╗  █████╗  ██████╔╝██████╔╝ ╚████╔╝
 ██║╚██╗██║██╔══╝     ██║   ██╔══╝  ██╔══╝  ██╔══██╗██╔══██╗  ╚██╔╝
 ██║ ╚████║███████╗   ██║   ██║     ███████╗██║  ██║██║  ██║   ██║
 ╚═╝  ╚═══╝╚══════╝   ╚═╝   ╚═╝     ╚══════╝╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝   `

// renderHeader returns the top banner: full ASCII logo on tall+wide terminals,
// a single-line wordmark otherwise. Returns "" when there's no room.
// Returned string never has a trailing newline.
func renderHeader(width, height int, subtitle string) string {
	if width < 60 || height < 18 {
		return ""
	}
	if width >= 72 && height >= 28 {
		return renderFullLogo(subtitle)
	}
	return renderCompactWordmark(subtitle)
}

func renderFullLogo(subtitle string) string {
	// Indigo → cyan gradient, one color per row, gives a "tunnel glow" feel.
	palette := []string{"63", "63", "39", "39", "45", "45"}
	lines := strings.Split(asciiLogoFull, "\n")
	out := make([]string, 0, len(lines)+1)
	for i, line := range lines {
		c := palette[i%len(palette)]
		out = append(out, lipgloss.NewStyle().
			Foreground(lipgloss.Color(c)).
			Bold(true).
			Render(line))
	}
	if subtitle != "" {
		out = append(out, lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Render(" "+subtitle))
	}
	return strings.Join(out, "\n")
}

func renderCompactWordmark(subtitle string) string {
	mark := lipgloss.NewStyle().
		Foreground(lipgloss.Color("63")).
		Bold(true).
		Render("▮▮▮ NetFerry")
	sub := ""
	if subtitle != "" {
		sub = "  " + lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Render(subtitle)
	}
	return " " + mark + sub
}

// renderTabBar renders the tab chips with active-tab highlight tinted by the
// tab's own theme color.
func renderTabBar(active tabIndex) string {
	chips := make([]string, 0, int(tabCount))
	for i := tabIndex(0); i < tabCount; i++ {
		num := lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAccent)).
			Render(fmt.Sprintf("%d", i+1))
		body := fmt.Sprintf(" %s %s ", num, i.title())
		var s lipgloss.Style
		if i == active {
			s = lipgloss.NewStyle().
				Background(tabTheme(i)).
				Foreground(lipgloss.Color(colorTextFg)).
				Bold(true)
			body = fmt.Sprintf(" %d %s ", i+1, i.title())
		} else {
			s = lipgloss.NewStyle().
				Foreground(lipgloss.Color(colorMuted))
		}
		chips = append(chips, s.Render(body))
	}
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color(colorDim)).Render(" ")
	return " " + strings.Join(chips, sep)
}

// panelBorder returns a rounded-border style coloured with the tab's accent.
// Body content rendered inside is padded by one column on each side.
func panelBorder(t tabIndex) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(tabTheme(t)).
		Padding(0, 1)
}

// pageTitle renders a page title prefixed by a coloured diamond bullet.
// Used inside each tab body.
func pageTitle(t tabIndex, title string) string {
	bullet := lipgloss.NewStyle().
		Foreground(tabTheme(t)).
		Bold(true).
		Render("◆")
	titleS := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(colorTextFg)).
		Render(title)
	return bullet + " " + titleS
}

// kbdHint formats a single "key action" pair: the key in amber, the action
// label in muted gray.
func kbdHint(key, action string) string {
	keyS := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorAccent)).
		Bold(true).
		Render(key)
	actS := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorMuted)).
		Render(action)
	return keyS + " " + actS
}

// kbdHints joins (key, action) pairs with a centred dot separator. Pass an
// even number of strings — each pair becomes one chip.
func kbdHints(pairs ...string) string {
	if len(pairs) == 0 {
		return ""
	}
	if len(pairs)%2 != 0 {
		return strings.Join(pairs, " ")
	}
	chips := make([]string, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		chips = append(chips, kbdHint(pairs[i], pairs[i+1]))
	}
	sep := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorDim)).
		Render(" · ")
	return strings.Join(chips, sep)
}

// statusPill renders a coloured "● TEXT" pill for connection / state badges.
// kind: "ok" | "warn" | "err" | "muted".
func statusPill(text, kind string) string {
	var c string
	switch kind {
	case "ok":
		c = colorOK
	case "warn":
		c = colorWarn
	case "err":
		c = colorErr
	default:
		c = colorMuted
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(c)).
		Foreground(lipgloss.Color("16")).
		Bold(true).
		Padding(0, 1).
		Render("● " + text)
}

// padLines extends s to exactly n lines by appending blank lines, or clips it
// down to n. Used so the body panel always renders at the same height
// regardless of which tab is active.
func padLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		return strings.Join(lines[:n], "\n")
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
