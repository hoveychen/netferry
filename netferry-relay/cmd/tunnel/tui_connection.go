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
type connectionStartedMsg struct {
	eng       *Engine
	profileID string
	groupID   string
	stopCh    chan struct{}
	doneCh    chan error
}

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

	lastBytesAt   time.Time
	lastRx        int64
	lastTx        int64
	rxRate        int64
	txRate        int64
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
		c.root.state.setActive(m.eng, m.profileID, m.groupID, m.stopCh, m.doneCh)
		return waitForEngineDone(m.doneCh)
	case connectionFailedMsg:
		return c.root.setFlash(false, "connect: "+m.err.Error())
	case connectionEndedMsg:
		c.root.state.clearActive(m.err)
		if m.err != nil && m.err != ErrExitForReconnect {
			return c.root.setFlash(false, "engine ended: "+m.err.Error())
		}
		return c.root.setFlash(true, "tunnel stopped")
	}
	return nil
}

func waitForEngineDone(doneCh <-chan error) tea.Cmd {
	return func() tea.Msg {
		err := <-doneCh
		return connectionEndedMsg{err: err}
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
	return startEngineCmd(cfg, profileID, "")
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
	return startEngineCmd(cfg, "", groupID)
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
	stateOnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	stateOffStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	statBoxStyle  = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("240")).
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
	connected := eng != nil

	var b strings.Builder
	if connected {
		b.WriteString(stateOnStyle.Render("● CONNECTED"))
	} else {
		b.WriteString(stateOffStyle.Render("○ DISCONNECTED"))
	}
	b.WriteByte('\n')

	if connected {
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
		b.WriteString(dimText.Render(who))
		b.WriteByte('\n')
		b.WriteString(c.viewStats(eng.Counters()))
		b.WriteByte('\n')
	}

	// Log viewport — last N lines from the ring buffer
	logHeight := height - 8
	if connected {
		logHeight -= 4
	}
	if logHeight < 4 {
		logHeight = 4
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Log:"))
	b.WriteByte('\n')
	logBody := tailLines(c.root.ring.Snapshot(), logHeight)
	if logBody == "" {
		logBody = dimText.Render("(no output yet)")
	}
	b.WriteString(logBody)
	b.WriteByte('\n')

	if connected {
		b.WriteString(dimText.Render("[s] stop tunnel"))
	} else {
		hint := "[p] connect to profile"
		if len(c.root.data.groups) > 0 {
			hint += "  [g] connect to group"
		}
		b.WriteString(dimText.Render(hint))
	}
	return b.String()
}

func (c *connectionModel) viewStats(co *stats.Counters) string {
	rx := co.RxTotal.Load()
	tx := co.TxTotal.Load()
	active := co.ActiveTCP.Load()
	total := co.TotalTCP.Load()
	rttMs := co.LastKeepaliveRTT().Milliseconds()
	cells := []string{
		statBoxStyle.Render(fmt.Sprintf("RX %s @ %s/s", fmtBytes(rx), fmtBytes(c.rxRate))),
		statBoxStyle.Render(fmt.Sprintf("TX %s @ %s/s", fmtBytes(tx), fmtBytes(c.txRate))),
		statBoxStyle.Render(fmt.Sprintf("Active %d / Total %d", active, total)),
		statBoxStyle.Render(fmt.Sprintf("RTT %d ms", rttMs)),
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cells...)
}

func (c *connectionModel) viewPickProfile() string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Select profile:"))
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
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Select group:"))
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
