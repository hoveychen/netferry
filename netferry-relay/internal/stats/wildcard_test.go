package stats

import (
	"reflect"
	"testing"
)

func TestWildcardCandidates(t *testing.T) {
	cases := []struct {
		host string
		want []string
	}{
		{"a.b.eastmoney.com", []string{"*.b.eastmoney.com", "*.eastmoney.com", "*.com"}},
		{"eastmoney.com", []string{"*.com"}},
		{"localhost", nil},
		{"", nil},
		{"*.eastmoney.com", nil}, // a wildcard pattern never generates candidates
	}
	for _, tc := range cases {
		if got := wildcardCandidates(tc.host); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("wildcardCandidates(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestLookupRouteModeWildcard(t *testing.T) {
	c := NewCounters()
	c.SetRouteModes(map[string]RouteMode{
		"*.eastmoney.com":     {Kind: RouteDirect},
		"push2.eastmoney.com": {Kind: RouteBlocked}, // exact override of the wildcard
	})

	cases := []struct {
		host string
		want RouteKind
	}{
		{"push2.eastmoney.com", RouteBlocked},        // exact wins over wildcard
		{"11.push2.eastmoney.com", RouteDirect},      // wildcard matches across sub-levels
		{"newspush.eastmoney.com", RouteDirect},      // wildcard matches one sub-level
		{"eastmoney.com", RouteTunnel},               // apex not matched by *.eastmoney.com
		{"example.org", RouteTunnel},                 // unrelated host falls back to tunnel
	}
	for _, tc := range cases {
		if got := c.LookupRouteMode("", tc.host).Kind; got != tc.want {
			t.Errorf("LookupRouteMode(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

func TestLookupRouteModeMostSpecificWildcard(t *testing.T) {
	c := NewCounters()
	c.SetRouteModes(map[string]RouteMode{
		"*.eastmoney.com":       {Kind: RouteDirect},
		"*.push2.eastmoney.com": {Kind: RouteBlocked},
	})

	// The narrower wildcard wins for hosts it covers.
	if got := c.LookupRouteMode("", "11.push2.eastmoney.com").Kind; got != RouteBlocked {
		t.Errorf("11.push2.eastmoney.com = %q, want %q", got, RouteBlocked)
	}
	// Hosts outside the narrower wildcard fall to the broader one.
	if got := c.LookupRouteMode("", "a.newspush.eastmoney.com").Kind; got != RouteDirect {
		t.Errorf("a.newspush.eastmoney.com = %q, want %q", got, RouteDirect)
	}
}

func TestLookupPriorityWildcard(t *testing.T) {
	c := NewCounters()
	c.SetPriorities(map[string]int{
		"*.eastmoney.com":     5,
		"push2.eastmoney.com": 2, // exact override
	})

	if got := c.LookupPriority("", "11.push2.eastmoney.com"); got != 5 {
		t.Errorf("11.push2.eastmoney.com priority = %d, want 5", got)
	}
	if got := c.LookupPriority("", "push2.eastmoney.com"); got != 2 {
		t.Errorf("push2.eastmoney.com priority = %d, want 2 (exact override)", got)
	}
	if got := c.LookupPriority("", "eastmoney.com"); got != DefaultPriority {
		t.Errorf("eastmoney.com priority = %d, want DefaultPriority %d", got, DefaultPriority)
	}
}
