// Package profile loads and decrypts .nfprofile files produced by the
// NetFerry desktop app. The on-disk format is a base64-encoded AES-256-GCM
// ciphertext (nonce || ciphertext+tag) over a JSON-encoded Profile struct.
// The encryption key is compiled in via ldflags (ExportKey) — see crypto.go.
package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DnsMode mirrors the desktop enum: "off" | "all" | "specific".
type DnsMode string

const (
	DnsOff      DnsMode = "off"
	DnsAll      DnsMode = "all"
	DnsSpecific DnsMode = "specific"
)

// JumpHost matches desktop Profile.jump_hosts[i]. IdentityKey is inline PEM.
type JumpHost struct {
	Remote       string `json:"remote"`
	IdentityFile string `json:"identityFile,omitempty"`
	IdentityKey  string `json:"identityKey,omitempty"`
}

// Profile mirrors the desktop `Profile` struct (models.rs). Fields not used
// by the tunnel CLI (color, autoConnect, notes, remotePython, latencyBufferSize)
// are accepted silently via json.Unmarshal's permissive behavior.
//
// Booleans whose desktop default is true use *bool so a missing field does
// not collapse to Go's false zero-value.
type Profile struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Remote        string     `json:"remote"`
	IdentityFile  string     `json:"identityFile"`
	IdentityKey   string     `json:"identityKey,omitempty"`
	JumpHosts     []JumpHost `json:"jumpHosts,omitempty"`
	Subnets       []string   `json:"subnets"`
	Dns           DnsMode    `json:"dns"`
	ExcludeSubnets []string  `json:"excludeSubnets"`
	AutoNets      bool       `json:"autoNets"`
	DnsTarget     string     `json:"dnsTarget,omitempty"`
	Method        string     `json:"method"`
	ExtraSSHOpts  string     `json:"extraSshOptions,omitempty"`
	DisableIPv6   bool       `json:"disableIpv6"`
	EnableUDP     bool       `json:"enableUdp"`
	BlockUDP      *bool      `json:"blockUdp,omitempty"`
	AutoExcludeLAN *bool     `json:"autoExcludeLan,omitempty"`
	PoolSize      int        `json:"poolSize,omitempty"`
	SplitConn     bool       `json:"splitConn"`
	TcpBalance    string     `json:"tcpBalanceMode,omitempty"`
}

// BlockUDPOrDefault returns BlockUDP with the desktop default (true) when unset.
func (p *Profile) BlockUDPOrDefault() bool {
	if p.BlockUDP == nil {
		return true
	}
	return *p.BlockUDP
}

// AutoExcludeLANOrDefault returns AutoExcludeLAN with the desktop default (true) when unset.
func (p *Profile) AutoExcludeLANOrDefault() bool {
	if p.AutoExcludeLAN == nil {
		return true
	}
	return *p.AutoExcludeLAN
}

// LoadFile reads the given .nfprofile file, decrypts it with the build-time
// ExportKey, and returns the parsed Profile.
func LoadFile(path string) (*Profile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile file: %w", err)
	}
	plaintext, err := Decrypt(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(plaintext, &p); err != nil {
		return nil, fmt.Errorf("parse profile JSON: %w", err)
	}
	return &p, nil
}
