package stats

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func lookupKind(c *Counters, host string) RouteKind { return c.LookupRouteMode("", host).Kind }

func TestRouteOverrideBeatsGroup(t *testing.T) {
	c := NewCounters()
	c.SetRouteTable(RouteTable{
		Overrides: map[string]RouteMode{
			"api.example.com":   {Kind: RouteBlocked},
			"*.cdn.example.com": {Kind: RouteTunnel},
		},
		Groups: []RouteGroup{{Domains: []string{"example.com"}, Route: RouteMode{Kind: RouteDirect}}},
	})
	cases := map[string]RouteKind{
		"api.example.com":   RouteBlocked, // exact override
		"a.cdn.example.com": RouteTunnel,  // wildcard override
		"www.example.com":   RouteDirect,  // group
		"example.com":       RouteDirect,  // group apex
	}
	for host, want := range cases {
		if got := lookupKind(c, host); got != want {
			t.Errorf("%s = %q, want %q", host, got, want)
		}
	}
}

func TestRouteFirstGroupWins(t *testing.T) {
	c := NewCounters()
	c.SetRouteTable(RouteTable{Groups: []RouteGroup{
		{Domains: []string{"google.com"}, Route: RouteMode{Kind: RouteDirect}},
		{Domains: []string{"=mail.google.com", "maps.google.com"}, Route: RouteMode{Kind: RouteBlocked}},
		{Domains: []string{"other.org"}, Route: RouteMode{Kind: RouteBlocked}},
	}})
	// Earlier broad group beats later exact / more specific groups.
	for _, h := range []string{"mail.google.com", "maps.google.com", "x.maps.google.com", "google.com"} {
		if got := lookupKind(c, h); got != RouteDirect {
			t.Errorf("%s = %q, want direct (first group wins)", h, got)
		}
	}
	if got := lookupKind(c, "a.other.org"); got != RouteBlocked {
		t.Errorf("a.other.org = %q, want blocked", got)
	}

	// Reversed order: the exact group now comes first and wins.
	c.SetRouteTable(RouteTable{Groups: []RouteGroup{
		{Domains: []string{"=mail.google.com"}, Route: RouteMode{Kind: RouteBlocked}},
		{Domains: []string{"google.com"}, Route: RouteMode{Kind: RouteDirect}},
	}})
	if got := lookupKind(c, "mail.google.com"); got != RouteBlocked {
		t.Errorf("mail.google.com = %q, want blocked", got)
	}
	if got := lookupKind(c, "maps.google.com"); got != RouteDirect {
		t.Errorf("maps.google.com = %q, want direct", got)
	}
}

func TestRouteExactScopeSkipsSubdomains(t *testing.T) {
	c := NewCounters()
	c.SetRouteTable(RouteTable{Groups: []RouteGroup{
		{Domains: []string{"=a.b.com"}, Route: RouteMode{Kind: RouteDirect}},
	}})
	if got := lookupKind(c, "a.b.com"); got != RouteDirect {
		t.Errorf("a.b.com = %q, want direct", got)
	}
	if got := lookupKind(c, "x.a.b.com"); got != RouteTunnel {
		t.Errorf("x.a.b.com = %q, want tunnel (=host is exact only)", got)
	}
	if got := lookupKind(c, "b.com"); got != RouteTunnel {
		t.Errorf("b.com = %q, want tunnel", got)
	}
}

func TestRouteBareDomainCoversApexAndSubdomains(t *testing.T) {
	c := NewCounters()
	c.SetRouteTable(RouteTable{Groups: []RouteGroup{
		{Domains: []string{"Example.COM."}, Route: RouteMode{Kind: RouteBlocked}},
	}})
	for _, h := range []string{"example.com", "www.example.com", "a.b.example.com", "WWW.Example.com."} {
		if got := lookupKind(c, h); got != RouteBlocked {
			t.Errorf("%s = %q, want blocked", h, got)
		}
	}
	for _, h := range []string{"notexample.com", "example.org"} {
		if got := lookupKind(c, h); got != RouteTunnel {
			t.Errorf("%s = %q, want tunnel", h, got)
		}
	}
}

func TestRouteFinal(t *testing.T) {
	c := NewCounters()
	if got := lookupKind(c, "anything.com"); got != RouteTunnel {
		t.Fatalf("empty table = %q, want tunnel", got)
	}
	c.SetRouteTable(RouteTable{
		Groups: []RouteGroup{{Domains: []string{"corp.net"}, Route: RouteMode{Kind: RouteTunnel}}},
		Final:  RouteMode{Kind: RouteDirect},
	})
	if got := lookupKind(c, "anything.com"); got != RouteDirect {
		t.Errorf("unmatched = %q, want final direct", got)
	}
	if got := lookupKind(c, "git.corp.net"); got != RouteTunnel {
		t.Errorf("git.corp.net = %q, want tunnel (group)", got)
	}
	if got := c.LookupRouteMode("8.8.8.8:53", "").Kind; got != RouteDirect {
		t.Errorf("bare IP = %q, want final direct", got)
	}
	// blocked is not a valid final → tunnel.
	c.SetRouteTable(RouteTable{Final: RouteMode{Kind: RouteBlocked}})
	if got := lookupKind(c, "anything.com"); got != RouteTunnel {
		t.Errorf("final blocked = %q, want tunnel", got)
	}
}

func TestRouteModeDecodesLegacyForms(t *testing.T) {
	cases := map[string]RouteKind{
		`"default"`:                             RouteTunnel,
		`"tunnel"`:                              RouteTunnel,
		`"direct"`:                              RouteDirect,
		`"blocked"`:                             RouteBlocked,
		`""`:                                    RouteTunnel,
		`"bogus"`:                               RouteTunnel,
		`null`:                                  RouteTunnel,
		`{"kind":"default"}`:                    RouteTunnel,
		`{"kind":"tunnel","profileId":"p1"}`:    RouteTunnel,
		`{"kind":"direct","profileId":"stale"}`: RouteDirect,
		`{"kind":"blocked"}`:                    RouteBlocked,
		`{}`:                                    RouteTunnel,
	}
	for in, want := range cases {
		var m RouteMode
		if err := json.Unmarshal([]byte(in), &m); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if m.Kind != want {
			t.Errorf("%s → %q, want %q", in, m.Kind, want)
		}
	}
	out, _ := json.Marshal(RouteMode{Kind: RouteDirect})
	if string(out) != `{"kind":"direct"}` {
		t.Errorf("marshal = %s", out)
	}
}

func TestRoutesEndpoint(t *testing.T) {
	c := NewCounters()
	srv := httptest.NewServer(http.HandlerFunc(c.handleRoutes))
	defer srv.Close()

	body := `{
		"overrides": {"API.example.com": "direct", "*.foo.com": {"kind":"blocked"}, "x.com": {"kind":"tunnel","profileId":"p"}},
		"groups": [
			{"id":"g1","name":"Work","domains":["example.com","=a.b.com","not a domain"],"route":{"kind":"default"}},
			{"domains":["b.com"],"route":"direct"}
		],
		"final": "direct"
	}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("POST status %d", resp.StatusCode)
	}
	if got := lookupKind(c, "api.example.com"); got != RouteDirect {
		t.Errorf("api.example.com = %q", got)
	}
	if got := lookupKind(c, "a.b.com"); got != RouteTunnel {
		t.Errorf("a.b.com = %q, want tunnel (group 1 default→tunnel)", got)
	}
	if got := lookupKind(c, "c.b.com"); got != RouteDirect {
		t.Errorf("c.b.com = %q, want direct (group 2)", got)
	}

	resp, err = http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	want := map[string]any{
		"overrides": map[string]any{
			"api.example.com": map[string]any{"kind": "direct"},
			"*.foo.com":       map[string]any{"kind": "blocked"},
			"x.com":           map[string]any{"kind": "tunnel"},
		},
		"groups": []any{
			map[string]any{"domains": []any{"example.com", "=a.b.com"}, "route": map[string]any{"kind": "tunnel"}},
			map[string]any{"domains": []any{"b.com"}, "route": map[string]any{"kind": "direct"}},
		},
		"final": map[string]any{"kind": "direct"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GET /routes = %#v\nwant %#v", got, want)
	}

	// The removed flat map format is rejected rather than silently clearing rules.
	resp, err = http.Post(srv.URL, "application/json", strings.NewReader(`{"example.com":"direct"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("legacy flat map status = %d, want 400", resp.StatusCode)
	}
	if got := lookupKind(c, "c.b.com"); got != RouteDirect {
		t.Errorf("rejected POST must not change table; c.b.com = %q", got)
	}
}
