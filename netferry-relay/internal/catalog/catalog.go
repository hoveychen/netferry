// Package catalog carries the offline domain catalogs the desktop app uses to
// suggest semantic rule-group scopes from observed destinations, plus the
// suggestion algorithms ported from netferry-desktop/src/lib/serviceCatalog.ts
// and routingCatalog.ts.
//
// data/*.json are byte-identical copies of netferry-desktop/src/data/*.json
// (go:embed cannot reach outside the module); catalog_test.go enforces that.
package catalog

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/hoveychen/netferry/relay/internal/store"
)

//go:embed data/serviceDomains.json
var serviceJSON []byte

//go:embed data/routingDomains.json
var routingJSON []byte

type Service struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	NameZh  string   `json:"nameZh,omitempty"`
	Domains []string `json:"domains"`
}

type ServiceCatalog struct {
	Source   string    `json:"source"`
	Revision string    `json:"revision"`
	License  string    `json:"license"`
	Services []Service `json:"services"`
}

type Source struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Revision string `json:"revision"`
	License  string `json:"license"`
}

type RegionalRestriction struct {
	Domain   string   `json:"domain"`
	Product  string   `json:"product"`
	Evidence string   `json:"evidence"`
	Regions  []string `json:"regions"`
	Checked  string   `json:"checked"`
}

type Scope struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	NameZh         string   `json:"nameZh"`
	Source         string   `json:"source"`
	SourceZh       string   `json:"sourceZh,omitempty"`
	SuggestedRoute string   `json:"suggestedRoute"`
	Domains        []string `json:"domains"`
}

type RoutingCatalog struct {
	Sources              []Source              `json:"sources"`
	RegionalRestrictions []RegionalRestriction `json:"regionalRestrictions"`
	Scopes               []Scope               `json:"scopes"`
}

// index maps a normalized scope key to the normalized scope it came from.
// The first entry to claim a key wins, matching the TS Map.has checks.
type index struct {
	exact  map[string]string
	suffix map[string]string
	owner  map[string]int // key → owning entry index (service catalog only)
}

func newIndex() *index {
	return &index{exact: map[string]string{}, suffix: map[string]string{}, owner: map[string]int{}}
}

func (ix *index) add(input string, owner int) {
	scope, ok := store.NormalizeDomain(input)
	if !ok {
		return
	}
	m, key := ix.suffix, scope
	if strings.HasPrefix(scope, "=") {
		m, key = ix.exact, scope[1:]
	}
	if _, taken := m[key]; taken {
		return
	}
	m[key] = scope
	ix.owner[scope] = owner
}

// identify returns the matched scope: exact first, then the longest suffix.
func (ix *index) identify(host string) (string, bool) {
	key := strings.TrimSuffix(strings.ToLower(host), ".")
	if strings.HasPrefix(key, "*.") {
		return "", false
	}
	if s, ok := ix.exact[key]; ok {
		return s, true
	}
	labels := strings.Split(key, ".")
	for i := range labels {
		if s, ok := ix.suffix[strings.Join(labels[i:], ".")]; ok {
			return s, true
		}
	}
	return "", false
}

var (
	loadOnce     sync.Once
	services     ServiceCatalog
	routing      RoutingCatalog
	serviceIndex *index
	routingIndex []*index
)

// load parses and indexes both catalogs on first use so the tunnel CLI path,
// which never needs them, pays nothing.
func load() {
	loadOnce.Do(func() {
		if err := json.Unmarshal(serviceJSON, &services); err != nil {
			panic("catalog: parse serviceDomains.json: " + err.Error())
		}
		if err := json.Unmarshal(routingJSON, &routing); err != nil {
			panic("catalog: parse routingDomains.json: " + err.Error())
		}
		serviceIndex = newIndex()
		for i, s := range services.Services {
			for _, d := range s.Domains {
				serviceIndex.add(d, i)
			}
		}
		routingIndex = make([]*index, len(routing.Scopes))
		for i, s := range routing.Scopes {
			ix := newIndex()
			for _, d := range s.Domains {
				ix.add(d, i)
			}
			routingIndex[i] = ix
		}
	})
}

// Services returns the service catalog metadata (source, revision).
func Services() ServiceCatalog { load(); return services }

// Routing returns the routing catalog metadata.
func Routing() RoutingCatalog { load(); return routing }

type ServiceSuggestion struct {
	ID      string
	Name    string
	NameZh  string
	Domains []string
	Hosts   []string
}

func covered(host string, existing []store.RuleGroup) bool {
	return store.RuleGroupFor(existing, host) != nil
}

// SuggestServiceGroups groups hosts not yet covered by a rule group under the
// catalog service that claims them.
func SuggestServiceGroups(hosts []string, existing []store.RuleGroup) []ServiceSuggestion {
	load()
	type entry struct {
		domains map[string]bool
		hosts   []string
	}
	found := map[int]*entry{}
	for _, h := range hosts {
		if covered(h, existing) {
			continue
		}
		scope, ok := serviceIndex.identify(h)
		if !ok {
			continue
		}
		owner := serviceIndex.owner[scope]
		e := found[owner]
		if e == nil {
			e = &entry{domains: map[string]bool{}}
			found[owner] = e
		}
		e.domains[scope] = true
		e.hosts = append(e.hosts, h)
	}
	var out []ServiceSuggestion
	for i, s := range services.Services {
		e := found[i]
		if e == nil {
			continue
		}
		out = append(out, ServiceSuggestion{ID: s.ID, Name: s.Name, NameZh: s.NameZh, Domains: sortedKeys(e.domains), Hosts: e.hosts})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if len(out[a].Hosts) != len(out[b].Hosts) {
			return len(out[a].Hosts) > len(out[b].Hosts)
		}
		return out[a].Name < out[b].Name
	})
	return out
}

type Evidence struct {
	Domain  string
	Product string
	URL     string
}

type RoutingSuggestion struct {
	ID             string
	Name           string
	NameZh         string
	Source         string
	SourceZh       string
	SuggestedRoute string // "default" | "direct"
	Domains        []string
	Hosts          []string
	CoveredHosts   int
	Evidence       []Evidence
}

// SuggestRoutingScopes matches hosts against every routing scope (a host may
// hit several). Hosts already covered by a rule group still count, and are
// reported in CoveredHosts.
func SuggestRoutingScopes(hosts []string, existing []store.RuleGroup) []RoutingSuggestion {
	load()
	type entry struct {
		domains map[string]bool
		hosts   []string
		covered int
	}
	found := make([]entry, len(routing.Scopes))
	for i := range found {
		found[i].domains = map[string]bool{}
	}
	for _, h := range hosts {
		cov := covered(h, existing)
		for i, ix := range routingIndex {
			scope, ok := ix.identify(h)
			if !ok {
				continue
			}
			found[i].domains[scope] = true
			found[i].hosts = append(found[i].hosts, h)
			if cov {
				found[i].covered++
			}
		}
	}
	var out []RoutingSuggestion
	for i, s := range routing.Scopes {
		e := found[i]
		if len(e.hosts) == 0 {
			continue
		}
		var ev []Evidence
		for _, r := range routing.RegionalRestrictions {
			if e.domains[r.Domain] {
				ev = append(ev, Evidence{Domain: r.Domain, Product: r.Product, URL: r.Evidence})
			}
		}
		out = append(out, RoutingSuggestion{
			ID: s.ID, Name: s.Name, NameZh: s.NameZh, Source: s.Source, SourceZh: s.SourceZh,
			SuggestedRoute: s.SuggestedRoute, Domains: sortedKeys(e.domains), Hosts: e.hosts,
			CoveredHosts: e.covered, Evidence: ev,
		})
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
