package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hoveychen/netferry/relay/internal/stats"
)

// connectionStartedMsg is delivered when NewEngine returns successfully.
// The engine is dialing/installing firewall rules in the background — the
// caller must still wait for connectionReadyMsg before claiming CONNECTED.
type connectionStartedMsg struct {
	eng       *Engine
	profileID string
	groupID   string
	stopCh    chan struct{}
	doneCh    chan error
}

// connectionReadyMsg fires when the engine reaches its "Connected to server"
// milestone (SSH+mux up, firewall rules installed, proxy listening). Until
// this arrives the TUI keeps the CONNECTING spinner so we don't lie when pf
// setup fails midway (e.g. running without sudo on macOS).
type connectionReadyMsg struct{}

// connectionFailedMsg is delivered when NewEngine fails before Run starts.
type connectionFailedMsg struct{ err error }

// connectionEndedMsg is delivered when the engine.Run goroutine returns.
type connectionEndedMsg struct{ err error }

type connectionMode int

const (
	connBrowse connectionMode = iota
	connSelectingProfile
	connSelectingGroup
)

type connectionModel struct {
	root         *rootModel
	mode         connectionMode
	cursor       int
	profileIdx   int // selected profile in selectingProfile mode
	groupIdx     int // selected group in selectingGroup mode
	logSnapshot  []string
	displayLines int

	// Engine startup is async — connecting flips true the moment the user picks
	// a profile/group, false once NewEngine returns success or failure.
	connecting bool
	spinFrame  int

	lastBytesAt time.Time
	lastRx      int64
	lastTx      int64
	rxRate      int64
	txRate      int64
}

// spinnerTickMsg drives the connecting-state braille spinner. Self-rescheduled
// every ~100ms while connecting; quiesces once connecting flips false.
type spinnerTickMsg struct{}

// Braille spinner frames (matches the bubbles default).
var spinnerFrames = []string{
	"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏",
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return spinnerTickMsg{} })
}

func newConnectionModel(root *rootModel) connectionModel {
	return connectionModel{root: root}
}

func (c connectionModel) Init() tea.Cmd { return nil }

func (c *connectionModel) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.KeyMsg:
		switch c.mode {
		case connBrowse:
			return c.updateBrowse(m)
		case connSelectingProfile:
			return c.updatePickProfile(m)
		case connSelectingGroup:
			return c.updatePickGroup(m)
		}
	case connectionStartedMsg:
		// Engine struct exists, but firewall rules aren't installed yet —
		// keep CONNECTING until connectionReadyMsg lands so a non-root pf
		// failure doesn't briefly flash CONNECTED.
		c.root.state.setActive(m.eng, m.profileID, m.groupID, m.stopCh, m.doneCh)
		return tea.Batch(
			waitForEngineReady(m.eng),
			waitForEngineDone(m.doneCh),
		)
	case connectionReadyMsg:
		c.connecting = false
		return nil
	case connectionFailedMsg:
		c.connecting = false
		return c.root.setFlash(false, "connect: "+m.err.Error())
	case connectionEndedMsg:
		c.connecting = false
		c.root.state.clearActive(m.err)
		if m.err != nil && m.err != ErrExitForReconnect {
			return c.root.setFlash(false, "engine ended: "+m.err.Error())
		}
		return c.root.setFlash(true, "tunnel stopped")
	case spinnerTickMsg:
		// Advance the spinner frame and reschedule only while still connecting,
		// otherwise let the tick chain die so we don't burn CPU.
		if c.connecting {
			c.spinFrame++
			return spinnerTickCmd()
		}
		return nil
	}
	return nil
}

func waitForEngineDone(doneCh <-chan error) tea.Cmd {
	return func() tea.Msg {
		err := <-doneCh
		return connectionEndedMsg{err: err}
	}
}

// waitForEngineReady blocks until the engine signals readiness. If the engine
// returned early without reaching the operational milestone (e.g. firewall
// setup failed), it emits a nil message — bubbletea ignores nil and the
// parallel waitForEngineDone goroutine will deliver the actual error.
func waitForEngineReady(eng *Engine) tea.Cmd {
	return func() tea.Msg {
		<-eng.ReadyCh()
		if !eng.ReadyOK() {
			return nil
		}
		return connectionReadyMsg{}
	}
}

// tick is called by rootModel on every periodic tick. Used to update live
// byte-rate readout while connected.
func (c *connectionModel) tick(now time.Time) tea.Cmd {
	eng, _, _, _ := c.root.state.snapshot()
	if eng == nil {
		c.lastBytesAt = time.Time{}
		c.rxRate = 0
		c.txRate = 0
		return nil
	}
	rx := eng.Counters().RxTotal.Load()
	tx := eng.Counters().TxTotal.Load()
	if !c.lastBytesAt.IsZero() {
		dt := now.Sub(c.lastBytesAt).Seconds()
		if dt > 0 {
			c.rxRate = int64(float64(rx-c.lastRx) / dt)
			c.txRate = int64(float64(tx-c.lastTx) / dt)
		}
	}
	c.lastBytesAt = now
	c.lastRx = rx
	c.lastTx = tx
	return nil
}

func (c *connectionModel) updateBrowse(msg tea.KeyMsg) tea.Cmd {
	connected := c.root.state.connected()
	switch msg.String() {
	case "p":
		if !connected && len(c.root.data.profiles) > 0 {
			c.mode = connSelectingProfile
			c.profileIdx = 0
		}
	case "g":
		if !connected && len(c.root.data.groups) > 0 {
			c.mode = connSelectingGroup
			c.groupIdx = 0
		}
	case "s":
		if connected {
			c.root.state.requestStop()
		}
	}
	return nil
}

func (c *connectionModel) updatePickProfile(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		c.mode = connBrowse
	case "up", "k":
		if c.profileIdx > 0 {
			c.profileIdx--
		}
	case "down", "j":
		if c.profileIdx+1 < len(c.root.data.profiles) {
			c.profileIdx++
		}
	case "enter":
		p := c.root.data.profiles[c.profileIdx]
		c.mode = connBrowse
		return c.startProfile(p.ID)
	}
	return nil
}

func (c *connectionModel) updatePickGroup(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		c.mode = connBrowse
	case "up", "k":
		if c.groupIdx > 0 {
			c.groupIdx--
		}
	case "down", "j":
		if c.groupIdx+1 < len(c.root.data.groups) {
			c.groupIdx++
		}
	case "enter":
		g := c.root.data.groups[c.groupIdx]
		c.mode = connBrowse
		return c.startGroup(g.ID)
	}
	return nil
}

func (c *connectionModel) startProfile(profileID string) tea.Cmd {
	pr := findProfileByID(c.root.data.profiles, profileID)
	if pr == nil {
		return c.root.setFlash(false, "profile not found")
	}
	cfg, err := engineConfigFromProfile(pr, c.root.verbose)
	if err != nil {
		return c.root.setFlash(false, err.Error())
	}
	c.connecting = true
	c.spinFrame = 0
	return tea.Batch(startEngineCmd(cfg, profileID, ""), spinnerTickCmd())
}

func (c *connectionModel) startGroup(groupID string) tea.Cmd {
	gp := findGroupByID(c.root.data.groups, groupID)
	if gp == nil {
		return c.root.setFlash(false, "group not found")
	}
	cfg, err := engineConfigFromGroup(gp, c.root.data.profiles, c.root.verbose)
	if err != nil {
		return c.root.setFlash(false, err.Error())
	}
	c.connecting = true
	c.spinFrame = 0
	return tea.Batch(startEngineCmd(cfg, "", groupID), spinnerTickCmd())
}

// startEngineCmd creates the engine off the UI thread and returns the result
// as a tea.Msg. The actual Run loop is dispatched once the message arrives.
func startEngineCmd(cfg *EngineConfig, profileID, groupID string) tea.Cmd {
	return func() tea.Msg {
		eng, err := NewEngine(cfg)
		if err != nil {
			return connectionFailedMsg{err: err}
		}
		stopCh := make(chan struct{})
		doneCh := make(chan error, 1)
		go func() {
			doneCh <- eng.Run(stopCh)
			close(doneCh)
		}()
		// Use a short context so a non-blocking startup banner can render before
		// the engine fully connects (engine emits "Connected to server." once mux
		// is up).
		_ = context.Background()
		return connectionStartedMsg{
			eng:       eng,
			profileID: profileID,
			groupID:   groupID,
			stopCh:    stopCh,
			doneCh:    doneCh,
		}
	}
}

// ── view ─────────────────────────────────────────────────────────────────────

var (
	statLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Bold(true)
	statValueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorTextFg))
	statBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colorDim)).
			Padding(0, 1)
)

func (c *connectionModel) view(width, height int) string {
	switch c.mode {
	case connSelectingProfile:
		return c.viewPickProfile()
	case connSelectingGroup:
		return c.viewPickGroup()
	}

	eng, profileID, groupID, _ := c.root.state.snapshot()
	// connectedReady = engine running AND past the "Connected to server."
	// milestone. We treat the connecting window separately so a non-root pf
	// failure does not briefly render the CONNECTED layout.
	connectedReady := eng != nil && !c.connecting

	var b strings.Builder
	switch {
	case c.connecting:
		frame := spinnerFrames[c.spinFrame%len(spinnerFrames)]
		b.WriteString(statusPill("CONNECTING "+frame, "warn"))
	case connectedReady:
		b.WriteString(statusPill("CONNECTED", "ok"))
	default:
		b.WriteString(statusPill("DISCONNECTED", "err"))
	}
	b.WriteByte('\n')

	if connectedReady {
		who := ""
		if profileID != "" {
			if p := findProfileByID(c.root.data.profiles, profileID); p != nil {
				who = "profile: " + p.Name
			}
		}
		if groupID != "" {
			if g := findGroupByID(c.root.data.groups, groupID); g != nil {
				who = "group: " + g.Name
			}
		}
		if who != "" {
			b.WriteString(dimText.Render(who))
			b.WriteByte('\n')
		}
		b.WriteString(c.viewStats(eng.Counters()))
		b.WriteByte('\n')
	}

	// Log viewport — last N lines from the ring buffer
	logHeight := height - 8
	if connectedReady {
		logHeight -= 4
	}
	if logHeight < 4 {
		logHeight = 4
	}
	b.WriteString(pageTitle(tabConnection, "Log"))
	b.WriteByte('\n')
	logBody := tailLines(c.root.ring.Snapshot(), logHeight)
	if logBody == "" {
		logBody = dimText.Render("(no output yet)")
	}
	b.WriteString(logBody)
	b.WriteByte('\n')

	switch {
	case c.connecting:
		b.WriteString(kbdHints("s", "abort"))
	case connectedReady:
		b.WriteString(kbdHints("s", "stop tunnel"))
	default:
		pairs := []string{"p", "connect to profile"}
		if len(c.root.data.groups) > 0 {
			pairs = append(pairs, "g", "connect to group")
		}
		b.WriteString(kbdHints(pairs...))
	}
	return b.String()
}

func (c *connectionModel) viewStats(co *stats.Counters) string {
	rx := co.RxTotal.Load()
	tx := co.TxTotal.Load()
	active := co.ActiveTCP.Load()
	total := co.TotalTCP.Load()
	rttMs := co.LastKeepaliveRTT().Milliseconds()
	stat := func(label, value string) string {
		return statBoxStyle.Render(
			statLabelStyle.Render(label) + "\n" + statValueStyle.Render(value),
		)
	}
	cells := []string{
		stat("RX", fmt.Sprintf("%s @ %s/s", fmtBytes(rx), fmtBytes(c.rxRate))),
		stat("TX", fmt.Sprintf("%s @ %s/s", fmtBytes(tx), fmtBytes(c.txRate))),
		stat("CONN", fmt.Sprintf("%d active · %d total", active, total)),
		stat("RTT", fmt.Sprintf("%d ms", rttMs)),
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cells...)
}

func (c *connectionModel) viewPickProfile() string {
	var b strings.Builder
	b.WriteString(pageTitle(tabConnection, "Select profile"))
	b.WriteString("\n\n")
	for i, p := range c.root.data.profiles {
		row := fmt.Sprintf("  %-30s  %s", p.Name, dimText.Render(p.Remote))
		if i == c.profileIdx {
			row = "▶ " + listSelected.Render(fmt.Sprintf("%-30s  %s", p.Name, p.Remote))
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(dimText.Render("[↑/↓] move  [Enter] connect  [Esc] cancel"))
	return b.String()
}

func (c *connectionModel) viewPickGroup() string {
	var b strings.Builder
	b.WriteString(pageTitle(tabConnection, "Select group"))
	b.WriteString("\n\n")
	for i, g := range c.root.data.groups {
		row := fmt.Sprintf("  %-30s  (%d profiles)", g.Name, len(g.ChildrenIDs))
		if i == c.groupIdx {
			row = "▶ " + listSelected.Render(fmt.Sprintf("%-30s  (%d profiles)", g.Name, len(g.ChildrenIDs)))
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(dimText.Render("[↑/↓] move  [Enter] connect  [Esc] cancel"))
	return b.String()
}

func tailLines(s string, n int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func fmtBytes(n int64) string {
	const (
		KB = 1 << 10
		MB = 1 << 20
		GB = 1 << 30
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.2f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.2f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
