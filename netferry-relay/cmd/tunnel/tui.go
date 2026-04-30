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
	case spinnerTickMsg, connectionStartedMsg, connectionFailedMsg, connectionEndedMsg:
		// Engine lifecycle + spinner animation must reach the Connection tab
		// regardless of which tab is currently focused, otherwise switching
		// away during a connect would drop the success/failure transition and
		// freeze the spinner.
		cmd := m.connection.update(msg)
		return m, cmd
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

var (
	flashOKStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorOK)).Bold(true)
	flashErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorErr)).Bold(true)
)

func (m *rootModel) View() string {
	if m.quit {
		return ""
	}
	if m.width == 0 {
		return "starting…"
	}
	var b strings.Builder

	// 1. Header banner: full ASCII logo on tall+wide terminals, compact
	//    wordmark otherwise. May be empty on tiny terminals.
	subtitle := "relay tunnel · v" + Version
	if m.state.connected() {
		subtitle += "  ·  ● tunnel up"
	}
	header := renderHeader(m.width, m.height, subtitle)
	headerLines := 0
	if header != "" {
		b.WriteString(header)
		b.WriteByte('\n')
		headerLines = strings.Count(header, "\n") + 1
	}

	// 2. Tab bar.
	b.WriteString(renderTabBar(m.tab))
	b.WriteByte('\n')

	// 3. Body panel: rounded border tinted by active tab's accent color.
	//    Fixed height so flash + footer always sit at the same row.
	footerStr := renderFooter()
	// Reserved rows: header + tabBar (1) + panel border (2) + flash (1) + footer (1).
	reserved := headerLines + 1 + 2 + 1 + 1
	innerHeight := m.height - reserved
	if innerHeight < 6 {
		innerHeight = 6
	}
	innerWidth := m.width - 4 // border (2) + horizontal padding (2)
	if innerWidth < 20 {
		innerWidth = 20
	}

	var body string
	switch m.tab {
	case tabProfiles:
		body = m.profiles.view(innerWidth, innerHeight)
	case tabGroups:
		body = m.groups.view(innerWidth, innerHeight)
	case tabConnection:
		body = m.connection.view(innerWidth, innerHeight)
	case tabDestinations:
		body = m.destination.view(innerWidth, innerHeight)
	case tabDiagnostics:
		body = m.diagnostics.view(innerWidth, innerHeight)
	case tabSettings:
		body = m.settings.view(innerWidth, innerHeight)
	}
	body = padLines(body, innerHeight)
	panel := panelBorder(m.tab).Width(m.width - 2).Render(body)
	b.WriteString(panel)
	b.WriteByte('\n')

	// 4. Flash status (always one line, blank when no flash to keep layout stable).
	if m.flash != "" {
		if m.flashOK {
			b.WriteString(flashOKStyle.Render(" ✓ " + m.flash))
		} else {
			b.WriteString(flashErrStyle.Render(" ✗ " + m.flash))
		}
	}
	b.WriteByte('\n')

	// 5. Footer keyboard hints.
	b.WriteString(footerStr)
	return b.String()
}

func renderFooter() string {
	return " " + kbdHints(
		"Tab", "next",
		"1-6", "jump",
		"Ctrl+C", "quit",
	)
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
