// Package mobile provides a gomobile-compatible API for the NetFerry tunnel
// engine. It wraps the internal SSH/mux/proxy packages into a simple
// Start/Stop interface that iOS (NEPacketTunnelProvider) and Android
// (VpnService) can call via gomobile bind.
//
// gomobile type restrictions: exported functions and interface methods may only
// use int, int32, int64, float32, float64, bool, string, []byte, error.
// Complex data crosses the boundary as JSON strings.
package mobile

import "encoding/json"

// Config is the tunnel configuration passed as JSON from the native side.
// Field names and semantics match the desktop Profile type (types.ts / models.rs).
type Config struct {
	// SSH connection
	Remote          string     `json:"remote"`                    // [user@]host[:port]
	IdentityKey     string     `json:"identityKey"`               // PEM-encoded private key
	JumpHosts       []jumpHost `json:"jumpHosts,omitempty"`
	ExtraSSHOptions string     `json:"extraSshOptions,omitempty"` // extra SSH options string

	// Routing
	Subnets        []string `json:"subnets"`                  // CIDRs to proxy (e.g. ["0.0.0.0/0"])
	ExcludeSubnets []string `json:"excludeSubnets,omitempty"` // CIDRs to exclude
	AutoNets       bool     `json:"autoNets"`                 // add remote-advertised routes
	AutoExcludeLAN bool     `json:"autoExcludeLan"`           // auto-exclude LAN subnets

	// DNS: "off", "all", or "specific" (matching desktop DnsMode)
	DNS       string `json:"dns"`
	DNSTarget string `json:"dnsTarget,omitempty"` // remote DNS server IP[@port]

	// UDP
	EnableUDP bool `json:"enableUdp"` // enable generic UDP proxy
	BlockUDP  bool `json:"blockUdp"`  // block non-DNS UDP (QUIC leak prevention)

	// Performance
	PoolSize          int    `json:"poolSize"`                    // SSH connection pool size (default 1)
	SplitConn         bool   `json:"splitConn"`                   // separate data/ctrl SSH connections
	TCPBalanceMode    string `json:"tcpBalanceMode,omitempty"`    // "round-robin" or "least-loaded"
	LatencyBufferSize *int   `json:"latencyBufferSize,omitempty"` // smux receive buffer size

	// Network
	DisableIPv6 bool `json:"disableIpv6"`

	// TUN
	MTU int `json:"mtu"` // TUN device MTU (default 1500)

	// UI-only (not used by engine, stored/displayed by native side)
	Notes string `json:"notes,omitempty"`
}

type jumpHost struct {
	Remote      string `json:"remote"`
	IdentityKey string `json:"identityKey,omitempty"`
}

func parseConfig(jsonStr string) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		return nil, err
	}
	// Defaults
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = 1
	}
	if cfg.MTU <= 0 {
		cfg.MTU = 1500
	}
	if len(cfg.Subnets) == 0 {
		cfg.Subnets = []string{"0.0.0.0/0"}
	}
	if cfg.DNS == "" {
		cfg.DNS = "all"
	}
	if cfg.TCPBalanceMode == "" {
		cfg.TCPBalanceMode = "least-loaded"
	}
	return &cfg, nil
}

// dnsEnabled returns true if DNS interception should be active.
func (c *Config) dnsEnabled() bool {
	return c.DNS == "all" || c.DNS == "specific"
}

// statsSnapshot is serialized to JSON and returned by Engine.GetStats.
type statsSnapshot struct {
	RxBytesPerSec int64 `json:"rxBytesPerSec"`
	TxBytesPerSec int64 `json:"txBytesPerSec"`
	TotalRxBytes  int64 `json:"totalRxBytes"`
	TotalTxBytes  int64 `json:"totalTxBytes"`
	ActiveConns   int32 `json:"activeConns"`
	TotalConns    int64 `json:"totalConns"`
	DNSQueries    int64 `json:"dnsQueries"`
}
