package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultIdentityFile returns the first IdentityFile declared under a
// wildcard `Host *` block of ~/.ssh/config (tilde-expanded), or "" — the
// desktop's get_default_identity_file, used to prefill new profiles.
func DefaultIdentityFile(home string) string {
	raw, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return ""
	}
	inWildcard := false
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := parseKV(line)
		if !ok {
			continue
		}
		if key == "host" {
			inWildcard = isWildcardHost(value)
			continue
		}
		if inWildcard && key == "identityfile" {
			return expandTilde(value, home)
		}
	}
	return ""
}

// ExpandTilde expands a leading ~ against home.
func ExpandTilde(path, home string) string { return expandTilde(path, home) }
