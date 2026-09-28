package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Palette: mid-tone accents that read on both dark and light terminals.
var (
	cAccent  = lipgloss.AdaptiveColor{Light: "#5b5bd6", Dark: "#8b8cf8"}
	cMuted   = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#9ca3af"}
	cDim     = lipgloss.AdaptiveColor{Light: "#d1d5db", Dark: "#4b5563"}
	cOK      = lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#4ade80"}
	cWarn    = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#fbbf24"}
	cErr     = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#f87171"}
	cInfo    = lipgloss.AdaptiveColor{Light: "#0369a1", Dark: "#38bdf8"}
	cSelFg   = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#111827"}
	cSelBg   = cAccent
	cTunnels = []lipgloss.AdaptiveColor{
		{Light: "#5b5bd6", Dark: "#8b8cf8"},
		{Light: "#0891b2", Dark: "#22d3ee"},
		{Light: "#c026d3", Dark: "#e879f9"},
		{Light: "#ea580c", Dark: "#fb923c"},
		{Light: "#16a34a", Dark: "#4ade80"},
		{Light: "#ca8a04", Dark: "#facc15"},
	}
)

var (
	sBold     = lipgloss.NewStyle().Bold(true)
	sMuted    = lipgloss.NewStyle().Foreground(cMuted)
	sDim      = lipgloss.NewStyle().Foreground(cDim)
	sAccent   = lipgloss.NewStyle().Foreground(cAccent)
	sOK       = lipgloss.NewStyle().Foreground(cOK)
	sWarn     = lipgloss.NewStyle().Foreground(cWarn)
	sErr      = lipgloss.NewStyle().Foreground(cErr)
	sInfo     = lipgloss.NewStyle().Foreground(cInfo)
	sSelected = lipgloss.NewStyle().Foreground(cSelFg).Background(cSelBg).Bold(true)
	sTitle    = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sSection  = lipgloss.NewStyle().Foreground(cMuted).Bold(true)
	sKey      = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cDim).Padding(0, 1)
	sModal    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(1, 2)
	sStrike   = lipgloss.NewStyle().Strikethrough(true).Foreground(cMuted)
)

// tunnelStyle colors the i-th group child (0 = default), like tunnelColor.ts.
func tunnelStyle(i int) lipgloss.Style {
	if i < 0 {
		i = 0
	}
	return lipgloss.NewStyle().Foreground(cTunnels[i%len(cTunnels)])
}

// pill renders a status badge.
func pill(text string, c lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Foreground(cSelFg).Background(c).Bold(true).Padding(0, 1).Render(text)
}

// hints renders "[k] label  [k] label" footers from key/label pairs.
func hints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sKey.Render(pairs[i])+" "+sMuted.Render(pairs[i+1]))
	}
	return strings.Join(parts, sDim.Render("  ·  "))
}

func fmtBytes(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.2f GB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func fmtRate(n int64) string { return fmtBytes(n) + "/s" }

// truncate cuts s (which may contain styling) to w display cells with an
// ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

func truncateANSI(s string, w int) string { return truncate(s, w) }

// padRight pads s with spaces to w display cells.
func padRight(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}
