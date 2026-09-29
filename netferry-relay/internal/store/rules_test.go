package store_test

import (
	"testing"

	"github.com/hoveychen/netferry/relay/internal/store"
)

func TestNormalizeDomain(t *testing.T) {
	cases := map[string]string{
		" Example.COM. ": "example.com",
		"=API.x.com":     "=api.x.com",
		"a.b.example.io": "a.b.example.io",
		"foo.local":      "foo.local",
		"com":            "",
		"localhost":      "",
		"=localhost":     "=localhost",
		"a..b.com":       "",
		"-a.com":         "",
		"a_b.com":        "",
		"":               "",
		"=":              "",
		// Private-suffix handling must match tldts {allowPrivateDomains:true}.
		"github.io":      "",
		"user.github.io": "user.github.io",
		"blogspot.com":   "",
		"co.uk":          "",
		"x.co.uk":        "x.co.uk",
	}
	for in, want := range cases {
		got, ok := store.NormalizeDomain(in)
		if want == "" {
			if ok {
				t.Errorf("NormalizeDomain(%q) = %q, want invalid", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("NormalizeDomain(%q) = %q,%v, want %q", in, got, ok, want)
		}
	}
}

func TestMatchesDomain(t *testing.T) {
	if !store.MatchesDomain("a.b.example.com", "example.com") || !store.MatchesDomain("example.com.", "example.com") {
		t.Fatal("suffix scope should match apex and subdomains")
	}
	if store.MatchesDomain("notexample.com", "example.com") {
		t.Fatal("must not match a bare suffix without a dot")
	}
	if !store.MatchesDomain("api.x.com", "=api.x.com") || store.MatchesDomain("v1.api.x.com", "=api.x.com") {
		t.Fatal("exact scope should match only that host")
	}
}

func TestRuleGroupForFirstMatch(t *testing.T) {
	groups := []store.RuleGroup{
		{ID: "broad", Domains: []string{"example.com", "bad domain"}, Route: store.RouteMode{Kind: "direct"}},
		{ID: "exact", Domains: []string{"=api.example.com"}, Route: store.RouteMode{Kind: "blocked"}},
	}
	if g := store.RuleGroupFor(groups, "api.example.com"); g == nil || g.ID != "broad" {
		t.Fatalf("first matching group should win, got %+v", g)
	}
	if g := store.RuleGroupFor(groups, "other.net"); g != nil {
		t.Fatalf("unexpected match %+v", g)
	}
}
