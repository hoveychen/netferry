package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hoveychen/netferry/relay/internal/firewall"
	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/sshconn"
)

// defaultFectun matches ProfileDetailPage DEFAULT_FECTUN.
var defaultFectun = sshconn.FectunConfig{Port: 55700, K: 20, M: 15, RateMbps: 25}

var (
	remoteRE = regexp.MustCompile(`^[^@\s]+@[^:\s]+(:\d{1,5})?$`)
	cidrRE   = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}/\d{1,2}$`)
)

const (
	identFile = "file"
	identPEM  = "pem"
	maxPool   = 16
)

// methodFeatures is overridable in tests.
var methodFeatures = firewall.ListMethodFeatures

// profileEditor is the ProfileDetailPage equivalent: a form over a draft.
type profileEditor struct {
	draft    profile.Profile
	isNew    bool
	form     *Form
	features map[string][]firewall.Feature
	banner   string
	onSave   func(profile.Profile) tea.Cmd
	onDelete func() tea.Cmd
	onCancel func() tea.Cmd
}

func newProfileEditor(p profile.Profile, isNew bool) *profileEditor {
	e := &profileEditor{draft: p, isNew: isNew, features: methodFeatures()}
	e.build("")
	return e
}

func (e *profileEditor) imported() bool { return e.draft.Imported }

// methods lists "auto" plus what the firewall layer reports, sorted.
func (e *profileEditor) methods() []Option {
	opts := []Option{{"auto", "auto"}}
	var names []string
	for m := range e.features {
		names = append(names, m)
	}
	sort.Strings(names)
	for _, m := range names {
		opts = append(opts, Option{m, m})
	}
	return opts
}

// hasFeature reports whether the selected method supports f ("auto" = union).
func (e *profileEditor) hasFeature(f firewall.Feature) bool {
	m := e.form.Field("method").Value()
	for name, feats := range e.features {
		if m != "auto" && name != m {
			continue
		}
		for _, x := range feats {
			if x == f {
				return true
			}
		}
	}
	return false
}

func identMode(key string) string {
	if strings.TrimSpace(key) != "" {
		return identPEM
	}
	return identFile
}

var identOptions = []Option{{"Key file", identFile}, {"PEM text", identPEM}}

// build (re)creates the form from e.draft and focuses key if given.
func (e *profileEditor) build(focus string) {
	p := e.draft
	notImported := func() bool { return !e.imported() }
	var fs []*Field
	add := func(f *Field) *Field { fs = append(fs, f); return f }

	f := add(textField("name", "Name", p.Name, "My server"))
	f.Section = "Connection"
	f = add(textField("remote", "Remote", p.Remote, "user@host[:port]"))
	f.Visible = notImported
	f = add(selectField("idMode", "Identity", identOptions, identMode(p.IdentityKey)))
	f.Visible = notImported
	f = add(textField("idFile", "Identity file", p.IdentityFile, "~/.ssh/id_ed25519"))
	f.Visible = func() bool { return notImported() && e.form.Field("idMode").Value() == identFile }
	f = add(areaField("idKey", "Private key (PEM)", p.IdentityKey, 4))
	f.Visible = func() bool { return notImported() && e.form.Field("idMode").Value() == identPEM }

	for i, j := range p.JumpHosts {
		i := i
		pre := fmt.Sprintf("jh%d.", i)
		f = add(textField(pre+"remote", fmt.Sprintf("Jump host %d", i+1), j.Remote, "user@bastion[:port]"))
		f.Visible = notImported
		f = add(selectField(pre+"mode", "  identity", identOptions, identMode(j.IdentityKey)))
		f.Visible = notImported
		f = add(textField(pre+"file", "  key file", j.IdentityFile, "(optional)"))
		f.Visible = func() bool { return notImported() && e.form.Field(pre+"mode").Value() == identFile }
		f = add(areaField(pre+"key", "  private key (PEM)", j.IdentityKey, 3))
		f.Visible = func() bool { return notImported() && e.form.Field(pre+"mode").Value() == identPEM }
		f = add(buttonField(pre+"del", fmt.Sprintf("Remove jump host %d", i+1), func() tea.Cmd { return e.removeJump(i) }))
		f.Visible = notImported
	}
	f = add(buttonField("jhAdd", "Add jump host", e.addJump))
	f.Visible = notImported

	f = add(textField("subnets", "Subnets", strings.Join(p.Subnets, ","), "0.0.0.0/0"))
	f.Help = "Comma-separated IPv4 CIDRs to send through the tunnel"
	dns := string(p.Dns)
	if dns == "" {
		dns = string(profile.DnsOff)
	}
	add(selectField("dns", "DNS", []Option{{"Off", "off"}, {"All", "all"}, {"Specific server", "specific"}}, dns))
	f = add(textField("dnsTarget", "DNS target", p.DnsTarget, "8.8.8.8:53"))
	f.Visible = func() bool { return e.form.Field("dns").Value() == "specific" }

	f = add(textField("exclude", "Exclude subnets", strings.Join(p.ExcludeSubnets, ","), "(none)"))
	f.Section = "Advanced"
	method := p.Method
	if method == "" {
		method = "auto"
	}
	f = add(selectField("method", "Method", e.methods(), method))
	f.Help = "Firewall redirect method; auto picks the best available"
	add(toggleField("autoNets", "Auto nets", p.AutoNets))
	f = add(toggleField("disableIpv6", "Disable IPv6", p.DisableIPv6))
	f.Visible = func() bool { return e.hasFeature(firewall.FeatureIPv6) }
	f = add(toggleField("blockUdp", "Block UDP", p.BlockUDPOrDefault()))
	f.Visible = func() bool { return e.hasFeature(firewall.FeatureBlockUDP) }
	f = add(toggleField("enableUdp", "Enable UDP", p.EnableUDP))
	f.Visible = func() bool { return e.hasFeature(firewall.FeatureUDP) }
	add(toggleField("autoExcludeLan", "Auto-exclude LAN", p.AutoExcludeLANOrDefault()))
	pool := p.PoolSize
	if pool == 0 {
		pool = 4
	}
	f = add(textField("poolSize", "Pool size", strconv.Itoa(pool), "4"))
	f.Help = fmt.Sprintf("Parallel SSH connections (1–%d)", maxPool)
	bal := p.TcpBalance
	if bal == "" {
		bal = "least-loaded"
	}
	f = add(selectField("tcpBalance", "TCP balance", []Option{{"Least loaded", "least-loaded"}, {"Round robin", "round-robin"}}, bal))
	f.Visible = func() bool { n, _ := strconv.Atoi(e.form.Field("poolSize").Text()); return n > 1 }
	add(toggleField("splitConn", "Split connections", p.SplitConn))

	fc := defaultFectun
	if p.Fectun != nil && p.Fectun.Port != 0 {
		fc = *p.Fectun
	}
	f = add(toggleField("fectun", "fectun (FEC over UDP)", p.Fectun != nil && p.Fectun.Port != 0))
	f.Help = "Carry the first SSH hop over forward-error-corrected UDP"
	fecOn := func() bool { return e.form.Field("fectun").On() }
	f = add(textField("fectunPort", "  UDP port", strconv.Itoa(fc.Port), "55700"))
	f.Visible = fecOn
	f = add(textField("fectunK", "  data shards (k)", strconv.Itoa(fc.K), "20"))
	f.Visible = fecOn
	f = add(textField("fectunM", "  parity shards (m)", strconv.Itoa(fc.M), "15"))
	f.Visible = fecOn
	f = add(textField("fectunRate", "  rate (Mbps)", strconv.FormatFloat(fc.RateMbps, 'f', -1, 64), "25"))
	f.Visible = fecOn
	f.Help = "Line rate including parity; never above the link's real capacity"

	f = add(textField("extraSsh", "Extra SSH options", p.ExtraSSHOpts, "-o Foo=bar"))
	f.Visible = notImported
	add(areaField("notes", "Notes", p.Notes, 3))

	f = add(buttonField("save", "Save  (ctrl+s)", func() tea.Cmd { return e.save() }))
	f.Section = " "
	if !e.isNew {
		add(buttonField("delete", "Delete profile", func() tea.Cmd { return e.onDelete() }))
	}
	add(buttonField("cancel", "Cancel  (esc)", func() tea.Cmd { return e.onCancel() }))

	old := e.form
	e.form = newForm(fs...)
	if old != nil {
		e.form.Errors = old.Errors
	}
	if focus != "" {
		e.form.FocusKey(focus)
	}
}

// collect reads the form back into a profile (without validating).
func (e *profileEditor) collect() profile.Profile {
	p := e.draft
	fl := e.form.Field
	p.Name = fl("name").Text()
	if !e.imported() {
		p.Remote = fl("remote").Text()
		if fl("idMode").Value() == identPEM {
			p.IdentityKey, p.IdentityFile = fl("idKey").Raw(), ""
		} else {
			p.IdentityKey, p.IdentityFile = "", fl("idFile").Text()
		}
		jumps := make([]profile.JumpHost, 0, len(p.JumpHosts))
		for i := range p.JumpHosts {
			pre := fmt.Sprintf("jh%d.", i)
			j := profile.JumpHost{Remote: fl(pre + "remote").Text()}
			if fl(pre+"mode").Value() == identPEM {
				j.IdentityKey = fl(pre + "key").Raw()
			} else {
				j.IdentityFile = fl(pre + "file").Text()
			}
			jumps = append(jumps, j)
		}
		if len(jumps) == 0 {
			jumps = nil
		}
		p.JumpHosts = jumps
		p.ExtraSSHOpts = fl("extraSsh").Text()
	}
	p.Subnets = splitList(fl("subnets").Text())
	p.Dns = profile.DnsMode(fl("dns").Value())
	p.DnsTarget = ""
	if p.Dns == profile.DnsSpecific {
		p.DnsTarget = fl("dnsTarget").Text()
	}
	p.ExcludeSubnets = splitList(fl("exclude").Text())
	p.Method = fl("method").Value()
	p.AutoNets = fl("autoNets").On()
	p.DisableIPv6 = fl("disableIpv6").On()
	p.BlockUDP = boolPtr(fl("blockUdp").On())
	p.EnableUDP = fl("enableUdp").On()
	p.AutoExcludeLAN = boolPtr(fl("autoExcludeLan").On())
	p.PoolSize, _ = strconv.Atoi(fl("poolSize").Text())
	p.TcpBalance = fl("tcpBalance").Value()
	p.SplitConn = fl("splitConn").On()
	p.Fectun = nil
	if fl("fectun").On() {
		fc := sshconn.FectunConfig{}
		fc.Port, _ = strconv.Atoi(fl("fectunPort").Text())
		fc.K, _ = strconv.Atoi(fl("fectunK").Text())
		fc.M, _ = strconv.Atoi(fl("fectunM").Text())
		fc.RateMbps, _ = strconv.ParseFloat(fl("fectunRate").Text(), 64)
		p.Fectun = &fc
	}
	p.Notes = fl("notes").Raw()
	return p
}

// splitList splits a comma-separated list, dropping blanks.
func splitList(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// validateProfile mirrors ProfileDetailPage's validation, keyed by field.
func validateProfile(p profile.Profile) map[string]string {
	errs := map[string]string{}
	if strings.TrimSpace(p.Name) == "" {
		errs["name"] = "Name is required"
	}
	if !p.Imported && !remoteRE.MatchString(strings.TrimSpace(p.Remote)) {
		errs["remote"] = "Use user@host or user@host:port"
	}
	if len(p.Subnets) == 0 {
		errs["subnets"] = "At least one subnet is required"
	} else {
		for _, s := range p.Subnets {
			if !cidrRE.MatchString(s) {
				errs["subnets"] = "Invalid subnet: " + s
				break
			}
		}
	}
	for _, s := range p.ExcludeSubnets {
		if !cidrRE.MatchString(s) {
			errs["exclude"] = "Invalid subnet: " + s
			break
		}
	}
	if p.Dns == profile.DnsSpecific && strings.TrimSpace(p.DnsTarget) == "" {
		errs["dnsTarget"] = "DNS target is required"
	}
	if p.PoolSize < 1 || p.PoolSize > maxPool {
		errs["poolSize"] = fmt.Sprintf("Must be 1–%d", maxPool)
	}
	if f := p.Fectun; f != nil {
		if f.Port < 1 || f.Port > 65535 {
			errs["fectunPort"] = "Port must be 1–65535"
		}
		if f.K < 1 || f.M < 1 || f.K+f.M > 255 {
			errs["fectunK"] = "k and m must be ≥ 1 with k + m ≤ 255"
		}
		if !(f.RateMbps > 0) {
			errs["fectunRate"] = "Rate must be greater than 0"
		}
	}
	return errs
}

func (e *profileEditor) addJump() tea.Cmd {
	e.draft = e.collect()
	e.draft.JumpHosts = append(e.draft.JumpHosts, profile.JumpHost{})
	e.build(fmt.Sprintf("jh%d.remote", len(e.draft.JumpHosts)-1))
	return nil
}

func (e *profileEditor) removeJump(i int) tea.Cmd {
	e.draft = e.collect()
	if i < len(e.draft.JumpHosts) {
		e.draft.JumpHosts = append(e.draft.JumpHosts[:i:i], e.draft.JumpHosts[i+1:]...)
	}
	if len(e.draft.JumpHosts) == 0 {
		e.draft.JumpHosts = nil
	}
	e.build("jhAdd")
	return nil
}

// fieldOrder is the order errors are focused in.
var errorFocusOrder = []string{"name", "remote", "subnets", "dnsTarget", "exclude", "poolSize", "fectunPort", "fectunK", "fectunRate"}

func (e *profileEditor) save() tea.Cmd {
	p := e.collect()
	errs := validateProfile(p)
	e.form.Errors = errs
	if len(errs) > 0 {
		e.banner = fmt.Sprintf("%d problem(s) to fix before saving", len(errs))
		for _, k := range errorFocusOrder {
			if _, ok := errs[k]; ok {
				e.form.FocusKey(k)
				break
			}
		}
		return nil
	}
	e.banner = ""
	return e.onSave(p)
}

func (e *profileEditor) update(msg tea.Msg) tea.Cmd {
	cmd, handled := e.form.Update(msg)
	if handled {
		return cmd
	}
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "ctrl+s":
			return e.save()
		case "esc":
			return e.onCancel()
		}
	}
	return cmd
}

func (e *profileEditor) view(width, height int) string {
	title := "Edit profile"
	if e.isNew {
		title = "New profile"
	}
	head := sTitle.Render(title)
	if n := e.form.Field("name").Text(); n != "" && !e.isNew {
		head += "  " + sBold.Render(n)
	}
	if e.imported() {
		head += "\n" + sWarn.Render("🔒 Imported profile: connection and credentials are locked.")
	}
	if e.banner != "" {
		head += "\n" + sErr.Render("✗ "+e.banner)
	}
	bodyH := height - strings.Count(head, "\n") - 2
	return head + "\n\n" + e.form.scrollWindow(e.form.View(width), bodyH)
}

func (e *profileEditor) hints() string {
	return hints("↑/↓", "field", "←/→/space", "change", "ctrl+s", "save", "esc", "cancel")
}
