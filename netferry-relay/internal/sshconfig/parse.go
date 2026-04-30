// Package sshconfig parses ~/.ssh/config and exposes a list of named Host
// entries with their resolved fields. It mirrors the desktop's
// ssh_config.rs::parse_default_ssh_config so TUI imports produce identical
// HostEntry values to the desktop's import flow, plus extends it with Include
// resolution and Match-block skipping for users that split their config.
package sshconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// HostEntry mirrors desktop SshHostEntry. Pointer fields distinguish "unset"
// from "empty" so wildcard defaults can fill only the gaps.
type HostEntry struct {
	Host         string
	HostName     *string
	User         *string
	Port         *int
	IdentityFile *string
	ProxyJump    *string
	ProxyCommand *string
}

// ParseDefault reads ~/.ssh/config (resolved against home), recursively
// expands `Include` directives, and returns a sorted list of Host entries with
// wildcard `Host *` defaults applied to fields the entry didn't override.
// `Match` blocks are skipped (we can't evaluate their conditions without
// runtime context, so they neither yield entries nor pollute defaults).
// A missing config file is not an error — empty slice is returned.
func ParseDefault(home string) ([]HostEntry, error) {
	path := filepath.Join(home, ".ssh", "config")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat ~/.ssh/config: %w", err)
	}
	a := newAccumulator(home)
	if err := a.processFile(path, filepath.Join(home, ".ssh"), map[string]bool{}); err != nil {
		return nil, err
	}
	return a.finalize(), nil
}

// Parse decodes raw ssh_config text into a sorted Host list with wildcard
// defaults applied. Include directives are silently ignored (they require a
// filesystem context — use ParseDefault for that). Exposed for testing.
func Parse(raw, home string) ([]HostEntry, error) {
	a := newAccumulator(home)
	a.processText(raw, "", map[string]bool{})
	return a.finalize(), nil
}

// accumulator carries the mutable state of a parse pass. Pulled into a struct
// so Include can recurse without juggling closures.
type accumulator struct {
	home              string
	entries           []HostEntry
	wildcard          HostEntry
	current           *HostEntry
	currentIsWildcard bool
	skipping          bool // inside a `Match` block — drop directives until next Host
}

func newAccumulator(home string) *accumulator {
	return &accumulator{home: home, wildcard: HostEntry{Host: "*"}}
}

func (a *accumulator) flush() {
	if a.current == nil {
		return
	}
	prev := *a.current
	a.current = nil
	if a.currentIsWildcard {
		if a.wildcard.HostName == nil {
			a.wildcard.HostName = prev.HostName
		}
		if a.wildcard.User == nil {
			a.wildcard.User = prev.User
		}
		if a.wildcard.Port == nil {
			a.wildcard.Port = prev.Port
		}
		if a.wildcard.IdentityFile == nil {
			a.wildcard.IdentityFile = prev.IdentityFile
		}
		if a.wildcard.ProxyJump == nil {
			a.wildcard.ProxyJump = prev.ProxyJump
		}
		if a.wildcard.ProxyCommand == nil {
			a.wildcard.ProxyCommand = prev.ProxyCommand
		}
		return
	}
	a.entries = append(a.entries, prev)
}

// processFile reads path and feeds it through processText. baseDir for that
// file is its containing directory (so nested Includes resolve correctly).
// visited guards against include cycles.
func (a *accumulator) processFile(path, baseDir string, visited map[string]bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if visited[abs] {
		return nil
	}
	visited[abs] = true
	raw, err := os.ReadFile(path)
	if err != nil {
		// Missing includes are non-fatal — OpenSSH tolerates them too.
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	a.processText(string(raw), filepath.Dir(path), visited)
	return nil
}

func (a *accumulator) processText(raw, baseDir string, visited map[string]bool) {
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := parseKV(line)
		if !ok {
			continue
		}
		switch key {
		case "host":
			a.flush()
			a.skipping = false
			a.currentIsWildcard = isWildcardHost(value)
			a.current = &HostEntry{Host: value}
		case "match":
			// Close out the previous Host block and ignore the Match contents.
			a.flush()
			a.skipping = true
		case "include":
			if baseDir == "" {
				// Best effort — caller passed raw text without a filesystem
				// anchor, so we can't resolve relative includes.
				continue
			}
			a.handleInclude(value, baseDir, visited)
		default:
			if a.skipping || a.current == nil {
				continue
			}
			a.applyDirective(key, value)
		}
	}
}

func (a *accumulator) applyDirective(key, value string) {
	switch key {
	case "hostname":
		v := value
		a.current.HostName = &v
	case "user":
		v := value
		a.current.User = &v
	case "port":
		if n, err := strconv.Atoi(value); err == nil {
			a.current.Port = &n
		}
	case "identityfile":
		v := expandTilde(value, a.home)
		a.current.IdentityFile = &v
	case "proxyjump":
		v := value
		a.current.ProxyJump = &v
	case "proxycommand":
		v := value
		a.current.ProxyCommand = &v
	}
}

// handleInclude expands one Include directive. OpenSSH supports glob patterns
// and tilde expansion; relative paths resolve against the directory of the
// file containing the Include.
func (a *accumulator) handleInclude(spec, baseDir string, visited map[string]bool) {
	// OpenSSH treats Include with multiple whitespace-separated paths.
	for _, raw := range strings.Fields(spec) {
		path := expandTilde(raw, a.home)
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		matches, err := filepath.Glob(path)
		if err != nil || len(matches) == 0 {
			// Treat as a literal path when glob doesn't expand to anything —
			// matches OpenSSH's behavior for non-glob Include.
			matches = []string{path}
		}
		sort.Strings(matches)
		for _, m := range matches {
			// Closing flush before nested file so Host blocks don't span files.
			a.flush()
			_ = a.processFile(m, filepath.Dir(m), visited)
		}
	}
}

func (a *accumulator) finalize() []HostEntry {
	a.flush()
	for i := range a.entries {
		e := &a.entries[i]
		if e.IdentityFile == nil {
			e.IdentityFile = a.wildcard.IdentityFile
		}
		if e.User == nil {
			e.User = a.wildcard.User
		}
		if e.Port == nil {
			e.Port = a.wildcard.Port
		}
		if e.ProxyJump == nil {
			e.ProxyJump = a.wildcard.ProxyJump
		}
		if e.ProxyCommand == nil {
			e.ProxyCommand = a.wildcard.ProxyCommand
		}
	}
	sort.SliceStable(a.entries, func(i, j int) bool { return a.entries[i].Host < a.entries[j].Host })
	return a.entries
}

func parseKV(line string) (key, value string, ok bool) {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return "", "", false
	}
	idx := strings.IndexAny(t, " \t")
	if idx < 0 {
		return "", "", false
	}
	k := strings.TrimSpace(t[:idx])
	v := strings.TrimSpace(t[idx+1:])
	if k == "" || v == "" {
		return "", "", false
	}
	return strings.ToLower(k), v, true
}

// isWildcardHost matches the desktop rule: every non-negated pattern in the
// Host directive must contain a glob char (`*` or `?`). This treats `Host *`
// (and `Host *.foo`) as defaults, while `Host alias` is a real entry.
func isWildcardHost(host string) bool {
	parts := strings.Fields(host)
	if len(parts) == 0 {
		return false
	}
	any := false
	for _, p := range parts {
		if strings.HasPrefix(p, "!") {
			continue
		}
		any = true
		if !strings.ContainsAny(p, "*?") {
			return false
		}
	}
	return any
}

func expandTilde(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
