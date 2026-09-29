package stats

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/store"
)

// RouteKind is the category of a per-destination route decision.
type RouteKind string

const (
	RouteTunnel  RouteKind = "tunnel"  // proxy through the tunnel
	RouteDirect  RouteKind = "direct"  // bypass tunnel, connect directly
	RouteBlocked RouteKind = "blocked" // reject connection
)

// NormalizeRouteKind maps any on-the-wire kind to one of the three supported
// kinds. Legacy "default" (route via the group's default profile) and any
// unknown/empty value collapse to tunnel.
func NormalizeRouteKind(s string) RouteKind {
	switch RouteKind(strings.ToLower(strings.TrimSpace(s))) {
	case RouteDirect:
		return RouteDirect
	case RouteBlocked:
		return RouteBlocked
	default:
		return RouteTunnel
	}
}

// RouteMode is the route decision for a destination. It is written as
// `{"kind":"tunnel"}`; on read it also accepts a bare string (`"direct"`) and
// the legacy `{"kind":"tunnel","profileId":"..."}` form (profileId dropped).
type RouteMode struct {
	Kind RouteKind `json:"kind"`
}

// UnmarshalJSON accepts either a bare string or an object with a `kind` field
// and normalizes the kind (see NormalizeRouteKind).
func (r *RouteMode) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		r.Kind = RouteTunnel
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		r.Kind = NormalizeRouteKind(s)
		return nil
	}
	var x struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	r.Kind = NormalizeRouteKind(x.Kind)
	return nil
}

// RouteGroup is one ordered rule group. Domains use the rule-group scope
// syntax: `=host` matches that exact host only; a bare `example.com` matches
// the apex and every subdomain. Extra fields sent by clients (id, name) are
// ignored.
type RouteGroup struct {
	Domains []string  `json:"domains"`
	Route   RouteMode `json:"route"`
}

// RouteTable is the full routing input, as POSTed to and returned by /routes.
//
// Precedence: Overrides (exact host, then most-specific `*.suffix`) > Groups
// (array order, first group with any matching domain wins) > Final. Final only
// allows tunnel/direct; blocked (or empty) is treated as tunnel.
type RouteTable struct {
	Overrides map[string]RouteMode `json:"overrides"`
	Groups    []RouteGroup         `json:"groups"`
	Final     RouteMode            `json:"final"`
}

// compiledRoutes is RouteTable indexed for O(labels) lookups.
type compiledRoutes struct {
	table     RouteTable // normalized copy, served by GET /routes
	overrides map[string]RouteMode
	exact     map[string]int // `=host` scopes → lowest group index
	suffix    map[string]int // bare-domain scopes → lowest group index
}

// normalizeHost lowercases and strips surrounding space and a trailing dot,
// matching store.NormalizeDomain's canonical form.
func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

func normalizeFinal(m RouteMode) RouteMode {
	if NormalizeRouteKind(string(m.Kind)) == RouteDirect {
		return RouteMode{Kind: RouteDirect}
	}
	return RouteMode{Kind: RouteTunnel}
}

func compileRouteTable(t RouteTable) *compiledRoutes {
	cr := &compiledRoutes{
		overrides: make(map[string]RouteMode, len(t.Overrides)),
		exact:     make(map[string]int),
		suffix:    make(map[string]int),
	}
	for k, v := range t.Overrides {
		key := normalizeHost(k)
		if key == "" {
			continue
		}
		cr.overrides[key] = RouteMode{Kind: NormalizeRouteKind(string(v.Kind))}
	}
	groups := make([]RouteGroup, 0, len(t.Groups))
	for i, g := range t.Groups {
		ng := RouteGroup{Domains: []string{}, Route: RouteMode{Kind: NormalizeRouteKind(string(g.Route.Kind))}}
		for _, d := range g.Domains {
			nd, ok := store.NormalizeDomain(d)
			if !ok {
				continue
			}
			ng.Domains = append(ng.Domains, nd)
			idx, m := i, cr.suffix
			if strings.HasPrefix(nd, "=") {
				nd, m = nd[1:], cr.exact
			}
			if _, seen := m[nd]; !seen {
				m[nd] = idx // groups are walked in order, so first write is the lowest index
			}
		}
		groups = append(groups, ng)
	}
	cr.table = RouteTable{
		Overrides: make(map[string]RouteMode, len(cr.overrides)),
		Groups:    groups,
		Final:     normalizeFinal(t.Final),
	}
	for k, v := range cr.overrides {
		cr.table.Overrides[k] = v
	}
	return cr
}

// lookup resolves the route for an (already normalized) destination key.
func (cr *compiledRoutes) lookup(key string) RouteMode {
	// 1. Per-host overrides: exact, then most specific *.suffix.
	if m, ok := cr.overrides[key]; ok {
		return m
	}
	for _, cand := range wildcardCandidates(key) {
		if m, ok := cr.overrides[cand]; ok {
			return m
		}
	}
	// 2. Rule groups: lowest-index group with any matching domain.
	best := -1
	consider := func(idx int, ok bool) {
		if ok && (best < 0 || idx < best) {
			best = idx
		}
	}
	idx, ok := cr.exact[key]
	consider(idx, ok)
	for rest := key; rest != ""; {
		idx, ok := cr.suffix[rest]
		consider(idx, ok)
		dot := strings.IndexByte(rest, '.')
		if dot < 0 {
			break
		}
		rest = rest[dot+1:]
	}
	if best >= 0 {
		return cr.table.Groups[best].Route
	}
	// 3. Final.
	return cr.table.Final
}

// LookupRouteMode returns the route decision for a destination.
func (c *Counters) LookupRouteMode(dstAddr, host string) RouteMode {
	dk := normalizeHost(destKey(dstAddr, host))
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.routes.lookup(dk)
}

// lookupRouteModeLocked resolves the route for key. Caller must hold c.mu.
func (c *Counters) lookupRouteModeLocked(key string) RouteMode {
	return c.routes.lookup(normalizeHost(key))
}

// SetRouteTable replaces the active route table.
func (c *Counters) SetRouteTable(t RouteTable) {
	cr := compileRouteTable(t)
	c.mu.Lock()
	c.routes = cr
	c.mu.Unlock()
}

// RouteTable returns a normalized copy of the active route table.
func (c *Counters) RouteTable() RouteTable {
	c.mu.Lock()
	src := c.routes.table
	c.mu.Unlock()
	out := RouteTable{
		Overrides: make(map[string]RouteMode, len(src.Overrides)),
		Groups:    make([]RouteGroup, len(src.Groups)),
		Final:     src.Final,
	}
	for k, v := range src.Overrides {
		out.Overrides[k] = v
	}
	for i, g := range src.Groups {
		out.Groups[i] = RouteGroup{Domains: append([]string{}, g.Domains...), Route: g.Route}
	}
	return out
}

// decodeRouteTable parses a /routes POST body. Unknown top-level keys are
// rejected so a stale client still sending the removed flat host→route map
// fails loudly instead of silently clearing every rule. Unknown fields inside
// groups (id, name) are ignored.
func decodeRouteTable(r io.Reader) (RouteTable, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return RouteTable{}, err
	}
	for k := range raw {
		switch k {
		case "overrides", "groups", "final":
		default:
			return RouteTable{}, fmt.Errorf("unknown field %q (expected overrides/groups/final)", k)
		}
	}
	var t RouteTable
	if v, ok := raw["overrides"]; ok {
		if err := json.Unmarshal(v, &t.Overrides); err != nil {
			return RouteTable{}, fmt.Errorf("overrides: %w", err)
		}
	}
	if v, ok := raw["groups"]; ok {
		if err := json.Unmarshal(v, &t.Groups); err != nil {
			return RouteTable{}, fmt.Errorf("groups: %w", err)
		}
	}
	if v, ok := raw["final"]; ok {
		if err := json.Unmarshal(v, &t.Final); err != nil {
			return RouteTable{}, fmt.Errorf("final: %w", err)
		}
	}
	return t, nil
}

// handleRoutes serves GET (current table) and POST (replace table) for
// destination routing. Body shape: RouteTable.
func (c *Counters) handleRoutes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(c.RouteTable())
	case http.MethodPost:
		t, err := decodeRouteTable(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.SetRouteTable(t)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(c.RouteTable())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
