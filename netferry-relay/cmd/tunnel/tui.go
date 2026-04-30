package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// tabIndex enumerates the top-level pages.
type tabIndex int

const (
	tabProfiles tabIndex = iota
	tabGroups
	tabConnection
	tabDestinations
	tabDiagnostics
	tabSettings
	tabCount
)

func (t tabIndex) title() string {
	switch t {
	case tabProfiles:
		return "Profiles"
	case tabGroups:
		return "Groups"
	case tabConnection:
		return "Connection"
	case tabDestinations:
		return "Destinations"
	case tabDiagnostics:
		return "Diagnostics"
	case tabSettings:
		return "Settings"
	}
	return ""
}

// flash is a transient status line shown above the footer (errors, save
// confirmations). Cleared by flashClearMsg after ~3s.
type flashClearMsg struct{ id int }

type tickMsg time.Time

// rootModel is the Bubble Tea Model for the whole TUI.
type rootModel struct {
	tab     tabIndex
	width   int
	height  int
	flashID int
	flash   string
	flashOK bool

	data  *loadedData
	state *tunnelState
	ring  *logRing

	profiles    profilesModel
	groups      groupsModel
	connection  connectionModel
	destination destinationsModel
	diagnostics diagnosticsModel
	settings    settingsModel

	verbose bool
	quit    bool
}

func newRootModel(data *loadedData, state *tunnelState, ring *logRing, verbose bool) *rootModel {
	m := &rootModel{
		data:    data,
		state:   state,
		ring:    ring,
		verbose: verbose,
	}
	m.profiles = newProfilesModel(m)
	m.groups = newGroupsModel(m)
	m.connection = newConnectionModel(m)
	m.destination = newDestinationsModel(m)
	m.diagnostics = newDiagnosticsModel(m)
	m.settings = newSettingsModel(m)
	return m
}

func (m *rootModel) Init() tea.Cmd {
	return tea.Batch(
		m.profiles.Init(),
		m.groups.Init(),
		m.connection.Init(),
		m.destination.Init(),
		m.diagnostics.Init(),
		m.settings.Init(),
		tickEvery(500*time.Millisecond),
	)
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// reload re-reads profiles/groups/settings/routes from disk. Called after any
// page mutates the store so all tabs see fresh data.
func (m *rootModel) reload() {
	d, err := loadAll()
	if err != nil {
		m.setFlash(false, "reload: "+err.Error())
		return
	}
	m.data = d
}

func (m *rootModel) setFlash(ok bool, msg string) tea.Cmd {
	m.flashID++
	m.flash = msg
	m.flashOK = ok
	id := m.flashID
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return flashClearMsg{id: id} })
}

func (m *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			m.quit = true
			return m, tea.Quit
		case "tab":
			m.tab = (m.tab + 1) % tabCount
			return m, nil
		case "shift+tab":
			m.tab = (m.tab + tabCount - 1) % tabCount
			return m, nil
		case "1", "2", "3", "4", "5", "6":
			i := int(msg.String()[0] - '1')
			if i >= 0 && i < int(tabCount) {
				m.tab = tabIndex(i)
			}
			return m, nil
		}
	case flashClearMsg:
		if msg.id == m.flashID {
			m.flash = ""
		}
		return m, nil
	case tickMsg:
		// Forward to tabs that need periodic refresh; chain another tick.
		var cmds []tea.Cmd
		c := m.connection.tick(time.Time(msg))
		if c != nil {
			cmds = append(cmds, c)
		}
		cmds = append(cmds, tickEvery(500*time.Millisecond))
		return m, tea.Batch(cmds...)
	}

	// Route to active tab's Update.
	var cmd tea.Cmd
	switch m.tab {
	case tabProfiles:
		cmd = m.profiles.update(msg)
	case tabGroups:
		cmd = m.groups.update(msg)
	case tabConnection:
		cmd = m.connection.update(msg)
	case tabDestinations:
		cmd = m.destination.update(msg)
	case tabDiagnostics:
		cmd = m.diagnostics.update(msg)
	case tabSettings:
		cmd = m.settings.update(msg)
	}
	return m, cmd
}

// View styles
var (
	tabActive = lipgloss.NewStyle().
			Background(lipgloss.Color("63")).
			Foreground(lipgloss.Color("230")).
			Bold(true).
			Padding(0, 2)
	tabInactive = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Padding(0, 2)
	headerLine = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Render(strings.Repeat("─", 200))
	flashOKStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	flashErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	footer        = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Render("[Tab] next  [1-6] jump  [?] tab help  [Ctrl+C] quit")
)

func (m *rootModel) View() string {
	if m.quit {
		return ""
	}
	if m.width == 0 {
		return "starting…"
	}
	var b strings.Builder

	// Header tabs
	chips := make([]string, 0, int(tabCount))
	for i := tabIndex(0); i < tabCount; i++ {
		t := fmt.Sprintf("%d %s", i+1, i.title())
		if i == m.tab {
			chips = append(chips, tabActive.Render(t))
		} else {
			chips = append(chips, tabInactive.Render(t))
		}
	}
	b.WriteString(strings.Join(chips, " "))
	b.WriteByte('\n')
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).
		Render(strings.Repeat("─", max(m.width, 1))))
	b.WriteByte('\n')

	bodyHeight := m.height - 5 // header (2) + flash (1) + footer (1) + buffer
	if bodyHeight < 5 {
		bodyHeight = 5
	}

	var body string
	switch m.tab {
	case tabProfiles:
		body = m.profiles.view(m.width, bodyHeight)
	case tabGroups:
		body = m.groups.view(m.width, bodyHeight)
	case tabConnection:
		body = m.connection.view(m.width, bodyHeight)
	case tabDestinations:
		body = m.destination.view(m.width, bodyHeight)
	case tabDiagnostics:
		body = m.diagnostics.view(m.width, bodyHeight)
	case tabSettings:
		body = m.settings.view(m.width, bodyHeight)
	}
	// Hard clip: no tab is allowed to push the header off-screen.
	b.WriteString(clipLines(body, bodyHeight))

	b.WriteByte('\n')
	if m.flash != "" {
		if m.flashOK {
			b.WriteString(flashOKStyle.Render("✓ " + m.flash))
		} else {
			b.WriteString(flashErrStyle.Render("✗ " + m.flash))
		}
		b.WriteByte('\n')
	}
	b.WriteString(footer)
	return b.String()
}

// clipLines truncates s to at most n lines, preserving the leading content.
// Drops the trailing newline if it survives the truncation.
func clipLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

// windowedRange returns the [start, end) row range to show given total rows,
// the cursor's index, and the available viewport height. Keeps the cursor in
// view by sliding the window when the cursor approaches an edge.
func windowedRange(total, cursor, height int) (start, end int) {
	if height <= 0 {
		return 0, 0
	}
	if total <= height {
		return 0, total
	}
	start = cursor - height/2
	if start < 0 {
		start = 0
	}
	end = start + height
	if end > total {
		end = total
		start = end - height
	}
	return start, end
}

// runTUI is invoked from main.go when the --tui flag is given.
func runTUI(verbose bool) error {
	data, err := loadAll()
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}

	// Redirect log output to the ring buffer so the Connection tab can show it.
	ring := newLogRing(64 * 1024)
	log.SetOutput(io.MultiWriter(ring, os.Stderr))
	// Engine writes a couple of lines directly to os.Stderr (status banner,
	// stats-port). Tee that into the ring too.
	stderrTee, err := teeStderr(ring)
	if err != nil {
		return fmt.Errorf("tee stderr: %w", err)
	}
	defer stderrTee.Close()

	state := &tunnelState{}
	root := newRootModel(data, state, ring, verbose)

	prog := tea.NewProgram(root, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		return err
	}
	// Graceful tunnel teardown if still running.
	if state.connected() {
		state.requestStop()
		_, _, _, _ = state.snapshot()
	}
	return nil
}
