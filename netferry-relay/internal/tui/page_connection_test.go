package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// newTestApp builds an App without a tea.Program; session events are dropped.
func newTestApp(d *Data) *App {
	if d == nil {
		d = &Data{Priorities: map[string]int{}}
	}
	a := &App{opts: Options{Log: NewLogRing(4096)}, data: d, live: newLive()}
	a.session = NewSession(nil, func(error) bool { return false }, func(Event) {})
	a.sess = a.session.State()
	a.pages = []page{newProfilesPage(a), newConnectionPage(a), newDestinationsPage(a), newDiagnosticsPage(a), newSettingsPage(a)}
	return a
}

func key(s string) tea.KeyMsg {
	switch s {
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// assertFits checks that the rendered screen respects the terminal size.
func assertFits(t *testing.T, screen string, w, h int) {
	t.Helper()
	lines := strings.Split(screen, "\n")
	if len(lines) > h {
		t.Fatalf("screen has %d lines, terminal is %d", len(lines), h)
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			t.Fatalf("line %d is %d cells wide (> %d): %q", i, lw, w, ansi.Strip(l))
		}
	}
}

func liveSession(a *App) {
	p1 := profile.Profile{ID: "p1", Name: "tokyo", Remote: "me@tokyo.example.com"}
	a.sess = SessionState{Status: StatusConnected, Message: "Tunnel established",
		Spec:   &ConnectSpec{Profile: p1},
		Errors: []TunnelError{{Message: "c : warning: something odd happened on the link and this line is long enough to wrap around", At: time.Now()}}}
	snap := stats.Snapshot{RxBytesPerSec: 3 << 20, TxBytesPerSec: 200 << 10, TotalRxBytes: 5 << 30, ActiveConns: 2, TotalConns: 9, DNSQueries: 12,
		Tunnels: []stats.TunnelSnapshot{
			{Index: 1, State: "alive", RxBytesPerSec: 2 << 20, LastRttUs: 40_000, MinRttUs: 38_000, JitterUs: 2_000, CongestionScore: 3},
			{Index: 2, State: "reconnecting", LastRttUs: 300_000, MinRttUs: 80_000, JitterUs: 5_000, CongestionScore: 9},
			{Index: 3, State: "dead"},
		}}
	for i := 0; i < 70; i++ {
		s := snap
		s.RxBytesPerSec = int64(i) << 16
		a.onSession(Event{Stats: &s})
	}
	a.onSession(Event{Stats: &snap})
	a.onSession(Event{Conn: &stats.ConnEvent{ID: 1, Action: "open", DstAddr: "1.2.3.4:443", Host: "github.com", TunnelIndex: 1, TimestampMs: 1000}})
	a.onSession(Event{Conn: &stats.ConnEvent{ID: 2, Action: "open", DstAddr: "[2001:db8::1]:80", TunnelIndex: 2, TimestampMs: 2000}})
	a.onSession(Event{Conn: &stats.ConnEvent{ID: 3, Action: "open", DstAddr: "5.6.7.8:22", TimestampMs: 500}})
	a.onSession(Event{Conn: &stats.ConnEvent{ID: 3, Action: "close", DstAddr: "5.6.7.8:22", TimestampMs: 3000}})
	a.live.dests = []stats.DestinationSnapshot{
		{Host: "github.com", ActiveConns: 1, TotalConns: 4, RxBytes: 900, RxBytesPerSec: 100, Route: "tunnel", ProcessNames: []string{"git", "curl"}, LastSeenMs: 5000},
		{Host: "ads.example", TotalConns: 2, RxBytes: 5000, Route: "blocked", LastSeenMs: 100},
		{Host: "intranet.local", TotalConns: 1, RxBytes: 10, Route: "direct", LastSeenMs: 9000},
	}
	_, _ = a.opts.Log.Write([]byte("c : Connected.\n" + strings.Repeat("x", 300) + "\n"))
}

func TestConnectionPageRendersEveryTab(t *testing.T) {
	a := newTestApp(nil)
	liveSession(a)
	a.cur = pageConnection
	cp := a.pages[pageConnection].(*connectionPage)
	for _, size := range [][2]int{{140, 50}, {80, 24}, {50, 16}} {
		a.width, a.height = size[0], size[1]
		for tab := connTab(0); tab < numConnTabs; tab++ {
			cp.tab = tab
			screen := a.View()
			assertFits(t, screen, size[0], size[1])
		}
	}

	a.width, a.height = 140, 50
	cp.tab = tabSpeed
	plain := ansi.Strip(a.View())
	for _, want := range []string{"PER TUNNEL", "tokyo", "Healthy", "reconnecting", "Reconnection failed", "3.0 MB/s"} {
		if !strings.Contains(plain, want) {
			t.Errorf("speed view missing %q", want)
		}
	}
	if strings.Contains(plain, "PER PROFILE") {
		t.Error("per-profile breakdown should be gone")
	}
	cp.tab = tabConns
	plain = ansi.Strip(a.View())
	for _, want := range []string{"ACTIVE (2)", "T1", "https github.com:443", "2001:db8::1:80", "RECENTLY CLOSED", "5.6.7.8:22"} {
		if !strings.Contains(plain, want) {
			t.Errorf("connections view missing %q", want)
		}
	}
	// Newest active connection first.
	if strings.Index(plain, "2001:db8") > strings.Index(plain, "github.com") {
		t.Error("active connections are not newest-first")
	}
	cp.tab = tabDests
	plain = ansi.Strip(a.View())
	for _, want := range []string{"git, curl", "blocked", "direct"} {
		if !strings.Contains(plain, want) {
			t.Errorf("destinations view missing %q", want)
		}
	}
	if strings.Index(plain, "ads.example") > strings.Index(plain, "github.com") {
		t.Error("default sort should be total bytes, descending")
	}
	cp.update(key("o")) // speed
	cp.update(key("o")) // active
	plain = ansi.Strip(a.View())
	if strings.Index(plain, "github.com") > strings.Index(plain, "ads.example") {
		t.Error("active sort should put github.com first")
	}
	cp.tab = tabErrors
	if plain = ansi.Strip(a.View()); !strings.Contains(plain, "warning: something odd") {
		t.Error("errors view missing the error line")
	}
	cp.tab = tabLogs
	if plain = ansi.Strip(a.View()); !strings.Contains(plain, "Connected.") {
		t.Error("logs view missing log output")
	}
}

func TestConnectionPageTabKeysAndScroll(t *testing.T) {
	a := newTestApp(nil)
	liveSession(a)
	cp := a.pages[pageConnection].(*connectionPage)
	cp.update(key("right"))
	if cp.tab != tabConns {
		t.Fatalf("right → %v", cp.tab)
	}
	cp.update(key("left"))
	cp.update(key("left"))
	if cp.tab != tabErrors {
		t.Fatalf("left wraps → %v", cp.tab)
	}
	cp.update(key("g"))
	for i := 0; i < 5; i++ {
		cp.update(key("up"))
	}
	a.width, a.height = 80, 24
	a.cur = pageConnection
	a.View() // clamps the offset to the content
	if cp.scroll[tabLogs] > 2 {
		t.Fatalf("logs scroll not clamped: %d", cp.scroll[tabLogs])
	}
}

func TestConnectionPageDeployBanner(t *testing.T) {
	a := newTestApp(nil)
	a.sess = SessionState{Status: StatusConnecting, Message: "Starting tunnel…", Spec: &ConnectSpec{Profile: profile.Profile{Name: "x", Remote: "a@b"}},
		DeployReason: "first-deploy", DeploySent: 512 << 10, DeployTotal: 2 << 20}
	a.width, a.height, a.cur = 100, 30, pageConnection
	plain := ansi.Strip(a.View())
	if !strings.Contains(plain, "first time") || !strings.Contains(plain, "25%") {
		t.Fatalf("deploy banner missing:\n%s", plain)
	}
	if strings.Contains(plain, "Starting tunnel") {
		t.Error("status message should be hidden while deploy progress shows")
	}
}

func TestDiagnoseTunnel(t *testing.T) {
	cases := []struct {
		last, min, jitter int64
		want              string
	}{
		{0, 0, 0, "Waiting"},
		{100_000, 40_000, 40_000, "Congestion"},
		{50_000, 40_000, 40_000, "Unstable"},
		{130_000, 40_000, 1_000, "Bufferbloat"},
		{80_000, 40_000, 1_000, "Possible bufferbloat"},
		{170_000, 160_000, 1_000, "High latency"},
		{40_000, 38_000, 2_000, "Healthy"},
	}
	for _, c := range cases {
		got, _ := diagnoseTunnel(stats.TunnelSnapshot{LastRttUs: c.last, MinRttUs: c.min, JitterUs: c.jitter})
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("diagnose(%d,%d,%d) = %q, want %q…", c.last, c.min, c.jitter, got, c.want)
		}
	}
}
