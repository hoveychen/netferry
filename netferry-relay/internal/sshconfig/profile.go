package sshconfig

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/profile"
)

// BuildRemote turns a HostEntry into the desktop-style "[user@]host[:port]"
// string that NetFerry profiles use for the Remote field. HostName overrides
// Host when present (matching desktop SshConfigImporter.tsx::buildRemote).
func BuildRemote(e HostEntry) string {
	host := e.Host
	if e.HostName != nil && *e.HostName != "" {
		host = *e.HostName
	}
	if e.User != nil && *e.User != "" {
		host = *e.User + "@" + host
	}
	if e.Port != nil {
		host = host + ":" + strconv.Itoa(*e.Port)
	}
	return host
}

// ResolveJumpHosts expands a ProxyJump value (comma-separated hop list) into
// JumpHost entries. Each hop is matched against the supplied host list — known
// aliases get fully resolved (HostName/User/Port/IdentityFile applied), unknown
// hops are passed through verbatim.
func ResolveJumpHosts(proxyJump string, all []HostEntry) []profile.JumpHost {
	hops := strings.Split(proxyJump, ",")
	out := make([]profile.JumpHost, 0, len(hops))
	for _, h := range hops {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if entry := findHost(all, h); entry != nil {
			jh := profile.JumpHost{Remote: BuildRemote(*entry)}
			if entry.IdentityFile != nil {
				jh.IdentityFile = *entry.IdentityFile
			}
			out = append(out, jh)
			continue
		}
		out = append(out, profile.JumpHost{Remote: h})
	}
	return out
}

var (
	sshPrefixRE = regexp.MustCompile(`(?i)^ssh\s+`)
	dashWRE     = regexp.MustCompile(`-W\s+%h:%p`)
)

// ParseProxyCommandAsJump tries to interpret a ProxyCommand of the form
//
//	ssh [opts] -W %h:%p <alias|host>
//
// as a single jump host. Returns nil when the pattern doesn't match, leaving
// the caller free to fall back to ExtraSSHOpts.
func ParseProxyCommandAsJump(cmd string, all []HostEntry) []profile.JumpHost {
	t := strings.TrimSpace(cmd)
	if !sshPrefixRE.MatchString(t) || !strings.Contains(t, "-W %h:%p") {
		return nil
	}
	rest := strings.TrimSpace(dashWRE.ReplaceAllString(sshPrefixRE.ReplaceAllString(t, ""), ""))
	tokens := strings.Fields(rest)

	var port, identity string
	remaining := make([]string, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		switch tokens[i] {
		case "-p":
			if i+1 < len(tokens) {
				i++
				port = tokens[i]
			}
		case "-i":
			if i+1 < len(tokens) {
				i++
				identity = tokens[i]
			}
		case "-o":
			if i+1 < len(tokens) {
				i++
			}
		default:
			if strings.HasPrefix(tokens[i], "-") {
				continue
			}
			remaining = append(remaining, tokens[i])
		}
	}
	if len(remaining) == 0 {
		return nil
	}
	dest := remaining[len(remaining)-1]
	if entry := findHost(all, dest); entry != nil {
		jh := profile.JumpHost{Remote: BuildRemote(*entry)}
		if entry.IdentityFile != nil {
			jh.IdentityFile = *entry.IdentityFile
		} else if identity != "" {
			jh.IdentityFile = identity
		}
		return []profile.JumpHost{jh}
	}
	remote := dest
	if port != "" {
		remote = remote + ":" + port
	}
	return []profile.JumpHost{{Remote: remote, IdentityFile: identity}}
}

// ApplyToProfile fills SSH-derived fields on the given Profile from a
// HostEntry. Caller owns the rest (ID assignment, defaults, etc.). `all` is
// the full host list — used to resolve ProxyJump aliases and ProxyCommand
// `ssh -W` references.
func ApplyToProfile(p *profile.Profile, e HostEntry, all []HostEntry) {
	p.Name = e.Host
	p.Remote = BuildRemote(e)
	if e.IdentityFile != nil {
		p.IdentityFile = *e.IdentityFile
	}
	p.Imported = true

	var jumps []profile.JumpHost
	if e.ProxyJump != nil {
		jumps = ResolveJumpHosts(*e.ProxyJump, all)
	}
	if len(jumps) == 0 && e.ProxyCommand != nil && e.ProxyJump == nil {
		if parsed := ParseProxyCommandAsJump(*e.ProxyCommand, all); parsed != nil {
			jumps = parsed
		} else {
			p.ExtraSSHOpts = fmt.Sprintf("-o ProxyCommand='%s'", *e.ProxyCommand)
		}
	}
	if len(jumps) > 0 {
		p.JumpHosts = jumps
	}
}

func findHost(all []HostEntry, host string) *HostEntry {
	for i := range all {
		if all[i].Host == host {
			return &all[i]
		}
	}
	return nil
}
