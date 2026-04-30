package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/sshconfig"
	"github.com/hoveychen/netferry/relay/internal/store"
)

// profilesPaneMode tracks whether the user is browsing the list or editing.
type profilesPaneMode int

const (
	profileBrowse profilesPaneMode = iota
	profileEdit
	profileConfirmDelete
	profileImportPath  // entering a .nfprofile path
	profileImportSSH   // multi-select host picker
)

// profileField indexes the editable Profile fields on the edit form.
type profileField int

const (
	pfName profileField = iota
	pfRemote
	pfIdentityFile
	pfSubnets
	pfExcludeSubnets
	pfMethod
	pfDNSMode
	pfDNSTarget
	pfPoolSize
	pfTcpBalance
	pfSplitConn
	pfDisableIPv6
	pfEnableUDP
	pfBlockUDP
	pfAutoNets
	pfAutoExcludeLAN
	pfNotes
	pfFieldCount
)

func (f profileField) label() string {
	return [...]string{
		"Name",
		"Remote (user@host[:port])",
		"Identity File",
		"Subnets (comma-separated)",
		"Exclude Subnets (comma-separated)",
		"Firewall Method (auto|pf|nft|ipt|tproxy|windivert|socks5)",
		"DNS Mode (off|all|specific)",
		"DNS Target (IP[@port])",
		"Pool Size",
		"TCP Balance (round-robin|least-loaded)",
		"Split Connection (y/n)",
		"Disable IPv6 (y/n)",
		"Enable UDP Proxy (y/n)",
		"Block UDP (y/n)",
		"Auto Nets (y/n)",
		"Auto Exclude LAN (y/n)",
		"Notes",
	}[f]
}

type profilesModel struct {
	root     *rootModel
	mode     profilesPaneMode
	cursor   int    // index into root.data.profiles in browse mode, or field index in edit mode
	editing  *profile.Profile
	editBuf  [pfFieldCount]string
	deleteID string

	// .nfprofile import state
	importPath string

	// SSH config import state
	sshHosts    []sshconfig.HostEntry
	sshCursor   int
	sshSelected map[int]bool
}

func newProfilesModel(root *rootModel) profilesModel {
	return profilesModel{root: root}
}

func (p profilesModel) Init() tea.Cmd { return nil }

func (p *profilesModel) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch p.mode {
		case profileBrowse:
			return p.updateBrowse(msg)
		case profileEdit:
			return p.updateEdit(msg)
		case profileConfirmDelete:
			return p.updateConfirmDelete(msg)
		case profileImportPath:
			return p.updateImportPath(msg)
		case profileImportSSH:
			return p.updateImportSSH(msg)
		}
	}
	return nil
}

func (p *profilesModel) updateBrowse(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor+1 < len(p.root.data.profiles) {
			p.cursor++
		}
	case "n":
		// New profile.
		np := defaultNewProfile()
		p.editing = &np
		p.fillEditBufFrom(&np)
		p.mode = profileEdit
		p.cursor = 0
	case "enter", "e":
		if len(p.root.data.profiles) == 0 {
			return nil
		}
		ed := p.root.data.profiles[p.cursor]
		p.editing = &ed
		p.fillEditBufFrom(&ed)
		p.mode = profileEdit
		p.cursor = 0
	case "d":
		if len(p.root.data.profiles) == 0 {
			return nil
		}
		p.deleteID = p.root.data.profiles[p.cursor].ID
		p.mode = profileConfirmDelete
	case "i":
		// Import an encrypted .nfprofile file.
		p.importPath = ""
		p.mode = profileImportPath
	case "I":
		// Import from ~/.ssh/config — load hosts now and switch to picker.
		home, err := os.UserHomeDir()
		if err != nil {
			return p.root.setFlash(false, "home dir: "+err.Error())
		}
		hosts, err := sshconfig.ParseDefault(home)
		if err != nil {
			return p.root.setFlash(false, "ssh config: "+err.Error())
		}
		if len(hosts) == 0 {
			return p.root.setFlash(false, "no Host entries in ~/.ssh/config")
		}
		p.sshHosts = hosts
		p.sshCursor = 0
		p.sshSelected = map[int]bool{}
		p.mode = profileImportSSH
	}
	return nil
}

func (p *profilesModel) updateImportPath(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.mode = profileBrowse
		return nil
	case "enter":
		path := strings.TrimSpace(p.importPath)
		if path == "" {
			return p.root.setFlash(false, "path required")
		}
		// Allow `~/...` as a courtesy.
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = home + path[1:]
			}
		}
		imported, err := profile.LoadFile(path)
		if err != nil {
			return p.root.setFlash(false, "import: "+err.Error())
		}
		// Force a fresh ID to avoid colliding with an existing profile.
		id, err := genID()
		if err != nil {
			return p.root.setFlash(false, err.Error())
		}
		imported.ID = id
		imported.Imported = true
		if _, err := store.UpsertProfile(*imported); err != nil {
			return p.root.setFlash(false, "save: "+err.Error())
		}
		p.root.reload()
		p.mode = profileBrowse
		return p.root.setFlash(true, "imported "+imported.Name)
	case "backspace":
		if len(p.importPath) > 0 {
			p.importPath = p.importPath[:len(p.importPath)-1]
		}
	default:
		s := msg.String()
		if len(s) == 1 && s[0] >= 0x20 && s[0] < 0x7f {
			p.importPath += s
		}
	}
	return nil
}

func (p *profilesModel) updateImportSSH(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.mode = profileBrowse
		p.sshHosts = nil
		p.sshSelected = nil
		return nil
	case "up", "k":
		if p.sshCursor > 0 {
			p.sshCursor--
		}
	case "down", "j":
		if p.sshCursor+1 < len(p.sshHosts) {
			p.sshCursor++
		}
	case " ", "x":
		p.sshSelected[p.sshCursor] = !p.sshSelected[p.sshCursor]
	case "a":
		// Toggle "all selected" — if all currently selected, clear; otherwise select all.
		all := true
		for i := range p.sshHosts {
			if !p.sshSelected[i] {
				all = false
				break
			}
		}
		if all {
			p.sshSelected = map[int]bool{}
		} else {
			for i := range p.sshHosts {
				p.sshSelected[i] = true
			}
		}
	case "enter":
		picked := make([]int, 0, len(p.sshSelected))
		for i, v := range p.sshSelected {
			if v {
				picked = append(picked, i)
			}
		}
		if len(picked) == 0 {
			// Treat Enter on a single row with no selection as "import this one".
			picked = []int{p.sshCursor}
		}
		sort.Ints(picked)
		n := 0
		for _, idx := range picked {
			pr, err := sshEntryToProfile(p.sshHosts[idx], p.sshHosts)
			if err != nil {
				return p.root.setFlash(false, "convert "+p.sshHosts[idx].Host+": "+err.Error())
			}
			if _, err := store.UpsertProfile(*pr); err != nil {
				return p.root.setFlash(false, "save: "+err.Error())
			}
			n++
		}
		p.root.reload()
		p.mode = profileBrowse
		p.sshHosts = nil
		p.sshSelected = nil
		return p.root.setFlash(true, fmt.Sprintf("imported %d profile(s)", n))
	}
	return nil
}

func (p *profilesModel) updateEdit(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.mode = profileBrowse
		p.editing = nil
		p.cursor = 0
		return nil
	case "ctrl+s":
		// Commit + save.
		if cmd := p.commitEdit(); cmd != nil {
			return cmd
		}
		return nil
	case "tab", "down":
		p.cursor = (p.cursor + 1) % int(pfFieldCount)
		return nil
	case "shift+tab", "up":
		p.cursor = (p.cursor + int(pfFieldCount) - 1) % int(pfFieldCount)
		return nil
	case "backspace":
		idx := profileField(p.cursor)
		if len(p.editBuf[idx]) > 0 {
			p.editBuf[idx] = p.editBuf[idx][:len(p.editBuf[idx])-1]
		}
		return nil
	}
	// Plain rune input → append to current field.
	s := msg.String()
	if len(s) == 1 && s[0] >= 0x20 && s[0] < 0x7f {
		p.editBuf[p.cursor] += s
	}
	return nil
}

func (p *profilesModel) updateConfirmDelete(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y":
		ps, err := store.RemoveProfile(p.deleteID)
		p.mode = profileBrowse
		if err != nil {
			return p.root.setFlash(false, "delete: "+err.Error())
		}
		p.root.data.profiles = ps
		if p.cursor >= len(ps) {
			p.cursor = max(0, len(ps)-1)
		}
		return p.root.setFlash(true, "profile deleted")
	case "n", "N", "esc":
		p.mode = profileBrowse
	}
	return nil
}

func (p *profilesModel) commitEdit() tea.Cmd {
	updated, err := p.profileFromBuf()
	if err != nil {
		return p.root.setFlash(false, err.Error())
	}
	if _, err := store.UpsertProfile(*updated); err != nil {
		return p.root.setFlash(false, "save: "+err.Error())
	}
	p.root.reload()
	p.mode = profileBrowse
	p.editing = nil
	p.cursor = 0
	return p.root.setFlash(true, "profile saved")
}

func (p *profilesModel) profileFromBuf() (*profile.Profile, error) {
	out := *p.editing
	get := func(f profileField) string { return strings.TrimSpace(p.editBuf[f]) }
	parseList := func(s string) []string {
		if s == "" {
			return []string{}
		}
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, x := range parts {
			x = strings.TrimSpace(x)
			if x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	parseBool := func(label, s string) (bool, error) {
		s = strings.ToLower(s)
		switch s {
		case "y", "yes", "true", "1", "on":
			return true, nil
		case "n", "no", "false", "0", "off", "":
			return false, nil
		}
		return false, fmt.Errorf("%s: expected y/n, got %q", label, s)
	}

	out.Name = get(pfName)
	if out.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	out.Remote = get(pfRemote)
	if out.Remote == "" {
		return nil, fmt.Errorf("remote is required")
	}
	out.IdentityFile = get(pfIdentityFile)
	out.Subnets = parseList(get(pfSubnets))
	out.ExcludeSubnets = parseList(get(pfExcludeSubnets))
	out.Method = get(pfMethod)
	if out.Method == "" {
		out.Method = "auto"
	}
	dns := strings.ToLower(get(pfDNSMode))
	switch dns {
	case "", "off":
		out.Dns = profile.DnsOff
	case "all":
		out.Dns = profile.DnsAll
	case "specific":
		out.Dns = profile.DnsSpecific
	default:
		return nil, fmt.Errorf("dns mode: expected off|all|specific, got %q", dns)
	}
	out.DnsTarget = get(pfDNSTarget)
	pool := get(pfPoolSize)
	if pool != "" {
		n, err := strconv.Atoi(pool)
		if err != nil {
			return nil, fmt.Errorf("pool size: %w", err)
		}
		out.PoolSize = n
	}
	bal := strings.ToLower(get(pfTcpBalance))
	if bal != "" && bal != "round-robin" && bal != "least-loaded" {
		return nil, fmt.Errorf("tcp balance: expected round-robin|least-loaded")
	}
	out.TcpBalance = bal

	var err error
	if out.SplitConn, err = parseBool("split", get(pfSplitConn)); err != nil {
		return nil, err
	}
	if out.DisableIPv6, err = parseBool("disable IPv6", get(pfDisableIPv6)); err != nil {
		return nil, err
	}
	if out.EnableUDP, err = parseBool("enable UDP", get(pfEnableUDP)); err != nil {
		return nil, err
	}
	bUDP, err := parseBool("block UDP", get(pfBlockUDP))
	if err != nil {
		return nil, err
	}
	out.BlockUDP = &bUDP
	if out.AutoNets, err = parseBool("auto nets", get(pfAutoNets)); err != nil {
		return nil, err
	}
	autoLAN, err := parseBool("auto exclude LAN", get(pfAutoExcludeLAN))
	if err != nil {
		return nil, err
	}
	out.AutoExcludeLAN = &autoLAN
	out.Notes = get(pfNotes)

	if out.ID == "" {
		id, err := genID()
		if err != nil {
			return nil, err
		}
		out.ID = id
	}
	return &out, nil
}

func (p *profilesModel) fillEditBufFrom(src *profile.Profile) {
	bs := func(b bool) string {
		if b {
			return "y"
		}
		return "n"
	}
	p.editBuf[pfName] = src.Name
	p.editBuf[pfRemote] = src.Remote
	p.editBuf[pfIdentityFile] = src.IdentityFile
	p.editBuf[pfSubnets] = strings.Join(src.Subnets, ", ")
	p.editBuf[pfExcludeSubnets] = strings.Join(src.ExcludeSubnets, ", ")
	p.editBuf[pfMethod] = src.Method
	if src.Dns == "" {
		p.editBuf[pfDNSMode] = "off"
	} else {
		p.editBuf[pfDNSMode] = string(src.Dns)
	}
	p.editBuf[pfDNSTarget] = src.DnsTarget
	if src.PoolSize > 0 {
		p.editBuf[pfPoolSize] = strconv.Itoa(src.PoolSize)
	} else {
		p.editBuf[pfPoolSize] = "4"
	}
	p.editBuf[pfTcpBalance] = src.TcpBalance
	p.editBuf[pfSplitConn] = bs(src.SplitConn)
	p.editBuf[pfDisableIPv6] = bs(src.DisableIPv6)
	p.editBuf[pfEnableUDP] = bs(src.EnableUDP)
	p.editBuf[pfBlockUDP] = bs(src.BlockUDPOrDefault())
	p.editBuf[pfAutoNets] = bs(src.AutoNets)
	p.editBuf[pfAutoExcludeLAN] = bs(src.AutoExcludeLANOrDefault())
	p.editBuf[pfNotes] = src.Notes
}

// sshEntryToProfile converts an SSH config Host entry to a fresh NetFerry
// Profile. Defaults come from defaultNewProfile; SSH-derived fields are
// applied via sshconfig.ApplyToProfile.
func sshEntryToProfile(e sshconfig.HostEntry, all []sshconfig.HostEntry) (*profile.Profile, error) {
	id, err := genID()
	if err != nil {
		return nil, err
	}
	pr := defaultNewProfile()
	pr.ID = id
	sshconfig.ApplyToProfile(&pr, e, all)
	return &pr, nil
}

func defaultNewProfile() profile.Profile {
	t := true
	return profile.Profile{
		Name:           "New Profile",
		Subnets:        []string{"0.0.0.0/0"},
		Dns:            profile.DnsAll,
		Method:         "auto",
		PoolSize:       4,
		TcpBalance:     "least-loaded",
		BlockUDP:       &t,
		AutoExcludeLAN: &t,
	}
}

func genID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ── view ─────────────────────────────────────────────────────────────────────

var (
	listSelected = lipgloss.NewStyle().
			Background(lipgloss.Color("63")).
			Foreground(lipgloss.Color("230"))
	dimText = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

func (p *profilesModel) view(width, height int) string {
	switch p.mode {
	case profileEdit:
		return p.viewEdit(width, height)
	case profileConfirmDelete:
		return p.viewConfirmDelete(width, height)
	case profileImportPath:
		return p.viewImportPath(width, height)
	case profileImportSSH:
		return p.viewImportSSH(width, height)
	default:
		return p.viewBrowse(width, height)
	}
}

func (p *profilesModel) viewBrowse(width, height int) string {
	var b strings.Builder
	if len(p.root.data.profiles) == 0 {
		b.WriteString(dimText.Render("(no profiles — press [n] to create one)"))
		b.WriteByte('\n')
		b.WriteByte('\n')
		b.WriteString(dimText.Render("[n] new  [i] import .nfprofile  [I] import ~/.ssh/config"))
		return b.String()
	}
	listHeight := height - 2 // leave 2 lines for blank + footer
	if listHeight < 3 {
		listHeight = 3
	}
	start, end := windowedRange(len(p.root.data.profiles), p.cursor, listHeight)
	if start > 0 {
		b.WriteString(dimText.Render(fmt.Sprintf("  ↑ %d more above", start)))
		b.WriteByte('\n')
	}
	for i := start; i < end; i++ {
		pr := p.root.data.profiles[i]
		row := fmt.Sprintf("%-30s  %s", pr.Name, dimText.Render(pr.Remote))
		if i == p.cursor {
			row = listSelected.Render(row)
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
	if end < len(p.root.data.profiles) {
		b.WriteString(dimText.Render(fmt.Sprintf("  ↓ %d more below", len(p.root.data.profiles)-end)))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(dimText.Render("[↑/↓] move  [Enter/e] edit  [n] new  [d] delete  [i] import .nfprofile  [I] import ~/.ssh/config"))
	return b.String()
}

func (p *profilesModel) viewImportPath(width, height int) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Import .nfprofile"))
	b.WriteByte('\n')
	b.WriteByte('\n')
	cursor := lipgloss.NewStyle().Reverse(true).Render(" ")
	b.WriteString("Path: ")
	b.WriteString(listSelected.Render(p.importPath) + cursor)
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString(dimText.Render("Tip: ~/ is expanded.  [Enter] import  [Esc] cancel"))
	return b.String()
}

func (p *profilesModel) viewImportSSH(width, height int) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Import from ~/.ssh/config (%d hosts)", len(p.sshHosts))))
	b.WriteByte('\n')
	b.WriteByte('\n')
	for i, h := range p.sshHosts {
		mark := "[ ]"
		if p.sshSelected[i] {
			mark = "[x]"
		}
		remote := sshconfig.BuildRemote(h)
		row := fmt.Sprintf("%s %-25s  %s", mark, h.Host, dimText.Render(remote))
		if i == p.sshCursor {
			row = "▶ " + listSelected.Render(row)
		} else {
			row = "  " + row
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(dimText.Render("[↑/↓] move  [Space/x] toggle  [a] toggle all  [Enter] import selection  [Esc] cancel"))
	return b.String()
}

func (p *profilesModel) viewEdit(width, height int) string {
	var b strings.Builder
	title := "Edit Profile"
	if p.editing != nil && p.editing.ID == "" {
		title = "New Profile"
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(title))
	b.WriteByte('\n')
	b.WriteByte('\n')
	for f := profileField(0); f < pfFieldCount; f++ {
		label := f.label()
		val := p.editBuf[f]
		row := fmt.Sprintf("%-50s %s", label+":", val)
		if int(f) == p.cursor {
			row = "▶ " + listSelected.Render(row)
		} else {
			row = "  " + row
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(dimText.Render("[Tab/↓↑] field  [type to edit]  [Backspace] delete  [Ctrl+S] save  [Esc] cancel"))
	return b.String()
}

func (p *profilesModel) viewConfirmDelete(width, height int) string {
	var name string
	for _, pr := range p.root.data.profiles {
		if pr.ID == p.deleteID {
			name = pr.Name
			break
		}
	}
	return fmt.Sprintf("Delete profile %q? [y/N]", name)
}
