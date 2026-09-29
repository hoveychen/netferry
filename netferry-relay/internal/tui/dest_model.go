package tui

import (
	"net"
	"sort"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/catalog"
	"github.com/hoveychen/netferry/relay/internal/stats"
	"github.com/hoveychen/netferry/relay/internal/store"
)

// Priority levels as DestinationsPage PRIORITY_META labels them.
var priorityLabels = map[int]string{1: "Low", 2: "Low+", 3: "Norm", 4: "High", 5: "Crit"}

// wildcardCandidates lists the wildcard rule keys that could match host, most
// specific first (DestinationsPage wildcardCandidates / stats.go).
func wildcardCandidates(host string) []string {
	if host == "" || strings.HasPrefix(host, "*.") {
		return nil
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return nil
	}
	out := make([]string, 0, len(labels)-1)
	for i := 1; i < len(labels); i++ {
		out = append(out, "*."+strings.Join(labels[i:], "."))
	}
	return out
}

// resolveOverride is a host's per-host override: exact rule, else the most
// specific wildcard; via names the wildcard that matched. ok is false when no
// override applies.
func resolveOverride(host string, rules map[string]store.RouteMode) (mode store.RouteMode, via string, ok bool) {
	if m, ok := rules[host]; ok {
		return m, "", true
	}
	for _, c := range wildcardCandidates(host) {
		if m, ok := rules[c]; ok {
			return m, c, true
		}
	}
	return store.RouteMode{}, "", false
}

// resolvePriority mirrors resolveRoute for the global priority map.
func resolvePriority(host string, prios map[string]int) int {
	if v, ok := prios[host]; ok {
		return v
	}
	for _, c := range wildcardCandidates(host) {
		if v, ok := prios[c]; ok {
			return v
		}
	}
	return stats.DefaultPriority
}

// routeKey / parseRouteKey map a route to and from its option value; legacy
// kinds (e.g. the catalog's "default") collapse to tunnel.
func routeKey(m store.RouteMode) string { return store.NormalizeRouteKind(m.Kind) }

func parseRouteKey(k string) store.RouteMode {
	return store.RouteMode{Kind: store.NormalizeRouteKind(k)}
}

// isIPHost matches tldts parse().isIp for the hosts the tunnel reports.
func isIPHost(h string) bool {
	return net.ParseIP(strings.Trim(h, "[]")) != nil
}

// groupMatcher answers "which rule group covers host" without re-normalizing
// every scope per lookup (the page asks this for every host on every render).
type groupMatcher struct {
	groups []store.RuleGroup
	scopes [][]string // normalized per group
}

func newGroupMatcher(groups []store.RuleGroup) *groupMatcher {
	m := &groupMatcher{groups: groups, scopes: make([][]string, len(groups))}
	for i, g := range groups {
		for _, d := range g.Domains {
			if n, ok := store.NormalizeDomain(d); ok {
				m.scopes[i] = append(m.scopes[i], n)
			}
		}
	}
	return m
}

func scopeMatches(host, scope string) bool {
	key := strings.TrimSuffix(strings.ToLower(host), ".")
	if v, ok := strings.CutPrefix(scope, "="); ok {
		return key == v
	}
	return key == scope || strings.HasSuffix(key, "."+scope)
}

// group returns the index of the first rule group covering host, or -1.
func (m *groupMatcher) group(host string) int {
	for i, scopes := range m.scopes {
		for _, s := range scopes {
			if scopeMatches(host, s) {
				return i
			}
		}
	}
	return -1
}

// inGroup reports whether host falls in rule group i.
func (m *groupMatcher) inGroup(i int, host string) bool {
	for _, s := range m.scopes[i] {
		if scopeMatches(host, s) {
			return true
		}
	}
	return false
}

type siteGroup struct {
	site  string
	hosts []string
}

// destModel is everything the Destinations page derives from the store and
// the live snapshot; rebuilt only when either changes.
type destModel struct {
	group      *store.Group
	ruleGroups []store.RuleGroup
	matcher    *groupMatcher
	priorities map[string]int
	rules      map[string]store.RouteMode // per-host overrides (group.rules)
	final      store.RouteMode            // fallback when nothing matches
	hosts      []string                   // sorted union
	known      map[string]bool
	live       map[string]stats.DestinationSnapshot
	sites      []siteGroup // unclassified, by registrable domain
	addresses  []string    // unclassified IPs
	services   []catalog.ServiceSuggestion
	routing    []catalog.RoutingSuggestion
}

func buildDestModel(d *Data, live []stats.DestinationSnapshot) *destModel {
	m := &destModel{priorities: d.Priorities, live: map[string]stats.DestinationSnapshot{}, known: map[string]bool{}}
	g := d.ActiveGroup()
	m.group = g
	m.final = store.RouteMode{Kind: store.RouteTunnel}
	if g != nil {
		m.ruleGroups = g.RuleGroups
		m.rules = g.Rules
		m.final = store.NormalizeFinalRoute(g.FinalRoute)
	}
	if m.rules == nil {
		m.rules = map[string]store.RouteMode{}
	}
	m.matcher = newGroupMatcher(m.ruleGroups)

	add := func(h string) {
		if h != "" {
			m.known[h] = true
		}
	}
	for h := range m.priorities {
		add(h)
	}
	for h := range m.rules {
		add(h)
	}
	for _, s := range live {
		m.live[s.Host] = s
		add(s.Host)
	}
	if g != nil {
		for _, h := range g.KnownHosts {
			add(h)
		}
	}
	for h := range m.known {
		m.hosts = append(m.hosts, h)
	}
	sort.Strings(m.hosts)

	bySite := map[string][]string{}
	for _, h := range m.hosts {
		if m.matcher.group(h) >= 0 {
			continue
		}
		if isIPHost(h) {
			m.addresses = append(m.addresses, h)
			continue
		}
		site := store.RegistrableDomain(h)
		if site == "" {
			site = h
		}
		bySite[site] = append(bySite[site], h)
	}
	for site, hosts := range bySite {
		m.sites = append(m.sites, siteGroup{site, hosts})
	}
	sort.Slice(m.sites, func(i, j int) bool {
		if len(m.sites[i].hosts) != len(m.sites[j].hosts) {
			return len(m.sites[i].hosts) > len(m.sites[j].hosts)
		}
		return m.sites[i].site < m.sites[j].site
	})
	m.services = catalog.SuggestServiceGroups(m.hosts, m.ruleGroups)
	m.routing = catalog.SuggestRoutingScopes(m.hosts, m.ruleGroups)
	return m
}

// route is host's effective route with the tunnel's precedence (stats
// routes.go): per-host override (via names a matching wildcard), then the
// first matching rule group, then the fallback.
func (m *destModel) route(host string) (mode store.RouteMode, via string) {
	if r, via, ok := resolveOverride(host, m.rules); ok {
		return parseRouteKey(r.Kind), via
	}
	if gi := m.matcher.group(host); gi >= 0 {
		return parseRouteKey(m.ruleGroups[gi].Route.Kind), ""
	}
	return m.final, ""
}

// groupHosts lists the known hosts covered by rule group i.
func (m *destModel) groupHosts(i int) []string {
	var out []string
	for _, h := range m.hosts {
		if m.matcher.inGroup(i, h) {
			out = append(out, h)
		}
	}
	return out
}

// wildcardSuggestion nudges a typed host toward the wildcard of its parent
// ("Create *.suffix"), or "" when there is nothing useful to suggest.
func wildcardSuggestion(query string, known map[string]bool) string {
	if query == "" || strings.HasPrefix(query, "*.") {
		return ""
	}
	labels := strings.Split(query, ".")
	if len(labels) < 2 {
		return ""
	}
	suffix := query
	if len(labels) >= 3 {
		suffix = strings.Join(labels[1:], ".")
	}
	c := "*." + suffix
	if c == query || known[c] {
		return ""
	}
	return c
}

// splitDomains splits the editor's domain text on newlines and commas.
func splitDomains(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' }) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// normalizeDomains returns the unique normalized scopes and the inputs that
// failed validation.
func normalizeDomains(inputs []string) (valid, invalid []string) {
	seen := map[string]bool{}
	for _, in := range inputs {
		n, ok := store.NormalizeDomain(in)
		if !ok {
			invalid = append(invalid, in)
			continue
		}
		if !seen[n] {
			seen[n] = true
			valid = append(valid, n)
		}
	}
	return valid, invalid
}

// mergeDomains unions two scope lists, keeping first-seen order.
func mergeDomains(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{a, b} {
		for _, d := range list {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}
