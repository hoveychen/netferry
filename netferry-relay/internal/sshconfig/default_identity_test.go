package sshconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultIdentityFile(t *testing.T) {
	home := t.TempDir()
	if DefaultIdentityFile(home) != "" {
		t.Fatal("missing config should yield empty")
	}
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	cfg := "Host box\n  IdentityFile ~/.ssh/box\n\nHost *\n  User me\n  IdentityFile ~/.ssh/id_ed25519\n  IdentityFile ~/.ssh/other\n"
	_ = os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(cfg), 0o600)
	if got, want := DefaultIdentityFile(home), filepath.Join(home, ".ssh/id_ed25519"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
