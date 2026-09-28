package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hoveychen/netferry/relay/internal/store"
)

// Default LAN proxy ports offered when a proxy is switched on
// (GlobalSettingsPage LanProxyRow defaultPort).
const (
	defaultLanSocks5Port = 1080
	defaultLanHTTPPort   = 8080
)

// settingsPage is GlobalSettingsPage minus the desktop-only sections
// (tray, theme, language, macOS helper, updates).
type settingsPage struct {
	app    *App
	form   *Form
	dirty  bool
	banner string
}

func newSettingsPage(a *App) *settingsPage {
	p := &settingsPage{app: a}
	p.build()
	return p
}

func (p *settingsPage) title() string { return "Settings" }

func (p *settingsPage) capturing() bool { return p.form.Capturing() }

func portText(port *uint16, def int) string {
	if port != nil {
		return strconv.Itoa(int(*port))
	}
	return strconv.Itoa(def)
}

// build loads the form from the current settings.
func (p *settingsPage) build() {
	a := p.app
	s := store.GlobalSettings{}
	var opts []Option
	opts = append(opts, Option{"— None —", ""})
	if a.data != nil {
		s = a.data.Settings
		for _, pr := range a.data.Profiles {
			opts = append(opts, Option{pr.Name, pr.ID})
		}
	}
	auto := selectField("auto", "Auto-connect", opts, s.AutoConnectProfileID)
	auto.Section = "Startup"
	auto.Help = "Connect to this profile when the TUI starts."

	socks := toggleField("socks", "SOCKS5 proxy", s.LanSocks5Port != nil)
	socks.Section = "LAN sharing"
	socks.Help = "TCP and UDP. For a computer's system proxy, or proxy apps on phones."
	socksPort := textField("socksPort", "  Port", portText(s.LanSocks5Port, defaultLanSocks5Port), "")
	http := toggleField("http", "HTTP proxy", s.LanHTTPPort != nil)
	http.Help = "TCP only. Phone Wi-Fi settings only accept an HTTP proxy, so use this one there."
	httpPort := textField("httpPort", "  Port", portText(s.LanHTTPPort, defaultLanHTTPPort), "")

	p.form = newForm(auto, socks, socksPort, http, httpPort)
	socksPort.Visible = func() bool { return p.form.Field("socks").On() }
	httpPort.Visible = func() bool { return p.form.Field("http").On() }
	p.form.OnChange = func(string) { p.dirty = true }
	p.dirty, p.banner = false, ""
}

// entered refreshes from disk unless there are unsaved edits.
func (p *settingsPage) entered() tea.Cmd {
	if !p.dirty {
		p.build()
	}
	return nil
}

func parsePort(s string) (uint16, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return uint16(n), true
}

func (p *settingsPage) save() tea.Cmd {
	a := p.app
	fl := p.form.Field
	errs := map[string]string{}
	port := func(on, key string) *uint16 {
		if !fl(on).On() {
			return nil
		}
		v, ok := parsePort(fl(key).Text())
		if !ok {
			errs[key] = "port must be 1–65535"
			return nil
		}
		return &v
	}
	socks, http := port("socks", "socksPort"), port("http", "httpPort")
	p.form.Errors = errs
	if len(errs) > 0 {
		p.banner = "fix the highlighted port before saving"
		return nil
	}
	// Re-read so fields this page does not own (active group, tray mode…)
	// keep whatever the desktop last wrote.
	s, err := store.LoadSettings()
	if err != nil {
		return a.setFlash(false, err.Error())
	}
	lanChanged := !samePort(s.LanSocks5Port, socks) || !samePort(s.LanHTTPPort, http)
	s.AutoConnectProfileID = fl("auto").Value()
	s.LanSocks5Port, s.LanHTTPPort = socks, http
	if err := store.SaveSettings(s); err != nil {
		return a.setFlash(false, err.Error())
	}
	a.reload()
	p.build()
	msg := "Settings saved"
	if lanChanged && a.session.Active() {
		msg += " — LAN sharing takes effect on the next connect"
	}
	return a.setFlash(true, msg)
}

func samePort(a, b *uint16) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (p *settingsPage) update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "ctrl+s":
		return p.save()
	case "esc":
		if p.dirty {
			p.build()
			return p.app.setFlash(true, "Changes discarded")
		}
		return nil
	}
	cmd, _ := p.form.Update(msg)
	return cmd
}

func (p *settingsPage) hints() string {
	h := []string{"↑/↓", "field", "←/→/space", "change", "ctrl+s", "save"}
	if p.dirty {
		h = append(h, "esc", "discard")
	}
	return hints(h...)
}

func (p *settingsPage) view(width, height int) string {
	head := sTitle.Render("Settings")
	if p.dirty {
		head += "  " + sWarn.Render("● unsaved")
	}
	if p.banner != "" {
		head += "\n" + sErr.Render("✗ "+p.banner)
	}
	lan := sMuted.Width(width).Render("Other devices on the same network can set their proxy to this machine's IP and the port below to browse through the current tunnel, following the same routing rules. No authentication — only enable on trusted networks. Takes effect on the next connect.")
	version := p.app.opts.Version
	if version == "" {
		version = "—"
	}
	about := sSection.Render("ABOUT") + "\n" + padRight("  Engine version", formLabelWidth+2) + " " + version
	return head + "\n\n" + p.form.View(width) + "\n" + lan + "\n\n" + about
}
