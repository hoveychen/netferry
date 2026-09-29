package store

import (
	"strings"

	"golang.org/x/net/publicsuffix"
)

// NormalizeDomain normalizes one user-entered rule-group scope, mirroring
// netferry-desktop/src/lib/ruleGroups.ts normalizeDomain. A leading "=" marks
// an exact-host scope; a bare domain covers the apex and all subdomains and
// must have a registrable domain. Returns ok=false for invalid input.
func NormalizeDomain(input string) (string, bool) {
	raw := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(input)), ".")
	exact := strings.HasPrefix(raw, "=")
	host := raw
	if exact {
		host = raw[1:]
	}
	if host == "" || len(host) > 253 || strings.Contains(host, "..") {
		return "", false
	}
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return "", false
		}
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", false
		}
	}
	if !exact {
		if _, err := publicsuffix.EffectiveTLDPlusOne(host); err != nil {
			return "", false
		}
		return host, true
	}
	return "=" + host, true
}

// MatchesDomain reports whether host falls inside a rule-group scope.
func MatchesDomain(host, domain string) bool {
	normalized, ok := NormalizeDomain(domain)
	if !ok {
		return false
	}
	exact := strings.HasPrefix(normalized, "=")
	value := strings.TrimPrefix(normalized, "=")
	key := strings.TrimSuffix(strings.ToLower(host), ".")
	return key == value || (!exact && strings.HasSuffix(key, "."+value))
}

// RuleGroupFor returns the first rule group whose scopes cover host, or nil.
func RuleGroupFor(groups []RuleGroup, host string) *RuleGroup {
	for i := range groups {
		for _, d := range groups[i].Domains {
			if MatchesDomain(host, d) {
				return &groups[i]
			}
		}
	}
	return nil
}

// RegistrableDomain returns the eTLD+1 of host, or "" when it has none (IPs,
// bare public suffixes).
func RegistrableDomain(host string) string {
	d, err := publicsuffix.EffectiveTLDPlusOne(strings.TrimSuffix(strings.ToLower(host), "."))
	if err != nil {
		return ""
	}
	return d
}
