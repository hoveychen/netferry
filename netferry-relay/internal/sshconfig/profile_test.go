package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoveychen/netferry/relay/internal/profile"
)

// fullPipeline runs ParseDefault on a synthetic ~/.ssh/config and then maps
// each entry through ApplyToProfile, returning a host→Profile map for easy
// assertions. Mirrors what tui_profiles.go does on import.
func fullPipeline(t *testing.T, raw string) map[string]profile.Profile {
	t.Helper()
	tmp := t.TempDir()
	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts, err := ParseDefault(tmp)
	if err != nil {
		t.Fatalf("ParseDefault: %v", err)
	}
	out := make(map[string]profile.Profile, len(hosts))
	for _, h := range hosts {
		var p profile.Profile
		ApplyToProfile(&p, h, hosts)
		out[h.Host] = p
	}
	return out
}

func TestPipeline_SimpleHostWithDefaults(t *testing.T) {
	raw := strings.Join([]string{
		"Host *",
		"  User defaultuser",
		"  IdentityFile ~/.ssh/id_default",
		"",
		"Host alpha",
		"  HostName 10.0.0.1",
		"  Port 2200",
	}, "\n")
	got := fullPipeline(t, raw)
	a, ok := got["alpha"]
	if !ok {
		t.Fatalf("alpha missing; got %v", keys(got))
	}
	// Wildcard User should propagate; HostName + Port from the entry.
	if a.Remote != "defaultuser@10.0.0.1:2200" {
		t.Fatalf("alpha.Remote = %q, want %q", a.Remote, "defaultuser@10.0.0.1:2200")
	}
	// IdentityFile should be tilde-expanded against the temp home.
	if !strings.HasSuffix(a.IdentityFile, "/.ssh/id_default") {
		t.Fatalf("alpha.IdentityFile = %q, want suffix /.ssh/id_default", a.IdentityFile)
	}
	if !a.Imported {
		t.Fatalf("alpha should be marked Imported")
	}
}

func TestPipeline_ProxyJumpChain(t *testing.T) {
	raw := strings.Join([]string{
		"Host bastion",
		"  HostName bastion.example.com",
		"  User jump",
		"  IdentityFile ~/.ssh/jump_id",
		"",
		"Host hop2",
		"  HostName 10.10.10.2",
		"",
		"Host target",
		"  HostName 10.10.20.5",
		"  User app",
		"  ProxyJump bastion,hop2",
	}, "\n")
	got := fullPipeline(t, raw)
	target := got["target"]
	if target.Remote != "app@10.10.20.5" {
		t.Fatalf("target.Remote = %q", target.Remote)
	}
	if len(target.JumpHosts) != 2 {
		t.Fatalf("want 2 jump hosts, got %d: %+v", len(target.JumpHosts), target.JumpHosts)
	}
	if target.JumpHosts[0].Remote != "jump@bastion.example.com" {
		t.Fatalf("jump[0].Remote = %q", target.JumpHosts[0].Remote)
	}
	if !strings.HasSuffix(target.JumpHosts[0].IdentityFile, "/.ssh/jump_id") {
		t.Fatalf("jump[0] should carry IdentityFile, got %q", target.JumpHosts[0].IdentityFile)
	}
	// Second hop has no User configured, so just the hostname.
	if target.JumpHosts[1].Remote != "10.10.10.2" {
		t.Fatalf("jump[1].Remote = %q", target.JumpHosts[1].Remote)
	}
}

func TestPipeline_ProxyCommandAsJump(t *testing.T) {
	raw := strings.Join([]string{
		"Host bastion",
		"  HostName bastion.example.com",
		"  User jump",
		"",
		"Host inner",
		"  HostName 10.0.0.99",
		"  ProxyCommand ssh -W %h:%p bastion",
	}, "\n")
	got := fullPipeline(t, raw)
	inner := got["inner"]
	if len(inner.JumpHosts) != 1 {
		t.Fatalf("ProxyCommand should map to 1 jump host, got %d", len(inner.JumpHosts))
	}
	if inner.JumpHosts[0].Remote != "jump@bastion.example.com" {
		t.Fatalf("jump.Remote = %q", inner.JumpHosts[0].Remote)
	}
	if inner.ExtraSSHOpts != "" {
		t.Fatalf("ExtraSSHOpts should be empty when ProxyCommand parsed as jump, got %q", inner.ExtraSSHOpts)
	}
}

func TestPipeline_ProxyCommandFallback(t *testing.T) {
	// A ProxyCommand we can't decode (no `-W %h:%p`) must fall back to
	// ExtraSSHOpts so the user keeps the directive.
	raw := strings.Join([]string{
		"Host weird",
		"  HostName 1.2.3.4",
		"  ProxyCommand /usr/bin/connect-proxy.sh %h %p",
	}, "\n")
	got := fullPipeline(t, raw)
	w := got["weird"]
	if len(w.JumpHosts) != 0 {
		t.Fatalf("opaque ProxyCommand should not be parsed as jump, got %+v", w.JumpHosts)
	}
	if !strings.Contains(w.ExtraSSHOpts, "ProxyCommand") || !strings.Contains(w.ExtraSSHOpts, "/usr/bin/connect-proxy.sh") {
		t.Fatalf("ExtraSSHOpts should preserve ProxyCommand, got %q", w.ExtraSSHOpts)
	}
}

func TestPipeline_IncludeHopsResolveAcrossFiles(t *testing.T) {
	// Verify that a ProxyJump in the main config can resolve to a Host alias
	// declared inside an Include'd file.
	tmp := t.TempDir()
	sshDir := filepath.Join(tmp, ".ssh")
	confDir := filepath.Join(sshDir, "conf.d")
	if err := os.MkdirAll(confDir, 0o700); err != nil {
		t.Fatal(err)
	}
	main := strings.Join([]string{
		"Include conf.d/*.conf",
		"",
		"Host target",
		"  HostName final.example.com",
		"  User app",
		"  ProxyJump bastion",
	}, "\n")
	included := strings.Join([]string{
		"Host bastion",
		"  HostName bastion.example.com",
		"  User jump",
		"  Port 2222",
	}, "\n")
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "00-bastion.conf"), []byte(included), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts, err := ParseDefault(tmp)
	if err != nil {
		t.Fatalf("ParseDefault: %v", err)
	}
	var target HostEntry
	for _, h := range hosts {
		if h.Host == "target" {
			target = h
			break
		}
	}
	if target.Host == "" {
		t.Fatalf("target host not found; got %+v", hosts)
	}
	var p profile.Profile
	ApplyToProfile(&p, target, hosts)
	if len(p.JumpHosts) != 1 {
		t.Fatalf("want 1 jump host, got %d", len(p.JumpHosts))
	}
	if p.JumpHosts[0].Remote != "jump@bastion.example.com:2222" {
		t.Fatalf("jump[0].Remote = %q (alias from include should be fully resolved)", p.JumpHosts[0].Remote)
	}
}

func keys(m map[string]profile.Profile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
