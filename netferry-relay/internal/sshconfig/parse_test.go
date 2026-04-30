package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_WildcardDefaultsAndExpansion(t *testing.T) {
	raw := strings.Join([]string{
		"# comment",
		"Host *",
		"  IdentityFile ~/.ssh/default_id",
		"  User defaultuser",
		"",
		"Host alpha",
		"  HostName alpha.example.com",
		"  Port 2222",
		"",
		"Host beta",
		"  HostName beta.example.com",
		"  User betauser",
		"  ProxyJump alpha",
	}, "\n")

	got, err := Parse(raw, "/home/test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}

	a := got[0]
	if a.Host != "alpha" {
		t.Fatalf("first entry host = %q", a.Host)
	}
	if a.User == nil || *a.User != "defaultuser" {
		t.Fatalf("alpha.User = %v, want default 'defaultuser'", a.User)
	}
	if a.IdentityFile == nil || *a.IdentityFile != "/home/test/.ssh/default_id" {
		t.Fatalf("alpha.IdentityFile = %v, want expanded default", a.IdentityFile)
	}
	if a.Port == nil || *a.Port != 2222 {
		t.Fatalf("alpha.Port = %v, want 2222", a.Port)
	}

	b := got[1]
	if b.Host != "beta" || b.User == nil || *b.User != "betauser" {
		t.Fatalf("beta override failed: host=%q user=%v", b.Host, b.User)
	}
	if b.ProxyJump == nil || *b.ProxyJump != "alpha" {
		t.Fatalf("beta.ProxyJump = %v", b.ProxyJump)
	}
	if b.IdentityFile == nil || *b.IdentityFile != "/home/test/.ssh/default_id" {
		t.Fatalf("beta should inherit wildcard IdentityFile, got %v", b.IdentityFile)
	}
}

func TestParse_MatchBlockIsSkipped(t *testing.T) {
	raw := strings.Join([]string{
		"Host alpha",
		"  HostName alpha.example.com",
		"  User alphauser",
		"",
		"Match user betauser",
		"  HostName overridden.example.com",
		"  IdentityFile ~/.ssh/should_not_apply",
		"",
		"Host beta",
		"  HostName beta.example.com",
	}, "\n")

	got, err := Parse(raw, "/home/test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries (alpha, beta), got %d", len(got))
	}
	for _, e := range got {
		// Ensure neither Host inherited the IdentityFile from inside the Match
		// block (which would prove Match contents were absorbed).
		if e.IdentityFile != nil && strings.Contains(*e.IdentityFile, "should_not_apply") {
			t.Fatalf("%s leaked IdentityFile from Match block: %q", e.Host, *e.IdentityFile)
		}
	}
	if got[0].Host != "alpha" || got[0].HostName == nil || *got[0].HostName != "alpha.example.com" {
		t.Fatalf("alpha entry corrupted: %+v", got[0])
	}
	if got[1].Host != "beta" || got[1].HostName == nil || *got[1].HostName != "beta.example.com" {
		t.Fatalf("beta entry corrupted: %+v", got[1])
	}
}

func TestParseDefault_IncludeExpansion(t *testing.T) {
	tmp := t.TempDir()
	sshDir := filepath.Join(tmp, ".ssh")
	confDir := filepath.Join(sshDir, "conf.d")
	if err := os.MkdirAll(confDir, 0o700); err != nil {
		t.Fatal(err)
	}
	main := strings.Join([]string{
		"Host alpha",
		"  HostName alpha.example.com",
		"",
		"Include conf.d/*.conf",
		"",
		"Host gamma",
		"  HostName gamma.example.com",
	}, "\n")
	included := strings.Join([]string{
		"Host beta",
		"  HostName beta.example.com",
		"  User betauser",
	}, "\n")
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "10-beta.conf"), []byte(included), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ParseDefault(tmp)
	if err != nil {
		t.Fatalf("ParseDefault: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 entries (alpha, beta, gamma), got %d: %+v", len(got), got)
	}
	hosts := []string{got[0].Host, got[1].Host, got[2].Host}
	want := []string{"alpha", "beta", "gamma"}
	for i := range want {
		if hosts[i] != want[i] {
			t.Fatalf("host[%d] = %q, want %q (full: %v)", i, hosts[i], want[i], hosts)
		}
	}
	if got[1].User == nil || *got[1].User != "betauser" {
		t.Fatalf("included beta entry lost User field: %+v", got[1])
	}
}

func TestParseDefault_IncludeCycleSafe(t *testing.T) {
	tmp := t.TempDir()
	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// config -> a.conf -> b.conf -> a.conf (cycle)
	main := "Host alpha\n  HostName alpha.example.com\nInclude a.conf\n"
	a := "Host beta\n  HostName beta.example.com\nInclude b.conf\n"
	b := "Host gamma\n  HostName gamma.example.com\nInclude a.conf\n"
	for name, body := range map[string]string{"config": main, "a.conf": a, "b.conf": b} {
		if err := os.WriteFile(filepath.Join(sshDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ParseDefault(tmp)
	if err != nil {
		t.Fatalf("ParseDefault: %v", err)
	}
	hosts := make([]string, len(got))
	for i, e := range got {
		hosts[i] = e.Host
	}
	want := []string{"alpha", "beta", "gamma"}
	if strings.Join(hosts, ",") != strings.Join(want, ",") {
		t.Fatalf("hosts = %v, want %v (cycle should be broken without infinite recursion)", hosts, want)
	}
}

func TestIsWildcardHost(t *testing.T) {
	cases := map[string]bool{
		"*":          true,
		"*.example":  true,
		"foo bar":    false,
		"alpha":      false,
		"a* b*":      true,
		"!bad *":     true, // ! patterns are excluded; remaining must be wildcard
		"alpha *":    false,
	}
	for in, want := range cases {
		if got := isWildcardHost(in); got != want {
			t.Errorf("isWildcardHost(%q) = %v, want %v", in, got, want)
		}
	}
}
