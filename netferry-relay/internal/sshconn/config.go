package sshconn

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gossh "github.com/kevinburke/ssh_config"
)

// HostConfig is the resolved configuration for a single SSH target.
type HostConfig struct {
	User         string
	HostName     string
	Port         int
	IdentityFile string
	ProxyJump    string
	ProxyCommand string
	// StrictHostKeyChecking: "yes" | "no" | "accept-new" | ""
	StrictHostKeyChecking string
	UserKnownHostsFile    string
}

// ParseSSHConfig loads ~/.ssh/config and resolves settings for the given alias.
// alias may be "user@host[:port]", "host[:port]", or an SSH config Host alias.
func ParseSSHConfig(alias string) (*HostConfig, error) {
	user, host := splitUserHost(alias)

	// Extract port from host if present (e.g. "host:2222").
	cliPort := 0
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		if p, err := strconv.Atoi(host[idx+1:]); err == nil {
			cliPort = p
			host = host[:idx]
		}
	}

	cfgPath := filepath.Join(expandHome("~"), ".ssh", "config")
	f, err := os.Open(cfgPath)
	if err != nil {
		// No config file — return defaults.
		port := 22
		if cliPort > 0 {
			port = cliPort
		}
		hc := &HostConfig{User: user, HostName: host, Port: port}
		if hc.User == "" {
			hc.User = os.Getenv("USER")
		}
		return hc, nil
	}
	defer f.Close()

	cfg, err := gossh.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("ssh_config: %w", err)
	}

	hc := &HostConfig{User: user, HostName: host, Port: 22}
	if cliPort > 0 {
		hc.Port = cliPort
	}

	// Helper: get a config value, ignoring errors (missing key returns "").
	get := func(key string) string {
		v, _ := cfg.Get(host, key)
		return v
	}

	if hn := get("HostName"); hn != "" {
		hc.HostName = hn
	}
	if u := get("User"); u != "" && hc.User == "" {
		hc.User = u
	}
	if cliPort == 0 {
		if p := get("Port"); p != "" {
			if n, err := strconv.Atoi(p); err == nil {
				hc.Port = n
			}
		}
	}
	if id := get("IdentityFile"); id != "" {
		hc.IdentityFile = expandHome(id)
	}
	if pj := get("ProxyJump"); pj != "" {
		hc.ProxyJump = pj
	}
	if pc := get("ProxyCommand"); pc != "" {
		hc.ProxyCommand = pc
	}
	if sc := get("StrictHostKeyChecking"); sc != "" {
		hc.StrictHostKeyChecking = sc
	}
	if kh := get("UserKnownHostsFile"); kh != "" {
		hc.UserKnownHostsFile = expandHome(kh)
	}

	// Defaults.
	if hc.User == "" {
		hc.User = os.Getenv("USER")
		if hc.User == "" {
			hc.User = "root"
		}
	}

	return hc, nil
}

// splitUserHost splits "user@host" into (user, host). Returns ("", host) if no @.
func splitUserHost(s string) (user, host string) {
	if idx := strings.LastIndex(s, "@"); idx >= 0 {
		user = s[:idx]
		host = s[idx+1:]
	} else {
		host = s
	}
	return
}
