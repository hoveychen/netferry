package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoveychen/netferry/relay/internal/store"
)

// The embedded catalogs must stay byte-identical to the desktop's copies so
// the TUI and desktop make the same suggestions.
func TestCatalogMatchesDesktopCopy(t *testing.T) {
	for _, name := range []string{"serviceDomains.json", "routingDomains.json"} {
		desktop, err := os.ReadFile(filepath.Join("..", "..", "..", "netferry-desktop", "src", "data", name))
		if err != nil {
			t.Fatalf("read desktop %s: %v", name, err)
		}
		ours, err := os.ReadFile(filepath.Join("data", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(desktop, ours) {
			t.Errorf("internal/catalog/data/%s differs from netferry-desktop/src/data/%s — copy it over", name, name)
		}
	}
}

func TestSuggestServiceGroups(t *testing.T) {
	hosts := []string{"r1---sn-a.googlevideo.com", "yt3.googleusercontent.com", "www.google.com", "unknown.example"}
	got := SuggestServiceGroups(hosts, nil)
	byID := map[string]ServiceSuggestion{}
	for _, s := range got {
		byID[s.ID] = s
	}
	yt, ok := byID["youtube"]
	if !ok || len(yt.Hosts) != 2 {
		t.Fatalf("youtube suggestion: %+v (all: %+v)", yt, got)
	}
	// A host already covered by a rule group is not suggested again.
	existing := []store.RuleGroup{{ID: "g", Domains: []string{"googlevideo.com"}}}
	for _, s := range SuggestServiceGroups(hosts, existing) {
		for _, h := range s.Hosts {
			if h == "r1---sn-a.googlevideo.com" {
				t.Fatalf("covered host re-suggested in %+v", s)
			}
		}
	}
}

func TestSuggestRoutingScopesCountsCovered(t *testing.T) {
	hosts := []string{"www.baidu.com", "www.google.com"}
	existing := []store.RuleGroup{{ID: "g", Domains: []string{"baidu.com"}}}
	var found bool
	for _, s := range SuggestRoutingScopes(hosts, existing) {
		if s.ID == "mainland-access" {
			found = true
			if s.CoveredHosts != 1 || s.SuggestedRoute != "direct" {
				t.Fatalf("mainland-access: %+v", s)
			}
		}
	}
	if !found {
		t.Fatal("baidu.com should hit mainland-access")
	}
}
