package tui

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// connTab is a ConnectionPage sub-view.
type connTab int

const (
	tabSpeed connTab = iota
	tabConns
	tabDests
	tabLogs
	tabErrors
	numConnTabs
)

// destSort is the Destinations tab ordering (DestSortKey).
type destSort int

const (
	sortTotalBytes destSort = iota
	sortSpeed
	sortActive
	sortTotalConns
	sortRecent
	numDestSorts
)

var destSortLabels = []string{"data", "speed", "active", "total", "recent"}

const maxRecentClosedShown = 50

type connectionPage struct {
	app    *App
	tab    connTab
	sort   destSort
	scroll [numConnTabs]int // lines scrolled; for Logs it counts up from the tail
	bodyH  int              // height of the scrollable area in the last view
}

func newConnectionPage(a *App) *connectionPage { return &connectionPage{app: a} }

func (p *connectionPage) title() string   { return "Connection" }
func (p *connectionPage) capturing() bool { return false }

func (p *connectionPage) hints() string {
	h := []string{"←/→", "view", "↑/↓", "scroll"}
	if p.tab == tabDests {
		h = append(h, "o", "sort: "+destSortLabels[p.sort])
	}
	if p.app.session.Active() {
		h = append(h, "d", "disconnect")
	}
	return hints(h...)
}

func (p *connectionPage) update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	page := p.bodyH - 1
	if page < 1 {
		page = 1
	}
	switch km.String() {
	case "right", "l":
		p.tab = (p.tab + 1) % numConnTabs
	case "left", "h":
		p.tab = (p.tab - 1 + numConnTabs) % numConnTabs
	case "s":
		p.tab = tabSpeed
	case "c":
		p.tab = tabConns
	case "t":
		p.tab = tabDests
	case "g":
		p.tab = tabLogs
	case "e":
		p.tab = tabErrors
	case "o":
		if p.tab == tabDests {
			p.sort = (p.sort + 1) % numDestSorts
			p.scroll[tabDests] = 0
		}
	case "down", "j":
		p.scrollBy(1)
	case "up", "k":
		p.scrollBy(-1)
	case "pgdown", " ":
		p.scrollBy(page)
	case "pgup":
		p.scrollBy(-page)
	case "home":
		p.scrollTo(true)
	case "end":
		p.scrollTo(false)
	case "d":
		if p.app.session.Active() {
			p.app.disconnect()
			return p.app.setFlash(true, "Disconnecting…")
		}
	}
	return nil
}

// scrollBy moves the view; the Logs tab scrolls from the tail so "down"
// moves toward the newest line.
func (p *connectionPage) scrollBy(n int) {
	if p.tab == tabLogs {
		n = -n
	}
	p.scroll[p.tab] += n
	if p.scroll[p.tab] < 0 {
		p.scroll[p.tab] = 0
	}
}

func (p *connectionPage) scrollTo(top bool) {
	if (p.tab == tabLogs) == top {
		p.scroll[p.tab] = math.MaxInt32 // clamped on render
	} else {
		p.scroll[p.tab] = 0
	}
}

// ── view ─────────────────────────────────────────────────────────────────────

func (p *connectionPage) view(width, height int) string {
	a := p.app
	if a.sess.Spec == nil || a.sess.Status == StatusDisconnected {
		return sMuted.Render("Not connected.") + "\n\n" +
			sMuted.Render("Pick a profile on the ") + sAccent.Render("1 Profiles") + sMuted.Render(" page and press ") +
			sKey.Render("enter") + sMuted.Render(" to connect.")
	}

	var top []string
	top = append(top, p.headerLine(width))
	if b := p.banner(width); b != "" {
		top = append(top, b)
	}
	if a.live.stats != nil {
		top = append(top, statCards(*a.live.stats, width))
	}
	top = append(top, p.tabBar())
	head := strings.Join(top, "\n")

	bodyH := height - lipgloss.Height(head) - 1
	if bodyH < 1 {
		bodyH = 1
	}
	p.bodyH = bodyH

	var lines []string
	switch p.tab {
	case tabSpeed:
		lines = p.speedView(width, bodyH)
	case tabConns:
		lines = p.connsView(width)
	case tabDests:
		lines = p.destsView(width)
	case tabLogs:
		lines = p.logsView(width)
	case tabErrors:
		lines = p.errorsView(width)
	}
	return head + "\n\n" + p.window(lines, bodyH)
}

// window clamps the current tab's scroll offset and returns the visible slice.
func (p *connectionPage) window(lines []string, h int) string {
	maxOff := len(lines) - h
	if maxOff < 0 {
		maxOff = 0
	}
	off := p.scroll[p.tab]
	if off > maxOff {
		off = maxOff
		p.scroll[p.tab] = off
	}
	start := off
	if p.tab == tabLogs {
		start = maxOff - off
	}
	end := start + h
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start:end], "\n")
}

func (p *connectionPage) headerLine(width int) string {
	a := p.app
	spec := a.sess.Spec
	var name, sub string
	if spec.Group != nil && len(spec.Children) > 1 {
		name = spec.Group.Name
		sub = fmt.Sprintf("group · %d profiles · default %s", len(spec.Children), spec.Children[0].Name)
	} else {
		name = spec.Profile.Name
		sub = spec.Profile.Remote
	}
	return truncate(sBold.Render(name)+"  "+sMuted.Render(sub), width)
}

// banner is the status message strip, or the deploy progress bar while the
// server binary is being uploaded.
func (p *connectionPage) banner(width int) string {
	s := p.app.sess
	if s.Status == StatusConnecting && s.DeployTotal > 0 {
		label := "Deploying tunnel server…"
		switch s.DeployReason {
		case "first-deploy":
			label = "Deploying tunnel server for the first time…"
		case "size-mismatch":
			label = "Updating tunnel server (version changed)…"
		}
		frac := float64(s.DeploySent) / float64(s.DeployTotal)
		if frac > 1 {
			frac = 1
		}
		right := fmt.Sprintf("%s / %s  %d%%", fmtBytes(s.DeploySent), fmtBytes(s.DeployTotal), int(math.Round(frac*100)))
		barW := width - 4
		if barW > 60 {
			barW = 60
		}
		return sBold.Render(label) + "  " + sMuted.Render(right) + "\n" + progressBar(frac, barW, cAccent)
	}
	if s.Message == "" {
		return ""
	}
	if s.Status == StatusReconnecting {
		return sWarn.Render("● "+s.Message) + "  " + sMuted.Render("(firewall rules kept active)")
	}
	return sWarn.Render(truncate(s.Message, width))
}

func progressBar(frac float64, w int, c lipgloss.TerminalColor) string {
	if w < 1 {
		return ""
	}
	n := int(math.Round(frac * float64(w)))
	if n > w {
		n = w
	}
	return lipgloss.NewStyle().Foreground(c).Render(strings.Repeat("█", n)) + sDim.Render(strings.Repeat("░", w-n))
}

// statCards renders the four summary cards in one row.
func statCards(st stats.Snapshot, width int) string {
	type card struct {
		label, value, sub string
		style             lipgloss.Style
	}
	cards := []card{
		{"↓ DOWNLOAD", fmtRate(st.RxBytesPerSec), "total " + fmtBytes(st.TotalRxBytes), sOK},
		{"↑ UPLOAD", fmtRate(st.TxBytesPerSec), "total " + fmtBytes(st.TotalTxBytes), sAccent},
		{"⇄ CONNECTIONS", strconv.Itoa(int(st.ActiveConns)), fmt.Sprintf("%d total", st.TotalConns), sBold},
		{"DNS", strconv.FormatInt(st.DNSQueries, 10), "queries", sInfo},
	}
	cw := width/len(cards) - 1
	if cw < 14 {
		// Too narrow for boxes: one compact line.
		var parts []string
		for _, c := range cards {
			parts = append(parts, sMuted.Render(c.label)+" "+c.style.Render(c.value))
		}
		return strings.Join(parts, "  ")
	}
	boxes := make([]string, len(cards))
	for i, c := range cards {
		inner := cw - 4
		boxes[i] = sBox.Width(cw - 2).Render(
			truncate(sSection.Render(c.label), inner) + "\n" +
				truncate(c.style.Bold(true).Render(c.value), inner) + "\n" +
				truncate(sMuted.Render(c.sub), inner))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, joinWithGap(boxes, " ")...)
}

func joinWithGap(items []string, gap string) []string {
	out := make([]string, 0, 2*len(items))
	for i, it := range items {
		if i > 0 {
			out = append(out, gap)
		}
		out = append(out, it)
	}
	return out
}

func (p *connectionPage) tabBar() string {
	a := p.app
	names := []string{"Speed", "Connections", "Destinations", "Logs", "Errors"}
	badges := []int{0, len(a.live.active), len(a.live.dests), 0, len(a.sess.Errors)}
	var parts []string
	for i, n := range names {
		label := n
		if badges[i] > 0 {
			label += " " + strconv.Itoa(badges[i])
		}
		label = " " + label + " "
		switch {
		case connTab(i) == p.tab:
			parts = append(parts, sSelected.Render(label))
		case i == int(tabErrors) && badges[i] > 0:
			parts = append(parts, sErr.Render(label))
		default:
			parts = append(parts, sMuted.Render(label))
		}
	}
	return strings.Join(parts, " ")
}

// ── Speed ────────────────────────────────────────────────────────────────────

func (p *connectionPage) speedView(width, height int) []string {
	a := p.app
	hist := a.live.history
	if len(hist) == 0 {
		return []string{sMuted.Render("Waiting for speed data…")}
	}
	children := p.groupChildren()
	var tunnels []stats.TunnelSnapshot
	if a.live.stats != nil {
		tunnels = a.live.stats.Tunnels
	}

	// Leave room below the chart for the legend and breakdowns, but keep the
	// chart readable.
	chartH := height - 3
	if len(children) > 1 || len(tunnels) > 1 {
		chartH = height / 2
	}
	if chartH > 12 {
		chartH = 12
	}
	if chartH < 3 {
		chartH = 3
	}
	lines := speedChart(hist, width, chartH)
	last := hist[len(hist)-1]
	lines = append(lines, "",
		sOK.Render("█")+" "+sMuted.Render("Download ")+sOK.Bold(true).Render(fmtRate(last.RxBytesPerSec))+"   "+
			sAccent.Render("•")+" "+sMuted.Render("Upload ")+sAccent.Bold(true).Render(fmtRate(last.TxBytesPerSec))+"   "+
			sDim.Render(fmt.Sprintf("last %ds", len(hist))))

	if len(children) > 1 {
		lines = append(lines, "", sSection.Render("PER PROFILE"))
		lines = append(lines, strings.Split(perProfileCards(children, tunnels, p.perProfileActive(children), width), "\n")...)
	}
	if len(tunnels) > 1 {
		lines = append(lines, "", sSection.Render("PER TUNNEL"))
		lines = append(lines, strings.Split(tunnelCards(tunnels, width), "\n")...)
	}
	return lines
}

// groupChildren returns the running group's profiles, or nil in solo mode.
func (p *connectionPage) groupChildren() []profile.Profile {
	s := p.app.sess.Spec
	if s == nil || s.Group == nil {
		return nil
	}
	return s.Children
}

// perProfileActive counts active connections per profile; unstamped ones are
// attributed to the default (first) child.
func (p *connectionPage) perProfileActive(children []profile.Profile) map[string]int {
	out := map[string]int{}
	if len(children) == 0 {
		return out
	}
	for _, c := range p.app.live.active {
		id := c.ActiveProfileID
		if id == "" {
			id = children[0].ID
		}
		out[id]++
	}
	return out
}

var blockRunes = []rune(" ▁▂▃▄▅▆▇█")

// speedChart draws download as filled bars and upload as a dotted line,
// scaled to the larger of the two, with a y-axis on the left.
func speedChart(hist []stats.Snapshot, width, height int) []string {
	var maxV int64 = 1024
	for _, s := range hist {
		if s.RxBytesPerSec > maxV {
			maxV = s.RxBytesPerSec
		}
		if s.TxBytesPerSec > maxV {
			maxV = s.TxBytesPerSec
		}
	}
	yMax := (maxV + 1023) / 1024 * 1024

	const axisW = 11 // "1023.9 KB/s" plus separator
	plotW := width - axisW - 1
	if plotW < 10 {
		plotW = 10
	}
	// One column per sample, newest on the right; stretch when there is room.
	cols := plotW
	if len(hist) < cols {
		cols = len(hist) * (plotW / speedHistoryLen)
		if cols < len(hist) {
			cols = len(hist)
		}
		if cols > plotW {
			cols = plotW
		}
	}
	sample := func(c int) stats.Snapshot {
		i := c * len(hist) / cols
		if i >= len(hist) {
			i = len(hist) - 1
		}
		return hist[i]
	}

	cells := height * 8
	grid := make([][]string, height)
	for r := range grid {
		grid[r] = make([]string, cols)
		for c := range grid[r] {
			grid[r][c] = " "
		}
	}
	rxStyle, txStyle := sOK, sAccent.Bold(true)
	for c := 0; c < cols; c++ {
		s := sample(c)
		level := int(math.Round(float64(s.RxBytesPerSec) / float64(yMax) * float64(cells)))
		for r := 0; r < height; r++ { // r = 0 is the bottom row
			fill := level - r*8
			if fill <= 0 {
				continue
			}
			if fill > 8 {
				fill = 8
			}
			grid[height-1-r][c] = rxStyle.Render(string(blockRunes[fill]))
		}
		txRow := int(math.Round(float64(s.TxBytesPerSec) / float64(yMax) * float64(height-1)))
		if s.TxBytesPerSec > 0 || c == cols-1 {
			grid[height-1-txRow][c] = txStyle.Render("•")
		}
	}

	lines := make([]string, height)
	for r := 0; r < height; r++ {
		label := ""
		switch r {
		case 0:
			label = fmtRate(yMax)
		case height / 2:
			if height >= 5 {
				label = fmtRate(yMax / 2)
			}
		case height - 1:
			label = "0"
		}
		lines[r] = sMuted.Render(fmt.Sprintf("%*s", axisW-1, label)) + sDim.Render(" ┤") + strings.Join(grid[r], "")
	}
	return lines
}

// cardGrid lays out equally sized boxes, wrapping by the available width.
func cardGrid(boxes []string, cardW, width int) string {
	perRow := width / (cardW + 1)
	if perRow < 1 {
		perRow = 1
	}
	if perRow > 4 {
		perRow = 4
	}
	var rows []string
	for i := 0; i < len(boxes); i += perRow {
		end := i + perRow
		if end > len(boxes) {
			end = len(boxes)
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, joinWithGap(boxes[i:end], " ")...))
	}
	return strings.Join(rows, "\n")
}

const breakdownCardW = 28

func kv(k, v string, w int) string {
	gap := w - lipgloss.Width(k) - lipgloss.Width(v)
	if gap < 1 {
		gap = 1
	}
	return k + strings.Repeat(" ", gap) + v
}

func perProfileCards(children []profile.Profile, tunnels []stats.TunnelSnapshot, active map[string]int, width int) string {
	byID := map[string]stats.TunnelSnapshot{}
	for _, t := range tunnels {
		if t.ProfileID != "" {
			byID[t.ProfileID] = t
		}
	}
	positional := len(tunnels) == len(children)
	inner := breakdownCardW - 4
	boxes := make([]string, len(children))
	for i, ch := range children {
		st := tunnelStyle(i)
		t, ok := byID[ch.ID]
		if !ok && positional {
			t, ok = tunnels[i], true
		}
		title := st.Bold(true).Render("● " + truncate(ch.Name, inner-10))
		if i == 0 {
			title = kv(title, sDim.Render("default"), inner)
		}
		rx, tx, rtt := "—", "—", sDim.Render("—")
		if ok {
			rx, tx = fmtRate(t.RxBytesPerSec), fmtRate(t.TxBytesPerSec)
			rtt = rttStyle(t.LastRttUs).Render(fmtRtt(t.LastRttUs))
		}
		body := []string{
			title,
			kv(sMuted.Render("↓"), st.Render(rx), inner),
			kv(sMuted.Render("↑"), tx, inner),
			kv(sMuted.Render("conns"), strconv.Itoa(active[ch.ID]), inner),
			kv(sMuted.Render("rtt"), rtt, inner),
		}
		if !ok {
			body = append(body, sDim.Render("(pending per-profile stats)"))
		}
		boxes[i] = sBox.Width(breakdownCardW - 2).Render(strings.Join(body, "\n"))
	}
	return cardGrid(boxes, breakdownCardW, width)
}

func tunnelCards(tunnels []stats.TunnelSnapshot, width int) string {
	maxScore := 1.0
	for _, t := range tunnels {
		if t.CongestionScore > maxScore {
			maxScore = t.CongestionScore
		}
	}
	inner := breakdownCardW - 4
	boxes := make([]string, len(tunnels))
	for i, t := range tunnels {
		st := tunnelStyle(t.Index - 1)
		title := st.Bold(true).Render(fmt.Sprintf("● TUNNEL %d", t.Index))
		border := cDim
		var body []string
		switch t.State {
		case "dead":
			title = sErr.Bold(true).Render(fmt.Sprintf("● TUNNEL %d", t.Index)) + " " + sErr.Render("dead")
			border = cErr
			body = []string{title, sErr.Render("Reconnection failed")}
		default:
			if t.State == "reconnecting" {
				title = sWarn.Bold(true).Render(fmt.Sprintf("● TUNNEL %d", t.Index)) + " " + sWarn.Render("reconnecting")
				border = cWarn
			}
			rtt := rttStyle(t.LastRttUs).Render(fmtRtt(t.LastRttUs))
			if t.MinRttUs > 0 && t.MinRttUs != t.LastRttUs {
				rtt += sDim.Render(" (" + fmtRtt(t.MinRttUs) + " min)")
			}
			jitter := "—"
			if t.LastRttUs != 0 {
				jitter = fmtRtt(t.JitterUs)
				if t.JitterUs > 30_000 {
					jitter = sWarn.Render(jitter)
				}
			}
			frac := t.CongestionScore / maxScore
			loadC := cOK
			if frac > 0.8 {
				loadC = cErr
			} else if frac > 0.5 {
				loadC = cWarn
			}
			label, style := diagnoseTunnel(t)
			body = []string{
				title,
				kv(sMuted.Render("↓"), st.Render(fmtRate(t.RxBytesPerSec)), inner),
				kv(sMuted.Render("↑"), fmtRate(t.TxBytesPerSec), inner),
				kv(sMuted.Render("conns"), strconv.Itoa(int(t.ActiveConns)), inner),
				kv(sMuted.Render("rtt"), rtt, inner),
				kv(sMuted.Render("jitter"), jitter, inner),
				kv(sMuted.Render("load"), sDim.Render(fmt.Sprintf("%.1f", t.CongestionScore)), inner),
				progressBar(frac, inner, loadC),
				style.Render(truncate(label, inner)),
			}
		}
		boxes[i] = sBox.BorderForeground(border).Width(breakdownCardW - 2).Render(strings.Join(body, "\n"))
	}
	return cardGrid(boxes, breakdownCardW, width)
}

// diagnoseTunnel mirrors ConnectionPage's useDiagnoseTunnel thresholds.
func diagnoseTunnel(t stats.TunnelSnapshot) (string, lipgloss.Style) {
	if t.LastRttUs == 0 {
		return "Waiting for data…", sDim
	}
	lastMs := float64(t.LastRttUs) / 1000
	minMs := float64(t.MinRttUs) / 1000
	jitterMs := float64(t.JitterUs) / 1000
	inflation := 1.0
	if minMs > 0 {
		inflation = lastMs / minMs
	}
	switch {
	case jitterMs > 30 && inflation > 2:
		return "Congestion", sErr
	case jitterMs > 30:
		return "Unstable link", sWarn
	case inflation > 3 && jitterMs < 15:
		return fmt.Sprintf("Bufferbloat (%.0f× RTT)", inflation), sErr
	case inflation > 1.8 && jitterMs < 15:
		return fmt.Sprintf("Possible bufferbloat (%.1f×)", inflation), sWarn
	case minMs > 150:
		return "High latency", sWarn
	}
	return "Healthy", sOK
}

func fmtRtt(us int64) string {
	switch {
	case us == 0:
		return "—"
	case us < 1000:
		return fmt.Sprintf("%dµs", us)
	case us < 10000:
		return fmt.Sprintf("%.1fms", float64(us)/1000)
	}
	return fmt.Sprintf("%.0fms", float64(us)/1000)
}

func rttStyle(us int64) lipgloss.Style {
	switch {
	case us == 0:
		return sDim
	case us >= 200_000:
		return sErr
	case us >= 50_000:
		return sWarn
	}
	return lipgloss.NewStyle()
}

// ── Connections ──────────────────────────────────────────────────────────────

// splitHostPort mirrors parseHost: prefer the resolved host (SNI/HTTP Host).
func splitHostPort(dst, resolved string) (host, port, scheme string) {
	host = dst
	if i := strings.LastIndex(dst, ":"); i > 0 {
		host, port = dst[:i], dst[i+1:]
	}
	switch port {
	case "443":
		scheme = "https"
	case "80":
		scheme = "http"
	}
	if resolved != "" {
		host = resolved
	}
	return strings.Trim(host, "[]"), port, scheme
}

func fmtClock(ms int64) string { return time.UnixMilli(ms).Format("15:04:05") }

func (p *connectionPage) connsView(width int) []string {
	a := p.app
	active := a.live.sortedConns()
	closed := a.live.closed
	if len(active) == 0 && len(closed) == 0 {
		return []string{sMuted.Render("No connections yet.")}
	}
	children := p.groupChildren()
	multi := len(children) > 1
	var lines []string
	if len(active) > 0 {
		lines = append(lines, sSection.Render(fmt.Sprintf("ACTIVE (%d)", len(active))))
		for _, c := range active {
			host, port, scheme := splitHostPort(c.DstAddr, c.Host)
			parts := []string{sOK.Render("●"), sDim.Render(fmtClock(c.TimestampMs))}
			if multi {
				id := c.ActiveProfileID
				if id == "" {
					id = children[0].ID
				}
				if i := a.childIndex(id); i >= 0 {
					parts = append(parts, tunnelStyle(i).Bold(true).Render("["+truncate(children[i].Name, 16)+"]"))
				}
			}
			if c.TunnelIndex > 0 {
				parts = append(parts, tunnelStyle(c.TunnelIndex-1).Render(fmt.Sprintf("T%d", c.TunnelIndex)))
			}
			if scheme != "" {
				parts = append(parts, sMuted.Render(scheme))
			}
			parts = append(parts, sAccent.Render(host)+sDim.Render(":"+port))
			lines = append(lines, truncate(strings.Join(parts, " "), width))
		}
	}
	if len(closed) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, sSection.Render("RECENTLY CLOSED"))
		n := len(closed)
		if n > maxRecentClosedShown {
			n = maxRecentClosedShown
		}
		for _, c := range closed[:n] {
			host, port, scheme := splitHostPort(c.DstAddr, c.Host)
			parts := []string{sDim.Render("○"), sDim.Render(fmtClock(c.TimestampMs))}
			if scheme != "" {
				parts = append(parts, sDim.Render(scheme))
			}
			parts = append(parts, sMuted.Render(host)+sDim.Render(":"+port))
			lines = append(lines, truncate(strings.Join(parts, " "), width))
		}
	}
	return lines
}

// ── Destinations ─────────────────────────────────────────────────────────────

func sortDests(ds []stats.DestinationSnapshot, key destSort) []stats.DestinationSnapshot {
	out := append([]stats.DestinationSnapshot(nil), ds...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch key {
		case sortSpeed:
			return a.RxBytesPerSec+a.TxBytesPerSec > b.RxBytesPerSec+b.TxBytesPerSec
		case sortActive:
			return a.ActiveConns > b.ActiveConns
		case sortTotalConns:
			return a.TotalConns > b.TotalConns
		case sortRecent:
			return a.LastSeenMs > b.LastSeenMs
		}
		return a.RxBytes+a.TxBytes > b.RxBytes+b.TxBytes
	})
	return out
}

func (p *connectionPage) destsView(width int) []string {
	a := p.app
	if len(a.live.dests) == 0 {
		return []string{sMuted.Render("No destinations yet.")}
	}
	children := p.groupChildren()
	multi := len(children) > 1
	var lines []string
	var sorts []string
	for i, l := range destSortLabels {
		if destSort(i) == p.sort {
			sorts = append(sorts, sSelected.Render(" "+l+" "))
		} else {
			sorts = append(sorts, sMuted.Render(" "+l+" "))
		}
	}
	lines = append(lines, sSection.Render("SORT BY ")+strings.Join(sorts, ""))
	for _, d := range sortDests(a.live.dests, p.sort) {
		blocked, direct, isActive := d.Route == "blocked", d.Route == "direct", d.ActiveConns > 0
		dot, hostS := " ", sMuted
		switch {
		case blocked:
			dot, hostS = sErr.Render("●"), sStrike
		case direct:
			dot, hostS = sOK.Render("●"), lipgloss.NewStyle()
		case isActive:
			dot, hostS = sAccent.Render("●"), sBold
		}
		var right []string
		if multi && !blocked && !direct {
			pid, label := d.AssignedProfileID, "pinned → "
			if pid == "" {
				pid, label = d.ActiveProfileID, "via "
			}
			if i := a.childIndex(pid); pid != "" && i >= 0 {
				right = append(right, tunnelStyle(i).Bold(true).Render(label+truncate(children[i].Name, 16)))
			}
		}
		switch d.Route {
		case "direct":
			right = append(right, sOK.Render("direct"))
		case "blocked":
			right = append(right, sErr.Render("blocked"))
		}
		if sp := d.RxBytesPerSec + d.TxBytesPerSec; sp > 0 {
			right = append(right, sOK.Render(fmtRate(sp)))
		}
		right = append(right, sMuted.Render(fmtBytes(d.RxBytes+d.TxBytes)))
		r := strings.Join(right, "  ")
		left := dot + " " + hostS.Render(truncate(d.Host, width-lipgloss.Width(r)-4))
		lines = append(lines, kv(left, r, width))

		activeS := sMuted
		if isActive {
			activeS = sOK
		}
		detail := "  " + activeS.Render(fmt.Sprintf("%d active", d.ActiveConns)) + sMuted.Render(fmt.Sprintf(" / %d total", d.TotalConns)) +
			sDim.Render("   ↓ "+fmtBytes(d.RxBytes)+"  ↑ "+fmtBytes(d.TxBytes))
		if d.RxBytesPerSec > 0 {
			detail += "  " + sOK.Render("↓ "+fmtRate(d.RxBytesPerSec))
		}
		if d.TxBytesPerSec > 0 {
			detail += "  " + sAccent.Render("↑ "+fmtRate(d.TxBytesPerSec))
		}
		if len(d.ProcessNames) > 0 {
			detail += "   " + sInfo.Render(strings.Join(d.ProcessNames, ", "))
		}
		lines = append(lines, kv(truncate(detail, width-10), sDim.Render(fmtClock(d.LastSeenMs)), width))
	}
	return lines
}

// ── Logs / Errors ────────────────────────────────────────────────────────────

func (p *connectionPage) logsView(width int) []string {
	raw := p.app.opts.Log.Lines()
	if len(raw) == 0 {
		return []string{sMuted.Render("Waiting for tunnel output…")}
	}
	var lines []string
	for _, l := range raw {
		lines = append(lines, wrapPlain(l, width)...)
	}
	return lines
}

func (p *connectionPage) errorsView(width int) []string {
	errs := p.app.sess.Errors
	if len(errs) == 0 {
		return []string{sMuted.Render("No errors.")}
	}
	var lines []string
	for i := len(errs) - 1; i >= 0; i-- {
		e := errs[i]
		prefix := e.At.Format("15:04:05") + " "
		wrapped := wrapPlain(e.Message, width-len(prefix))
		for j, w := range wrapped {
			if j == 0 {
				lines = append(lines, sDim.Render(prefix)+sErr.Render(w))
			} else {
				lines = append(lines, strings.Repeat(" ", len(prefix))+sErr.Render(w))
			}
		}
	}
	return lines
}

// wrapPlain hard-wraps an unstyled line to w cells.
func wrapPlain(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	s = strings.ReplaceAll(s, "\t", "    ")
	var out []string
	for lipgloss.Width(s) > w {
		cut := truncateCells(s, w)
		out = append(out, cut)
		s = s[len(cut):]
	}
	return append(out, s)
}

// truncateCells returns the longest prefix of s (unstyled) fitting w cells.
func truncateCells(s string, w int) string {
	width := 0
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if width+rw > w {
			return s[:i]
		}
		width += rw
	}
	return s
}
