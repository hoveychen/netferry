// Package firewall provides platform-specific firewall management for
// transparent TCP/UDP and DNS proxying.
package firewall

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Feature describes a capability that a firewall method may support.
type Feature string

const (
	FeatureIPv6      Feature = "ipv6"
	FeatureUDP       Feature = "udp"
	FeatureDNS       Feature = "dns"
	FeaturePortRange Feature = "portRange"
	FeatureBlockUDP  Feature = "blockUdp"
)

// Method is implemented by each platform-specific backend.
type Method interface {
	// Setup installs redirect rules.
	// subnets: SubnetRules to proxy (parsed from CIDR[:portLow-portHigh]).
	// excludes: CIDR strings to pass through unchanged.
	// proxyPort: local TCP port the transparent proxy listens on.
	// dnsPort: local UDP port for DNS (0 = DNS proxying disabled).
	// dnsServers: remote DNS server IPs to redirect (only used when dnsPort > 0).
	Setup(subnets []SubnetRule, excludes []string, proxyPort, dnsPort int, dnsServers []string) error

	// Restore removes all rules installed by Setup.
	Restore() error

	// Name returns the method name for logging.
	Name() string

	// SupportedFeatures returns the features this method supports.
	SupportedFeatures() []Feature
}

// OrigDstQuerier is optionally implemented by Methods that can resolve the
// original destination of a redirected connection (e.g. WinDivert).
type OrigDstQuerier interface {
	QueryOrigDst(conn net.Conn) (ip string, port int, err error)
}

// UDPBlocker is optionally implemented by Methods that support blocking
// non-DNS UDP traffic (e.g. to prevent QUIC leaks on macOS pf).
type UDPBlocker interface {
	SetBlockUDP(block bool)
}

// SetUDPBlock calls SetBlockUDP on the method if it implements UDPBlocker.
func SetUDPBlock(m Method, block bool) {
	if b, ok := m.(UDPBlocker); ok {
		b.SetBlockUDP(block)
	}
}

// QueryOrigDstFor returns a QueryOrigDst function if the Method implements
// OrigDstQuerier, or nil otherwise.
func QueryOrigDstFor(m Method) func(net.Conn) (string, int, error) {
	if q, ok := m.(OrigDstQuerier); ok {
		return q.QueryOrigDst
	}
	return nil
}

// Supports returns true if the method supports all the given features.
func Supports(m Method, features ...Feature) bool {
	supported := make(map[Feature]bool)
	for _, f := range m.SupportedFeatures() {
		supported[f] = true
	}
	for _, f := range features {
		if !supported[f] {
			return false
		}
	}
	return true
}

// NewAuto picks the best available method for the current platform.
func NewAuto() Method {
	return newDefault()
}

// New returns the named method, or an error if it's not supported.
func New(name string) (Method, error) {
	return newNamed(name)
}

// ListMethodFeatures returns a map of method name → supported features
// for all methods available on the current platform.
func ListMethodFeatures() map[string][]Feature {
	return listMethodFeatures()
}

// --- SubnetRule ---

// SubnetRule represents a subnet to proxy, optionally restricted to a port range.
type SubnetRule struct {
	CIDR    string // e.g. "10.0.0.0/8", "fd00::/64"
	PortLow int    // 0 = all ports
	PortHigh int   // 0 = all ports
}

// IsIPv6 returns true if this rule targets an IPv6 subnet.
func (s SubnetRule) IsIPv6() bool {
	return IsIPv6CIDR(s.CIDR)
}

// HasPortRange returns true if this rule is restricted to a port range.
func (s SubnetRule) HasPortRange() bool {
	return s.PortLow > 0 && s.PortHigh > 0
}

// NftPortExpr returns nft syntax for the port range, or "" if no range.
func (s SubnetRule) NftPortExpr() string {
	if !s.HasPortRange() {
		return ""
	}
	if s.PortLow == s.PortHigh {
		return fmt.Sprintf("tcp dport %d", s.PortLow)
	}
	return fmt.Sprintf("tcp dport %d-%d", s.PortLow, s.PortHigh)
}

// PfPortExpr returns pf syntax for the port range, or "" if no range.
func (s SubnetRule) PfPortExpr() string {
	if !s.HasPortRange() {
		return ""
	}
	if s.PortLow == s.PortHigh {
		return fmt.Sprintf(" port %d", s.PortLow)
	}
	return fmt.Sprintf(" port %d:%d", s.PortLow, s.PortHigh)
}

// IptPortArgs returns iptables args for the port range, or nil if no range.
func (s SubnetRule) IptPortArgs() []string {
	if !s.HasPortRange() {
		return nil
	}
	if s.PortLow == s.PortHigh {
		return []string{"--dport", strconv.Itoa(s.PortLow)}
	}
	return []string{"--dport", fmt.Sprintf("%d:%d", s.PortLow, s.PortHigh)}
}

// ParseSubnetRule parses "CIDR" or "CIDR:portLow-portHigh" into a SubnetRule.
func ParseSubnetRule(s string) (SubnetRule, error) {
	// Check for port suffix: "10.0.0.0/8:80-443" or "fd00::/64#80-443"
	// For IPv6, use '#' as separator since ':' is ambiguous. Also support ':'
	// for IPv4 for backwards compat.
	var cidr, portPart string

	if idx := strings.LastIndex(s, "#"); idx >= 0 {
		cidr = s[:idx]
		portPart = s[idx+1:]
	} else {
		// For IPv4 CIDRs: "10.0.0.0/8:80-443"
		// For plain IPv6 CIDRs: "fd00::/64" (no port)
		// Heuristic: if there's a '/' and a ':' after it, the ':' is a port separator
		slashIdx := strings.Index(s, "/")
		if slashIdx >= 0 {
			rest := s[slashIdx:]
			if colonIdx := strings.LastIndex(rest, ":"); colonIdx >= 0 {
				candidate := rest[colonIdx+1:]
				// Only treat as port if it looks like a number or range
				if _, err := strconv.Atoi(candidate); err == nil {
					cidr = s[:slashIdx+colonIdx]
					portPart = candidate
				} else if strings.Contains(candidate, "-") {
					cidr = s[:slashIdx+colonIdx]
					portPart = candidate
				} else {
					cidr = s
				}
			} else {
				cidr = s
			}
		} else {
			cidr = s
		}
	}

	// Validate CIDR.
	_, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return SubnetRule{}, fmt.Errorf("parse subnet %q: %w", s, err)
	}

	rule := SubnetRule{CIDR: cidr}

	if portPart != "" {
		if strings.Contains(portPart, "-") {
			parts := strings.SplitN(portPart, "-", 2)
			rule.PortLow, err = strconv.Atoi(parts[0])
			if err != nil {
				return SubnetRule{}, fmt.Errorf("parse port range %q: %w", portPart, err)
			}
			rule.PortHigh, err = strconv.Atoi(parts[1])
			if err != nil {
				return SubnetRule{}, fmt.Errorf("parse port range %q: %w", portPart, err)
			}
		} else {
			p, err := strconv.Atoi(portPart)
			if err != nil {
				return SubnetRule{}, fmt.Errorf("parse port %q: %w", portPart, err)
			}
			rule.PortLow = p
			rule.PortHigh = p
		}
	}

	return rule, nil
}

// ParseSubnetRules parses a slice of subnet strings into SubnetRules.
func ParseSubnetRules(ss []string) ([]SubnetRule, error) {
	rules := make([]SubnetRule, 0, len(ss))
	for _, s := range ss {
		r, err := ParseSubnetRule(s)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// --- Helpers ---

// IsIPv6CIDR returns true if the CIDR string is an IPv6 network.
func IsIPv6CIDR(cidr string) bool {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		// Try parsing as plain IP.
		ip = net.ParseIP(cidr)
	}
	if ip == nil {
		return false
	}
	return ip.To4() == nil
}

// SplitByFamily splits subnet rules into IPv4 and IPv6 groups.
func SplitByFamily(rules []SubnetRule) (v4, v6 []SubnetRule) {
	for _, r := range rules {
		if r.IsIPv6() {
			v6 = append(v6, r)
		} else {
			v4 = append(v4, r)
		}
	}
	return
}

// SplitExcludesByFamily splits exclude CIDRs into IPv4 and IPv6 groups.
func SplitExcludesByFamily(excludes []string) (v4, v6 []string) {
	for _, e := range excludes {
		if IsIPv6CIDR(e) {
			v6 = append(v6, e)
		} else {
			v4 = append(v4, e)
		}
	}
	return
}

// SplitDNSByFamily splits DNS server IPs into IPv4 and IPv6 groups.
func SplitDNSByFamily(servers []string) (v4, v6 []string) {
	for _, s := range servers {
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		if ip.To4() == nil {
			v6 = append(v6, s)
		} else {
			v4 = append(v4, s)
		}
	}
	return
}

// TProxyConfig holds configurable TPROXY parameters.
type TProxyConfig struct {
	FWMark     int // default: 1
	RouteTable int // default: 100
}

// DefaultTProxyConfig returns the default TPROXY configuration.
func DefaultTProxyConfig() TProxyConfig {
	return TProxyConfig{FWMark: 1, RouteTable: 100}
}

// SetTProxyConfig updates the TPROXY configuration if m is a TPROXY method.
func SetTProxyConfig(m Method, cfg TProxyConfig) {
	type tproxyConfigurable interface {
		SetConfig(TProxyConfig)
	}
	if tc, ok := m.(tproxyConfigurable); ok {
		tc.SetConfig(cfg)
	}
}
