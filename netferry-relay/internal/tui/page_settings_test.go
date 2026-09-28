package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hoveychen/netferry/relay/internal/nexttrace"
	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/store"
)

func settingsApp(t *testing.T) *App {
	t.Helper()
	isolate(t)
	if err := store.SaveProfiles([]profile.Profile{{ID: "pa", Name: "Alpha", Remote: "u@alpha.example:2222"}, {ID: "pb", Name: "Beta", Remote: "u@b"}}); err != nil {
		t.Fatal(err)
	}
	a := loadedApp(t)
	s := a.data.Settings
	s.TrayDisplayMode = "connections" // a desktop-only field the TUI must keep
	if err := store.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	a.reload()
	a.cur = pageSettings
	a.pages[pageSettings].(*settingsPage).entered()
	return a
}

func TestSettingsSaveKeepsForeignFields(t *testing.T) {
	a := settingsApp(t)
	a.opts.Version = "v9.9.9"
	s := screen(a)
	for _, want := range []string{"Auto-connect", "— None —", "SOCKS5 proxy", "HTTP proxy", "Engine version", "v9.9.9"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Port") {
		t.Fatal("port fields show while the proxies are off")
	}
	assertFits(t, a.View(), a.width, a.height)

	press(a, "right")         // auto-connect → Alpha
	press(a, "down", "space") // SOCKS5 on
	if !strings.Contains(screen(a), "1080") {
		t.Fatal("SOCKS5 should default to 1080")
	}
	press(a, "down", "down", "space") // HTTP on (8080)
	press(a, "ctrl+s")

	got, err := store.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.AutoConnectProfileID != "pa" || got.LanSocks5Port == nil || *got.LanSocks5Port != 1080 || got.LanHTTPPort == nil || *got.LanHTTPPort != 8080 {
		t.Fatalf("saved %+v", got)
	}
	if got.TrayDisplayMode != "connections" || got.ActiveGroupID != store.DefaultGroupID {
		t.Fatalf("foreign fields lost: %+v", got)
	}
	if a.data.Settings.LanSocks5Port == nil {
		t.Fatal("the app did not reload settings (next connect would miss the LAN port)")
	}

	// Turning a proxy off stores null.
	press(a, "home")
	p := a.pages[pageSettings].(*settingsPage)
	p.form.FocusKey("socks")
	press(a, "space", "ctrl+s")
	got, _ = store.LoadSettings()
	if got.LanSocks5Port != nil || got.LanHTTPPort == nil {
		t.Fatalf("after off: %+v", got)
	}
}

func TestSettingsRejectsBadPort(t *testing.T) {
	a := settingsApp(t)
	p := a.pages[pageSettings].(*settingsPage)
	p.form.FocusKey("socks")
	press(a, "space", "down")
	for i := 0; i < 4; i++ {
		a.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	typeText(a, "70000")
	press(a, "ctrl+s")
	if !strings.Contains(screen(a), "port must be 1–65535") {
		t.Fatalf("no error:\n%s", screen(a))
	}
	if got, _ := store.LoadSettings(); got.LanSocks5Port != nil {
		t.Fatal("invalid port saved")
	}
	// esc discards the edit.
	press(a, "esc")
	if p.dirty || p.form.Field("socks").On() {
		t.Fatal("esc should discard unsaved changes")
	}
}

// fakeTrace stubs nexttrace so the page runs without the network or a binary.
func fakeTrace(t *testing.T, installed bool, run func(ctx context.Context, onHop func(nexttrace.Hop)) (int, error)) *[]string {
	t.Helper()
	oldE, oldR, oldF := traceEnsure, traceRun, traceFind
	var calls []string
	traceFind = func() string {
		if installed {
			return "/fake/nexttrace"
		}
		return ""
	}
	traceEnsure = func(ctx context.Context, progress func(n, total int64)) (string, error) {
		calls = append(calls, "ensure")
		if !installed {
			progress(5, 10)
			progress(10, 10)
		}
		return "/fake/nexttrace", nil
	}
	traceRun = func(ctx context.Context, exe, host string, o nexttrace.Options, onHop func(nexttrace.Hop)) (int, error) {
		calls = append(calls, "run "+host+" "+strings.Join(nexttrace.Args(host, o), " "))
		return run(ctx, onHop)
	}
	t.Cleanup(func() { traceEnsure, traceRun, traceFind = oldE, oldR, oldF })
	return &calls
}

// pump executes cmd and feeds its messages back until the chain ends.
func pump(t *testing.T, a *App, cmd tea.Cmd) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for cmd != nil {
		done := make(chan tea.Msg, 1)
		go func(c tea.Cmd) { done <- c() }(cmd)
		select {
		case msg := <-done:
			if msg == nil {
				return
			}
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, c := range b {
					pump(t, a, c)
				}
				return
			}
			_, cmd = a.Update(msg)
		case <-deadline:
			t.Fatal("trace did not finish")
		}
	}
}

func TestDiagnosticsTraceStreamsHops(t *testing.T) {
	a := settingsApp(t)
	a.cur = pageDiagnostics
	p := a.pages[pageDiagnostics].(*diagnosticsPage)
	p.entered()
	calls := fakeTrace(t, false, func(ctx context.Context, onHop func(nexttrace.Hop)) (int, error) {
		onHop(nexttrace.Hop{TTL: 2, Timeout: true})
		onHop(nexttrace.Hop{TTL: 1, IP: "10.0.0.1", RTTMs: 1.234, HasRTT: true})
		onHop(nexttrace.Hop{TTL: 2, IP: "203.0.113.9", Hostname: "edge.example", RTTMs: 42, HasRTT: true, ASN: "64500", Country: "JP", City: "Tokyo", ISP: "ExampleNet"})
		return 0, nil
	})
	if !strings.Contains(screen(a), "First-run download: NextTrace "+nexttrace.Version) {
		t.Fatalf("no install hint:\n%s", screen(a))
	}

	press(a, "right") // fill from profile → Alpha
	if got := p.form.Field("target").Text(); got != "u@alpha.example:2222" {
		t.Fatalf("target = %q", got)
	}
	p.form.FocusKey("start")
	_, cmd := a.Update(key("enter"))
	if !p.downloading {
		t.Fatal("a missing binary should download first")
	}
	// Events keep flowing while the user looks at another page.
	a.cur = pageProfiles
	pump(t, a, cmd)
	a.cur = pageDiagnostics

	if len(*calls) != 2 || (*calls)[1] != "run alpha.example --raw --no-color -m 30 -q 1 -d LeoMoeAPI alpha.example" {
		t.Fatalf("calls = %q", *calls)
	}
	if len(p.hops) != 2 || p.hops[0].TTL != 1 || p.hops[1].IP != "203.0.113.9" {
		t.Fatalf("hops = %+v (TTL rows must be replaced and sorted)", p.hops)
	}
	s := screen(a)
	for _, want := range []string{"10.0.0.1", "1.23 ms", "203.0.113.9 edge.example", "42.0 ms", "AS64500", "JP Tokyo · ExampleNet", "Finished."} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	if p.running || p.downloading {
		t.Fatal("still running")
	}
	assertFits(t, a.View(), a.width, a.height)
}

func TestDiagnosticsStopAndErrors(t *testing.T) {
	a := settingsApp(t)
	a.cur = pageDiagnostics
	p := a.pages[pageDiagnostics].(*diagnosticsPage)
	fakeTrace(t, true, func(ctx context.Context, onHop func(nexttrace.Hop)) (int, error) {
		onHop(nexttrace.Hop{TTL: 1, IP: "10.0.0.1"})
		<-ctx.Done()
		return -1, nil
	})
	p.form.FocusKey("target")
	typeText(a, "example.com")
	_, cmd := a.Update(key("enter")) // enter in the target field starts
	if !p.running {
		t.Fatal("not running")
	}
	typeText(a, "x")
	if p.form.Field("target").Text() != "example.com" {
		t.Fatal("inputs must be frozen while tracing")
	}
	go func() { time.Sleep(50 * time.Millisecond); a.pages[pageDiagnostics].(*diagnosticsPage).stop() }()
	pump(t, a, cmd)
	if p.running || !strings.Contains(screen(a), "Stopped.") {
		t.Fatalf("after stop:\n%s", screen(a))
	}

	// A failed download surfaces its error.
	traceFind = func() string { return "" }
	traceEnsure = func(context.Context, func(int64, int64)) (string, error) { return "", errors.New("SHA-256 mismatch") }
	p.form.FocusKey("start")
	_, cmd = a.Update(key("enter"))
	pump(t, a, cmd)
	if !strings.Contains(screen(a), "download failed: SHA-256 mismatch") {
		t.Fatalf("no error:\n%s", screen(a))
	}

	// Empty target.
	p.form.Field("target").SetText("")
	_, cmd = a.Update(key("enter"))
	if cmd != nil || !strings.Contains(screen(a), "enter a target host first") {
		t.Fatal("empty target should not start")
	}
}
