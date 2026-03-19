// Package firewall provides platform-specific firewall management for
// transparent TCP and DNS proxying.
package firewall

// Method is implemented by each platform-specific backend.
type Method interface {
	// Setup installs redirect rules.
	// subnets: CIDR strings to proxy (e.g. "0.0.0.0/0").
	// excludes: CIDR strings to pass through unchanged.
	// proxyPort: local TCP port the transparent proxy listens on.
	// dnsPort: local UDP port for DNS (0 = DNS proxying disabled).
	// dnsServers: remote DNS server IPs to redirect (only used when dnsPort > 0).
	Setup(subnets, excludes []string, proxyPort, dnsPort int, dnsServers []string) error

	// Restore removes all rules installed by Setup.
	Restore() error

	// Name returns the method name for logging.
	Name() string
}

// NewAuto picks the best available method for the current platform.
func NewAuto() Method {
	return newDefault()
}

// New returns the named method, or an error if it's not supported.
func New(name string) (Method, error) {
	return newNamed(name)
}
