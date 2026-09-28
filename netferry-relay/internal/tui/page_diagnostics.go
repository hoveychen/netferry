package tui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hoveychen/netferry/relay/internal/nexttrace"
)

// Swappable in tests.
var (
	traceEnsure = nexttrace.Ensure
	traceRun    = nexttrace.Run
	traceFind   = nexttrace.Find
)

// traceMsg is one event from a running trace; routed to the diagnostics page
// whichever page is showing, so the event pump never stalls.
type traceMsg struct {
	run      int
	hop      *nexttrace.Hop
	dl       *[2]int64 // bytes, total
	started  bool      // download finished, nexttrace launched
	done     bool
	exitCode int
	err      error
}

func (traceMsg) targetPage() int { return pageDiagnostics }

// diagnosticsPage is DiagnosticsPage: a NextTrace run with streaming hops.
type diagnosticsPage struct {
	app  *App
	form *Form

	run         int // increments per start, so stale events are dropped
	events      chan traceMsg
	cancel      context.CancelFunc
	running     bool
	downloading bool
	dl          [2]int64
	hops        []nexttrace.Hop
	status      string
	errMsg      string
}

func newDiagnosticsPage(a *App) *diagnosticsPage {
	p := &diagnosticsPage{app: a}
	p.build()
	return p
}

func (p *diagnosticsPage) title() string { return "Diagnostics" }

func (p *diagnosticsPage) capturing() bool { return p.form.Capturing() }

func (p *diagnosticsPage) build() {
	opts := []Option{{"None", ""}}
	if p.app.data != nil {
		for _, pr := range p.app.data.Profiles {
			opts = append(opts, Option{pr.Name + " — " + pr.Remote, pr.ID})
		}
	}
	from := selectField("profile", "Fill from profile", opts, "")
	target := textField("target", "Target host", "", "host.example.com or user@host:port")
	var geo []Option
	for _, g := range nexttrace.GeoSources {
		geo = append(geo, Option{g, g})
	}
	geoF := selectField("geo", "Data source", geo, nexttrace.GeoSources[0])
	hops := textField("hops", "Max hops", "30", "1–64")
	queries := textField("queries", "Queries per hop", "1", "1–5")
	start := buttonField("start", "Start trace", p.toggle)
	p.form = newForm(from, target, geoF, hops, queries, start)
	p.form.OnChange = func(key string) {
		if key != "profile" {
			return
		}
		if pr := p.app.data.Profile(p.form.Field("profile").Value()); pr != nil {
			p.form.Field("target").SetText(pr.Remote)
		}
	}
}

// entered refreshes the profile list (profiles may have changed).
func (p *diagnosticsPage) entered() tea.Cmd {
	f := p.form.Field("profile")
	opts := []Option{{"None", ""}}
	for _, pr := range p.app.data.Profiles {
		opts = append(opts, Option{pr.Name + " — " + pr.Remote, pr.ID})
	}
	f.SetOptions(opts)
	return nil
}

// numField parses a number field, falling back to def and clamping.
func numField(s string, lo, hi, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n == 0 {
		n = def
	}
	return max(lo, min(hi, n))
}

func (p *diagnosticsPage) toggle() tea.Cmd {
	if p.running || p.downloading {
		p.stop()
		return nil
	}
	return p.start()
}

func (p *diagnosticsPage) start() tea.Cmd {
	fl := p.form.Field
	raw := fl("target").Text()
	if raw == "" {
		p.errMsg = "enter a target host first"
		return nil
	}
	host, err := nexttrace.ParseTarget(raw)
	if err != nil {
		p.errMsg = "Error: " + err.Error()
		return nil
	}
	o := nexttrace.Options{
		MaxHops: numField(fl("hops").Text(), 1, 64, 30),
		Queries: numField(fl("queries").Text(), 1, 5, 1),
		Geo:     fl("geo").Value(),
	}
	fl("hops").SetText(strconv.Itoa(o.MaxHops))
	fl("queries").SetText(strconv.Itoa(o.Queries))

	p.run++
	run := p.run
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan traceMsg, 64)
	p.events, p.cancel = ch, cancel
	p.hops, p.status, p.errMsg = nil, "", ""
	p.dl = [2]int64{}
	if traceFind() == "" {
		p.downloading = true
	} else {
		p.running = true
	}
	go func() {
		defer close(ch)
		exe, err := traceEnsure(ctx, func(n, total int64) {
			select {
			case ch <- traceMsg{run: run, dl: &[2]int64{n, total}}:
			default: // progress is lossy; the next update supersedes it
			}
		})
		if err != nil {
			ch <- traceMsg{run: run, done: true, err: fmt.Errorf("download failed: %w", err)}
			return
		}
		ch <- traceMsg{run: run, started: true}
		code, err := traceRun(ctx, exe, host, o, func(h nexttrace.Hop) { ch <- traceMsg{run: run, hop: &h} })
		ch <- traceMsg{run: run, done: true, exitCode: code, err: err}
	}()
	return p.wait()
}

// wait reads the next event of the current run.
func (p *diagnosticsPage) wait() tea.Cmd {
	ch := p.events
	return func() tea.Msg {
		m, ok := <-ch
		if !ok {
			return nil
		}
		return m
	}
}

func (p *diagnosticsPage) stop() {
	if p.cancel != nil {
		p.cancel()
	}
}

// shutdown kills a running trace when the TUI exits.
func (p *diagnosticsPage) shutdown() { p.stop() }

func (p *diagnosticsPage) onTrace(m traceMsg) tea.Cmd {
	if m.run != p.run {
		return nil
	}
	switch {
	case m.dl != nil:
		p.dl = *m.dl
	case m.started:
		p.downloading, p.running = false, true
	case m.hop != nil:
		// Replace an existing TTL (nexttrace redraws rows with -q > 1).
		for i := range p.hops {
			if p.hops[i].TTL == m.hop.TTL {
				p.hops[i] = *m.hop
				return p.wait()
			}
		}
		p.hops = append(p.hops, *m.hop)
		sort.SliceStable(p.hops, func(i, j int) bool { return p.hops[i].TTL < p.hops[j].TTL })
	case m.done:
		p.running, p.downloading, p.cancel = false, false, nil
		switch {
		case m.err != nil:
			p.errMsg = m.err.Error()
		case m.exitCode == 0:
			p.status = "Finished."
		case m.exitCode < 0:
			p.status = "Stopped."
		default:
			p.status = fmt.Sprintf("Finished (exit %d).", m.exitCode)
		}
		return nil
	}
	return p.wait()
}

func (p *diagnosticsPage) update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(traceMsg); ok {
		return p.onTrace(m)
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	busy := p.running || p.downloading
	switch km.String() {
	case "enter":
		if f := p.form.focused(); f != nil && f.Key == "target" {
			return p.toggle()
		}
	case "ctrl+r":
		return p.toggle()
	case "esc":
		if busy {
			p.stop()
		}
		return nil
	}
	if busy {
		// Inputs are frozen while a trace runs; only moving focus is allowed.
		if f := p.form.focused(); f != nil && f.Key != "start" {
			switch km.String() {
			case "up", "down", "tab", "shift+tab":
			default:
				return nil
			}
		}
	}
	cmd, _ := p.form.Update(msg)
	return cmd
}

func (p *diagnosticsPage) hints() string {
	if p.running || p.downloading {
		return hints("esc/ctrl+r", "stop")
	}
	return hints("↑/↓", "field", "←/→", "change", "enter/ctrl+r", "start trace")
}

func fmtRTT(h nexttrace.Hop) string {
	switch {
	case h.Timeout || !h.HasRTT:
		return "—"
	case h.RTTMs < 10:
		return fmt.Sprintf("%.2f ms", h.RTTMs)
	case h.RTTMs < 100:
		return fmt.Sprintf("%.1f ms", h.RTTMs)
	}
	return fmt.Sprintf("%.0f ms", h.RTTMs)
}

func hopLocation(h nexttrace.Hop) string {
	var geo []string
	for _, s := range []string{h.Country, h.Province, h.City} {
		if s != "" {
			geo = append(geo, s)
		}
	}
	g := strings.Join(geo, " ")
	owner := h.ISP
	if owner == "" {
		owner = h.Owner
	}
	if g != "" && owner != "" {
		return g + " · " + owner
	}
	return g + owner
}

func (p *diagnosticsPage) view(width, height int) string {
	p.form.Field("start").Label = "Start trace"
	if p.running || p.downloading {
		p.form.Field("start").Label = "Stop"
	}
	head := sTitle.Render("Route Diagnostics")
	switch {
	case p.downloading:
		pct := ""
		if p.dl[1] > 0 {
			pct = fmt.Sprintf(" %d%%", p.dl[0]*100/p.dl[1])
		}
		head += "  " + sWarn.Render("Downloading NextTrace…"+pct)
	case p.running:
		head += "  " + sWarn.Render("Tracing…")
	}
	lines := []string{head, sMuted.Render("Trace the underlay path from this machine to a target host using NextTrace.")}

	if !p.downloading && !p.running && traceFind() == "" {
		path, _ := nexttrace.InstallPath()
		lines = append(lines, "",
			sInfo.Render("First-run download: NextTrace "+nexttrace.Version+" required"),
			sMuted.Width(width).Render("The tool is fetched from GitHub on demand (~11 MB) when you start a trace. If your network blocks GitHub, download it manually and drop the file at the path below (or set NETFERRY_NEXTTRACE_BIN)."),
			sDim.Render(truncate("Install path: "+path, width)))
		if u := nexttrace.DownloadURL(); u != "" {
			lines = append(lines, sDim.Render(truncate(u, width)))
		}
	}
	if p.downloading {
		done := fmtBytes(p.dl[0])
		if p.dl[1] > 0 {
			done += " / " + fmtBytes(p.dl[1])
		}
		frac := 0.2 // unknown size: an indeterminate sliver, like the desktop
		if p.dl[1] > 0 {
			frac = float64(p.dl[0]) / float64(p.dl[1])
		}
		lines = append(lines, "", progressBar(frac, width-lipgloss.Width(done)-2, cAccent)+"  "+sMuted.Render(done))
	}

	lines = append(lines, "", strings.TrimRight(p.form.View(width), "\n"))
	if p.status != "" {
		lines = append(lines, sMuted.Render(p.status))
	}
	if p.errMsg != "" {
		lines = append(lines, sErr.Render(truncate(p.errMsg, width)))
	}
	lines = append(lines, "")

	top := strings.Join(lines, "\n")
	if len(p.hops) == 0 {
		return top + "\n" + sMuted.Render(`Enter a target host and press enter on "Start trace".`)
	}
	ipW := 34
	if width < 90 {
		ipW = 24
	}
	row := func(ttl, ip, rtt, as, loc string) string {
		return truncate(padRight(ttl, 4)+padRight(ip, ipW)+padLeft(rtt, 10)+"  "+padRight(as, 10)+loc, width)
	}
	table := []string{sSection.Render(row("#", "HOP", "RTT", "AS", "OWNER / LOCATION"))}
	for _, h := range p.hops {
		ip := h.IP
		if ip == "" {
			ip = "—"
		}
		if h.Hostname != "" && h.Hostname != h.IP {
			ip += " " + h.Hostname
		}
		if h.Timeout {
			table = append(table, sDim.Render(row(strconv.Itoa(h.TTL), "* No reply", "—", "", "")))
			continue
		}
		as := ""
		if h.ASN != "" {
			as = "AS" + h.ASN
		}
		table = append(table, row(strconv.Itoa(h.TTL), truncate(ip, ipW-1), fmtRTT(h), as, hopLocation(h)))
	}
	// Keep the newest hops visible when the table outgrows the screen.
	room := height - strings.Count(top, "\n") - 2
	if room < 2 {
		room = 2
	}
	if len(table) > room {
		table = append(table[:1], table[len(table)-room+1:]...)
	}
	return top + "\n" + strings.Join(table, "\n")
}

func padLeft(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}
