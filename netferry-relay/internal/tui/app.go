package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/stats"
	"github.com/hoveychen/netferry/relay/internal/store"
)

// Options wires the TUI to the tunnel binary (package main).
type Options struct {
	Start       Starter
	IsReconnect func(error) bool
	Log         *LogRing
	Version     string
}

// page is one top-level screen.
type page interface {
	title() string
	update(msg tea.Msg) tea.Cmd
	view(width, height int) string
	hints() string
	// capturing reports that keys go to a text field, so global shortcuts
	// (page numbers, q) must not fire.
	capturing() bool
}

const (
	pageProfiles = iota
	pageConnection
	pageDestinations
	pageDiagnostics
	pageSettings
)

// Messages.
type (
	sessionMsg struct{ ev Event }
	tickMsg    time.Time
	flashClear struct{ id int }
)

const speedHistoryLen = 60 // points on the speed chart (ConnectionPage MAX_HISTORY)
const maxClosedConns = 100

// liveState is the per-connection view of the engine stream, reset on every
// new connect (connectionStore.ts).
type liveState struct {
	stats   *stats.Snapshot
	history []stats.Snapshot
	active  map[uint64]stats.ConnEvent
	closed  []stats.ConnEvent // newest first
	dests   []stats.DestinationSnapshot
}

func newLive() liveState { return liveState{active: map[uint64]stats.ConnEvent{}} }

// modal is a blocking overlay: a confirmation or an info dialog.
type modal struct {
	title string
	body  string
	onYes func() tea.Cmd // nil = info dialog (any of enter/esc closes)
}

// App is the root Bubble Tea model.
type App struct {
	opts    Options
	data    *Data
	session *Session
	sess    SessionState
	live    liveState

	pages []page
	cur   int

	width, height int
	flash         string
	flashOK       bool
	flashID       int
	modal         *modal
	quitting      bool
	autoConnected bool
}

// Run starts the TUI and blocks until the user quits. A running tunnel is
// stopped (and its firewall rules removed) before Run returns.
func Run(opts Options) error {
	// Same first-launch bootstrap the desktop runs, so a machine that never
	// ran the desktop app still gets its Default group.
	if err := store.MigrateV2(); err != nil {
		return err
	}
	data, err := LoadData()
	if err != nil {
		return err
	}
	a := &App{opts: opts, data: data, live: newLive()}
	prog := tea.NewProgram(a, tea.WithAltScreen())
	a.session = NewSession(opts.Start, opts.IsReconnect, func(ev Event) { prog.Send(sessionMsg{ev}) })
	a.sess = a.session.State()
	a.pages = []page{
		newProfilesPage(a),
		newConnectionPage(a),
		newDestinationsPage(a),
		newDiagnosticsPage(a),
		newSettingsPage(a),
	}
	a.pushRules()
	go func() {
		for line := range opts.Log.Subscribe() {
			a.session.ObserveLog(line)
		}
	}()

	_, runErr := prog.Run()

	if a.session.Active() {
		a.session.Disconnect()
		deadline := time.Now().Add(15 * time.Second)
		for a.session.Active() && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		for a.session.Counters() != nil && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return runErr
}

func tick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{tick()}
	for _, p := range a.pages {
		if in, ok := p.(interface{ init() tea.Cmd }); ok {
			cmds = append(cmds, in.init())
		}
	}
	// Auto-connect: always solo, like App.tsx.
	if id := a.data.Settings.AutoConnectProfileID; id != "" && !a.autoConnected {
		a.autoConnected = true
		if p := a.data.Profile(id); p != nil {
			cmds = append(cmds, a.connectProfile(*p))
		}
	}
	return tea.Batch(cmds...)
}

// ── helpers used by pages ────────────────────────────────────────────────────

func (a *App) setFlash(ok bool, msg string) tea.Cmd {
	a.flashID++
	id := a.flashID
	a.flash, a.flashOK = msg, ok
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return flashClear{id} })
}

func (a *App) confirm(title, body string, onYes func() tea.Cmd) {
	a.modal = &modal{title: title, body: body, onYes: onYes}
}

func (a *App) info(title, body string) { a.modal = &modal{title: title, body: body} }

// reload re-reads the store after a page wrote to it, and re-pushes rules.
func (a *App) reload() {
	d, err := LoadData()
	if err != nil {
		a.setFlash(false, "reload: "+err.Error())
		return
	}
	a.data = d
	a.pushRules()
}

// groupMode reports whether the running (or last) session is a group session.
func (a *App) groupMode() bool { return a.sess.Spec != nil && a.sess.Spec.Group != nil }

// pushRules hands the current rules to the session (and a running engine).
func (a *App) pushRules() { a.session.SetRules(a.data.Rules(a.groupMode())) }

func (a *App) connectProfile(p profile.Profile) tea.Cmd {
	return a.connect(ConnectSpec{Profile: p, Settings: a.data.Settings})
}

// connectGroup connects the active group, seeded by its first child.
func (a *App) connectGroup() tea.Cmd {
	g := a.data.ActiveGroup()
	children := a.data.Children(g)
	if g == nil || len(children) == 0 {
		return a.setFlash(false, "the active group has no profiles")
	}
	gc := *g
	return a.connect(ConnectSpec{Profile: children[0], Group: &gc, Children: children, Settings: a.data.Settings})
}

func (a *App) connect(spec ConnectSpec) tea.Cmd {
	a.session.SetRules(a.data.Rules(spec.Group != nil))
	a.live = newLive()
	a.opts.Log.Clear()
	if err := a.session.Connect(spec); err != nil {
		return a.setFlash(false, err.Error())
	}
	a.cur = pageConnection
	return nil
}

func (a *App) disconnect() { a.session.Disconnect() }

// profileName resolves an id to a display name.
func (a *App) profileName(id string) string {
	if p := a.data.Profile(id); p != nil {
		return p.Name
	}
	if a.sess.Spec != nil {
		for _, c := range a.sess.Spec.Children {
			if c.ID == id {
				return c.Name
			}
		}
		if a.sess.Spec.Profile.ID == id {
			return a.sess.Spec.Profile.Name
		}
	}
	return id
}

// childIndex returns the index of profile id among the running group's
// children (0 = default), or -1.
func (a *App) childIndex(id string) int {
	if a.sess.Spec == nil {
		return -1
	}
	for i, c := range a.sess.Spec.Children {
		if c.ID == id {
			return i
		}
	}
	return -1
}

// ── update ───────────────────────────────────────────────────────────────────

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		return a, nil
	case tickMsg:
		return a, tea.Batch(tick(), a.pages[a.cur].update(m))
	case flashClear:
		if m.id == a.flashID {
			a.flash = ""
		}
		return a, nil
	case sessionMsg:
		return a, a.onSession(m.ev)
	case tea.KeyMsg:
		if a.modal != nil {
			return a, a.updateModal(m)
		}
		if a.quitting {
			return a, nil
		}
		p := a.pages[a.cur]
		if m.String() == "ctrl+c" {
			return a, a.requestQuit()
		}
		if !p.capturing() {
			switch m.String() {
			case "q":
				return a, a.requestQuit()
			case "1", "2", "3", "4", "5":
				a.cur = int(m.String()[0] - '1')
				return a, a.pageEntered()
			case "tab":
				a.cur = (a.cur + 1) % len(a.pages)
				return a, a.pageEntered()
			case "shift+tab":
				a.cur = (a.cur - 1 + len(a.pages)) % len(a.pages)
				return a, a.pageEntered()
			}
		}
		return a, p.update(m)
	}
	return a, a.pages[a.cur].update(msg)
}

func (a *App) pageEntered() tea.Cmd {
	if e, ok := a.pages[a.cur].(interface{ entered() tea.Cmd }); ok {
		return e.entered()
	}
	return nil
}

func (a *App) requestQuit() tea.Cmd {
	if a.session.Active() {
		a.confirm("Quit NetFerry?", "The tunnel is running. Quitting disconnects it and removes its firewall rules.", func() tea.Cmd {
			a.quitting = true
			return tea.Quit
		})
		return nil
	}
	a.quitting = true
	return tea.Quit
}

func (a *App) updateModal(m tea.KeyMsg) tea.Cmd {
	md := a.modal
	if md.onYes == nil {
		switch m.String() {
		case "enter", "esc", "q", " ":
			a.modal = nil
		}
		return nil
	}
	switch m.String() {
	case "y", "Y", "enter":
		a.modal = nil
		return md.onYes()
	case "n", "N", "esc", "q":
		a.modal = nil
	}
	return nil
}

func (a *App) onSession(ev Event) tea.Cmd {
	switch {
	case ev.State != nil:
		prev := a.sess.Status
		a.sess = *ev.State
		switch a.sess.Status {
		case StatusDisconnected:
			if prev != StatusDisconnected {
				a.live = newLive()
				a.cur = pageProfiles
			}
		case StatusError:
			if prev != StatusError {
				a.live = newLive()
				a.showConnectionError()
			}
		}
	case ev.Stats != nil:
		a.live.stats = ev.Stats
		a.live.history = append(a.live.history, *ev.Stats)
		if len(a.live.history) > speedHistoryLen {
			a.live.history = a.live.history[len(a.live.history)-speedHistoryLen:]
		}
	case ev.Conn != nil:
		c := *ev.Conn
		if c.Action == "open" {
			a.live.active[c.ID] = c
		} else {
			if open, ok := a.live.active[c.ID]; ok {
				c.Host, c.TunnelIndex, c.ActiveProfileID = open.Host, open.TunnelIndex, open.ActiveProfileID
			}
			delete(a.live.active, c.ID)
			a.live.closed = append([]stats.ConnEvent{c}, a.live.closed...)
			if len(a.live.closed) > maxClosedConns {
				a.live.closed = a.live.closed[:maxClosedConns]
			}
		}
	case ev.ConnSnapReset:
		a.live.active = map[uint64]stats.ConnEvent{}
		for _, c := range ev.ConnSnapshot {
			a.live.active[c.ID] = c
		}
	case ev.DestSnapshot != nil:
		a.live.dests = ev.DestSnapshot
		a.recordObservedHosts(ev.DestSnapshot)
	}
	return nil
}

// recordObservedHosts appends newly seen hosts to the active group's
// knownHosts and persists the group only when something was added.
func (a *App) recordObservedHosts(snap []stats.DestinationSnapshot) {
	g := a.data.ActiveGroup()
	if g == nil {
		return
	}
	hosts := make([]string, 0, len(snap))
	for _, d := range snap {
		hosts = append(hosts, d.Host)
	}
	if RecordKnownHosts(g, hosts) {
		_ = store.SaveGroup(g)
	}
}

// showConnectionError opens the ConnectionErrorDialog equivalent.
func (a *App) showConnectionError() {
	name := ""
	if a.sess.Spec != nil {
		name = a.sess.Spec.Profile.Name
	}
	var b strings.Builder
	if name != "" {
		b.WriteString(sMuted.Render("Profile: ") + name + "\n\n")
	}
	b.WriteString(sErr.Render(a.sess.Message) + "\n\n")
	if len(a.sess.Errors) == 0 {
		b.WriteString(sMuted.Render("No additional details were reported by the tunnel."))
	} else {
		b.WriteString(sSection.Render("DETAILS") + "\n")
		errs := a.sess.Errors
		if len(errs) > 12 {
			errs = errs[len(errs)-12:]
		}
		for _, e := range errs {
			b.WriteString(sMuted.Render(e.At.Format("15:04:05")) + " " + truncate(e.Message, 90) + "\n")
		}
	}
	a.info("Connection failed", b.String())
}

// ── view ─────────────────────────────────────────────────────────────────────

func (a *App) statusPill() string {
	switch a.sess.Status {
	case StatusConnected:
		return pill("CONNECTED", cOK)
	case StatusConnecting:
		return pill("CONNECTING", cWarn)
	case StatusReconnecting:
		return pill("RECONNECTING", cWarn)
	case StatusError:
		return pill("ERROR", cErr)
	}
	return pill("DISCONNECTED", cMuted)
}

func (a *App) View() string {
	if a.width == 0 {
		return ""
	}
	if a.quitting {
		return "Stopping tunnel…\n"
	}
	w := a.width
	// Header: brand, tabs, status.
	right := a.statusPill()
	renderTabs := func(compact bool) string {
		var tabs []string
		for i, p := range a.pages {
			label := fmt.Sprintf(" %d %s ", i+1, p.title())
			if compact && i != a.cur {
				label = fmt.Sprintf(" %d ", i+1)
			}
			if i == a.cur {
				tabs = append(tabs, sSelected.Render(label))
			} else {
				tabs = append(tabs, sMuted.Render(label))
			}
		}
		return sTitle.Render(" NetFerry ") + strings.Join(tabs, "")
	}
	left := renderTabs(false)
	if lipgloss.Width(left)+lipgloss.Width(right)+1 > w {
		left = renderTabs(true)
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	header := truncate(left+strings.Repeat(" ", gap)+right, w)
	rule := sDim.Render(strings.Repeat("─", w))

	bodyH := a.height - 5
	if bodyH < 3 {
		bodyH = 3
	}
	p := a.pages[a.cur]
	body := p.view(w-2, bodyH)
	body = clampLines(body, bodyH, w-2)

	flash := ""
	if a.flash != "" {
		if a.flashOK {
			flash = sOK.Render("✓ " + a.flash)
		} else {
			flash = sErr.Render("✗ " + truncate(a.flash, w-4))
		}
	}
	global := hints("1-5", "pages", "q", "quit")
	if p.capturing() {
		global = hints("ctrl+c", "quit")
	}
	footer := p.hints()
	if footer != "" {
		footer += sDim.Render("  │  ")
	}
	footer += global

	screen := strings.Join([]string{header, rule, indent(body, 1), rule, padRight(flash, w) + "\n" + truncate(footer, w)}, "\n")
	if a.modal != nil {
		return a.overlay(screen)
	}
	return screen
}

func (a *App) overlay(base string) string {
	md := a.modal
	var foot string
	if md.onYes != nil {
		foot = hints("y", "confirm", "n/esc", "cancel")
	} else {
		foot = hints("enter/esc", "close")
	}
	boxW := a.width - 10
	if boxW > 100 {
		boxW = 100
	}
	body := sTitle.Render(md.title) + "\n\n" + lipgloss.NewStyle().Width(boxW-6).Render(md.body) + "\n\n" + foot
	box := sModal.Width(boxW).Render(body)
	return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, box)
}

// clampLines keeps at most h lines, each truncated to w cells.
func clampLines(s string, h, w int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		if lipgloss.Width(l) > w {
			lines[i] = truncateANSI(l, w)
		}
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// sortedConns returns active connections newest first.
func (l *liveState) sortedConns() []stats.ConnEvent {
	out := make([]stats.ConnEvent, 0, len(l.active))
	for _, c := range l.active {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TimestampMs > out[j].TimestampMs })
	return out
}
