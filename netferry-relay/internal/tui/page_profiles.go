package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/sshconfig"
)

type profMode int

const (
	profList profMode = iota
	profEdit
	profMenu
	profPrompt
	profQR
)

// stdoutWriter receives terminal escapes (OSC 52); a var for tests.
var stdoutWriter = func(s string) { _, _ = os.Stdout.WriteString(s) }

// homeDir is the home ~ expands against (a var for tests).
var homeDir = func() string { h, _ := os.UserHomeDir(); return h }

type profilesPage struct {
	app    *App
	sel    int
	mode   profMode
	editor *profileEditor
	menu   *menu
	prompt *prompt
	qr     struct {
		name   string
		chunks []string
		page   int
	}
}

func newProfilesPage(a *App) *profilesPage { return &profilesPage{app: a} }

func (p *profilesPage) title() string { return "Profiles" }

func (p *profilesPage) capturing() bool { return p.mode != profList }

// row is one list entry: the "Connect all" card (profile == nil) or a profile.
type profRow struct{ profile *profile.Profile }

func (p *profilesPage) rows() []profRow {
	children := p.app.data.Children(p.app.data.ActiveGroup())
	var rows []profRow
	if len(children) > 1 {
		rows = append(rows, profRow{})
	}
	for i := range children {
		rows = append(rows, profRow{profile: &children[i]})
	}
	return rows
}

func (p *profilesPage) selected() *profRow {
	rows := p.rows()
	if len(rows) == 0 {
		return nil
	}
	if p.sel >= len(rows) {
		p.sel = len(rows) - 1
	}
	if p.sel < 0 {
		p.sel = 0
	}
	return &rows[p.sel]
}

func (p *profilesPage) back() tea.Cmd {
	p.mode, p.editor, p.menu, p.prompt = profList, nil, nil, nil
	return nil
}

func (p *profilesPage) hints() string {
	switch p.mode {
	case profEdit:
		return p.editor.hints()
	case profQR:
		return hints("←/→", "page", "esc", "close")
	case profMenu, profPrompt:
		return ""
	}
	a := p.app
	if a.data.ActiveGroup() == nil {
		return hints("g", "pick group", "N", "new group")
	}
	h := []string{"enter", "connect", "e", "edit", "n", "new", "i", "import", "x", "export", "D", "delete", "R", "remove from group", "g", "group…"}
	if a.session.Active() {
		h = append([]string{"d", "disconnect"}, h...)
	}
	return hints(h...)
}

// ── update ───────────────────────────────────────────────────────────────────

func (p *profilesPage) update(msg tea.Msg) tea.Cmd {
	switch p.mode {
	case profEdit:
		return p.editor.update(msg)
	case profMenu:
		if km, ok := msg.(tea.KeyMsg); ok {
			m := p.menu
			cmd, done := m.update(km)
			if done && p.menu == m && p.mode == profMenu {
				p.back()
			}
			return cmd
		}
		return nil
	case profPrompt:
		pr := p.prompt
		cmd, done := pr.update(msg)
		if done && p.prompt == pr && p.mode == profPrompt {
			p.back()
		}
		return cmd
	case profQR:
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "right", "l", "n", " ":
				if p.qr.page < len(p.qr.chunks)-1 {
					p.qr.page++
				}
			case "left", "h", "p":
				if p.qr.page > 0 {
					p.qr.page--
				}
			case "esc", "q", "enter":
				p.back()
			}
		}
		return nil
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	a := p.app
	switch km.String() {
	case "up", "k":
		if p.sel > 0 {
			p.sel--
		}
	case "down", "j":
		p.sel++
		p.selected()
	case "g":
		p.openGroupMenu()
	case "N":
		return p.createGroup()
	case "r":
		if g := a.data.ActiveGroup(); g != nil {
			p.openPrompt("Rename group", "", g.Name, func(v string) tea.Cmd {
				if v == "" {
					return a.setFlash(false, "group name cannot be empty")
				}
				return p.do(a.data.RenameActiveGroup(v), "Group renamed")
			})
		}
	case "X":
		p.confirmDeleteGroup()
	case "d":
		if a.session.Active() {
			a.disconnect()
			return a.setFlash(true, "Disconnecting…")
		}
	}
	if a.data.ActiveGroup() == nil {
		return nil
	}
	switch km.String() {
	case "n":
		p.openEditor(NewProfile(homeDir()), true)
	case "i":
		p.openImportMenu()
	}
	row := p.selected()
	if row == nil {
		return nil
	}
	switch km.String() {
	case "enter":
		return p.connect(row)
	case "e":
		if row.profile != nil {
			p.openEditor(*row.profile, false)
		}
	case "x":
		if row.profile != nil {
			return p.openExportMenu(*row.profile)
		}
	case "D":
		if row.profile != nil {
			p.confirmDeleteProfile(*row.profile)
		}
	case "R":
		if row.profile != nil {
			pr := *row.profile
			a.confirm("Remove from group?", fmt.Sprintf("Remove %q from %q? The profile itself is kept.", pr.Name, a.data.ActiveGroup().Name), func() tea.Cmd {
				return p.do(a.data.RemoveFromActiveGroup(pr.ID), "Removed from group")
			})
		}
	}
	return nil
}

// do reloads after a store write and flashes the outcome.
func (p *profilesPage) do(err error, ok string) tea.Cmd {
	p.app.reload()
	if err != nil {
		return p.app.setFlash(false, err.Error())
	}
	return p.app.setFlash(true, ok)
}

func (p *profilesPage) connect(row *profRow) tea.Cmd {
	a := p.app
	if a.session.Active() {
		if p.isConnected(row) {
			a.cur = pageConnection
			return nil
		}
		return a.setFlash(false, "a tunnel is already running — press d to disconnect first")
	}
	if row.profile == nil {
		return a.connectGroup()
	}
	return a.connectProfile(*row.profile)
}

// isConnected reports whether row is what the running session is serving.
func (p *profilesPage) isConnected(row *profRow) bool {
	s := p.app.sess.Spec
	if s == nil || !p.app.session.Active() {
		return false
	}
	if row.profile == nil {
		return s.Group != nil
	}
	return s.Group == nil && s.Profile.ID == row.profile.ID
}

func (p *profilesPage) openEditor(pr profile.Profile, isNew bool) {
	a := p.app
	e := newProfileEditor(pr, isNew)
	e.onCancel = p.back
	e.onSave = func(saved profile.Profile) tea.Cmd {
		var err error
		if isNew {
			err = a.data.AddProfile(saved)
		} else {
			err = a.data.UpdateProfile(saved)
		}
		if err != nil {
			return a.setFlash(false, err.Error())
		}
		p.back()
		msg := "Profile saved"
		if a.session.Active() && a.sess.Spec != nil && (a.sess.Spec.Profile.ID == saved.ID || a.childIndex(saved.ID) >= 0) {
			msg += " — reconnect to apply"
		}
		return p.do(nil, msg)
	}
	e.onDelete = func() tea.Cmd {
		p.confirmDeleteProfile(e.draft)
		return nil
	}
	p.editor, p.mode = e, profEdit
}

func (p *profilesPage) confirmDeleteProfile(pr profile.Profile) {
	a := p.app
	a.confirm("Delete profile?", fmt.Sprintf("Delete %q? This cannot be undone.", pr.Name), func() tea.Cmd {
		p.back()
		return p.do(a.data.DeleteProfile(pr.ID), "Profile deleted")
	})
}

func (p *profilesPage) openMenu(m *menu) { p.menu, p.mode = m, profMenu }
func (p *profilesPage) openPrompt(title, help, value string, submit func(string) tea.Cmd) {
	p.prompt, p.mode = newPrompt(title, help, value, submit), profPrompt
}

// ── groups ───────────────────────────────────────────────────────────────────

func (p *profilesPage) openGroupMenu() {
	a := p.app
	m := &menu{title: "Profile groups"}
	for i, g := range a.data.Groups {
		id := g.ID
		name := strings.TrimSpace(g.Name)
		if name == "" {
			name = "(unnamed)"
		}
		detail := fmt.Sprintf("%d profiles", len(g.ChildrenIDs))
		if active := a.data.ActiveGroup(); active != nil && active.ID == id {
			detail += " · active"
			m.sel = i
		}
		m.items = append(m.items, menuItem{label: name, detail: detail, run: func() tea.Cmd {
			p.sel = 0
			return p.do(a.data.SelectGroup(id), "Switched group")
		}})
	}
	m.items = append(m.items, menuItem{label: "+ New empty group", run: p.createGroup})
	if a.data.ActiveGroup() != nil {
		m.items = append(m.items,
			menuItem{label: "Rename current group…", run: func() tea.Cmd {
				p.openPrompt("Rename group", "", a.data.ActiveGroup().Name, func(v string) tea.Cmd {
					if v == "" {
						return a.setFlash(false, "group name cannot be empty")
					}
					return p.do(a.data.RenameActiveGroup(v), "Group renamed")
				})
				return nil
			}},
			menuItem{label: "Delete current group…", run: func() tea.Cmd { p.confirmDeleteGroup(); return nil }})
	}
	p.openMenu(m)
}

func (p *profilesPage) createGroup() tea.Cmd {
	_, err := p.app.data.CreateGroup()
	p.sel = 0
	return p.do(err, "Created a new group")
}

func (p *profilesPage) confirmDeleteGroup() {
	a := p.app
	g := a.data.ActiveGroup()
	if g == nil {
		return
	}
	a.confirm("Delete group?", fmt.Sprintf("Delete %q and the %d profile(s) in it? This cannot be undone.", g.Name, len(g.ChildrenIDs)), func() tea.Cmd {
		p.back()
		p.sel = 0
		return p.do(a.data.DeleteActiveGroup(), "Group deleted")
	})
}

// ── import / export ──────────────────────────────────────────────────────────

func (p *profilesPage) openImportMenu() {
	a := p.app
	importData := func(data string) tea.Cmd {
		pr, err := DecodeProfile(data)
		if err != nil {
			return a.setFlash(false, "import: "+err.Error())
		}
		return p.do(a.data.AddProfile(pr), fmt.Sprintf("Imported %q", pr.Name))
	}
	p.openMenu(&menu{title: "Import profile", items: []menuItem{
		{label: "From ~/.ssh/config", detail: "pick a Host entry", run: func() tea.Cmd { return p.openSSHPicker() }},
		{label: "From a .nfprofile file", run: func() tea.Cmd {
			p.openPrompt("Import .nfprofile file", "Path to the exported file", "", func(path string) tea.Cmd {
				raw, err := os.ReadFile(sshconfig.ExpandTilde(path, homeDir()))
				if err != nil {
					return a.setFlash(false, err.Error())
				}
				return importData(string(raw))
			})
			return nil
		}},
		{label: "Paste exported text", run: func() tea.Cmd {
			p.openPrompt("Import from text", "Paste the exported profile (the text copied from NetFerry)", "", importData)
			return nil
		}},
	}})
}

func (p *profilesPage) openSSHPicker() tea.Cmd {
	entries, err := sshconfig.ParseDefault(homeDir())
	if err != nil {
		return p.app.setFlash(false, err.Error())
	}
	if len(entries) == 0 {
		return p.app.setFlash(false, "no Host entries found in ~/.ssh/config")
	}
	m := &menu{title: "Import from ~/.ssh/config"}
	for _, e := range entries {
		e := e
		detail := sshconfig.BuildRemote(e)
		if e.ProxyJump != nil {
			detail += " via " + *e.ProxyJump
		}
		m.items = append(m.items, menuItem{label: e.Host, detail: detail, run: func() tea.Cmd {
			p.openEditor(ProfileFromSSH(e, entries), true)
			return nil
		}})
	}
	p.openMenu(m)
	return nil
}

var unsafeFileChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func (p *profilesPage) openExportMenu(pr profile.Profile) tea.Cmd {
	a := p.app
	if !CanShare(pr) {
		return a.setFlash(false, "export needs a key file or PEM key for every identity (including jump hosts)")
	}
	export := func() (string, error) { return ExportProfile(pr, homeDir()) }
	p.openMenu(&menu{title: "Export " + pr.Name, items: []menuItem{
		{label: "Copy to clipboard", detail: "via terminal (OSC 52)", run: func() tea.Cmd {
			data, err := export()
			if err != nil {
				return a.setFlash(false, err.Error())
			}
			stdoutWriter(osc52(data))
			return a.setFlash(true, "Copied — if your terminal supports OSC 52")
		}},
		{label: "Save to .nfprofile file", run: func() tea.Cmd {
			def := unsafeFileChars.ReplaceAllString(pr.Name, "_") + ".nfprofile"
			if wd, err := os.Getwd(); err == nil {
				def = filepath.Join(wd, def)
			}
			p.openPrompt("Export to file", "Destination path", def, func(path string) tea.Cmd {
				data, err := export()
				if err != nil {
					return a.setFlash(false, err.Error())
				}
				path = sshconfig.ExpandTilde(path, homeDir())
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					return a.setFlash(false, err.Error())
				}
				return a.setFlash(true, "Exported to "+path)
			})
			return nil
		}},
		{label: "Show QR code", detail: "scan with NetFerry mobile", run: func() tea.Cmd {
			data, err := export()
			if err != nil {
				return a.setFlash(false, err.Error())
			}
			p.qr.name, p.qr.chunks, p.qr.page = pr.Name, QRChunks(data), 0
			p.mode = profQR
			return nil
		}},
	}})
	return nil
}

// ── view ─────────────────────────────────────────────────────────────────────

func (p *profilesPage) view(width, height int) string {
	switch p.mode {
	case profEdit:
		return p.editor.view(width, height)
	case profMenu:
		return p.menu.view(width, height)
	case profPrompt:
		return p.prompt.view(width)
	case profQR:
		return p.qrView(width, height)
	}
	a := p.app
	g := a.data.ActiveGroup()
	if g == nil {
		return sSection.Render("NO PROFILE GROUP") + "\n\n" +
			sMuted.Render("Profiles live in groups. Press ") + sKey.Render("N") + sMuted.Render(" to create a group") +
			func() string {
				if len(a.data.Groups) > 0 {
					return sMuted.Render(", or ") + sKey.Render("g") + sMuted.Render(" to pick one.")
				}
				return sMuted.Render(".")
			}()
	}
	name := strings.TrimSpace(g.Name)
	if name == "" {
		name = "(unnamed)"
	}
	children := a.data.Children(g)
	head := sSection.Render("GROUP ") + sBold.Render(name) + sMuted.Render(fmt.Sprintf("  %d profile(s)", len(children))) +
		sDim.Render("   g switch · N new · r rename · X delete")
	if len(a.data.Groups) > 1 {
		head += sDim.Render(fmt.Sprintf("  (%d groups)", len(a.data.Groups)))
	}
	if len(children) == 0 {
		return head + "\n\n" + sTitle.Render("Welcome to NetFerry") + "\n" +
			sMuted.Render("Route traffic through SSH servers — encrypted, fast, any SSH host.") + "\n\n" +
			sKey.Render("n") + sMuted.Render(" create a profile   ") + sKey.Render("i") + sMuted.Render(" import (ssh config / .nfprofile)")
	}

	var rows []string
	selLine := 0
	for i, r := range p.rows() {
		if i == p.sel {
			selLine = len(rows)
		}
		rows = append(rows, p.renderRow(r, i == p.sel, width)...)
		rows = append(rows, "")
	}
	return head + "\n\n" + strings.Join(windowAround(rows, selLine, height-2), "\n")
}

func (p *profilesPage) renderRow(r profRow, selected bool, width int) []string {
	a := p.app
	marker := "  "
	if selected {
		marker = sAccent.Render("▸ ")
	}
	connected := p.isConnected(&r)
	state := ""
	if connected {
		state = "  " + sOK.Render("● "+string(a.sess.Status))
	}
	if r.profile == nil {
		n := len(a.data.Children(a.data.ActiveGroup()))
		title := sTitle.Render("⇉ Connect all") + sMuted.Render(fmt.Sprintf("  %d profiles, per-host routing", n)) + state
		return []string{marker + title, "    " + sInfo.Render("multi-profile")}
	}
	pr := r.profile
	nameS := sBold
	if selected {
		nameS = sBold.Foreground(cAccent)
	}
	var badges []string
	if pr.Imported {
		badges = append(badges, sWarn.Render("🔒 imported"))
	}
	if a.data.Settings.AutoConnectProfileID == pr.ID {
		badges = append(badges, sInfo.Render("auto-connect"))
	}
	if pr.Fectun != nil && pr.Fectun.Port != 0 {
		badges = append(badges, sAccent.Render("fectun"))
	}
	if pr.PoolSize > 1 {
		badges = append(badges, sMuted.Render(fmt.Sprintf("pool %d", pr.PoolSize)))
	}
	line1 := marker + nameS.Render(pr.Name) + state
	if len(badges) > 0 {
		line1 += "  " + strings.Join(badges, sDim.Render(" · "))
	}
	detail := pr.Remote
	if pr.Imported {
		detail = "(hidden)"
	}
	if len(pr.JumpHosts) > 0 {
		detail += sDim.Render(fmt.Sprintf(" via %d jump host(s)", len(pr.JumpHosts)))
	}
	detail += sDim.Render("  ·  " + strings.Join(pr.Subnets, ",") + "  ·  dns " + string(pr.Dns) + "  ·  " + pr.Method)
	return []string{truncate(line1, width), "    " + truncate(sMuted.Render(detail), width-4)}
}

func (p *profilesPage) qrView(width, height int) string {
	n := len(p.qr.chunks)
	head := sTitle.Render("QR export — "+p.qr.name) + sMuted.Render(fmt.Sprintf("   code %d of %d", p.qr.page+1, n))
	code, modules, err := renderQR(p.qr.chunks[p.qr.page])
	if err != nil {
		return head + "\n\n" + sErr.Render(err.Error())
	}
	if modules > width || (modules+1)/2 > height-3 {
		return head + "\n\n" + sWarn.Render(fmt.Sprintf("The terminal is too small for this QR code (needs %d×%d, have %d×%d).", modules, (modules+1)/2+3, width, height)) +
			"\n" + sMuted.Render("Enlarge the window or reduce the font size.")
	}
	return head + "\n\n" + lipgloss.PlaceHorizontal(width, lipgloss.Center, code)
}
