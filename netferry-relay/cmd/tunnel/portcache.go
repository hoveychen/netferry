package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"

	"github.com/hoveychen/netferry/relay/internal/proxy"
)

// portCache stores previously used ports so reconnections reuse the same ports
// when possible.
type portCache struct {
	ProxyPort int `json:"proxy_port,omitempty"`
	DNSPort   int `json:"dns_port,omitempty"`
	StatsPort int `json:"stats_port,omitempty"`
}

func portCachePath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "netferry", "ports.json")
	}
	return ""
}

func loadPortCache() portCache {
	path := portCachePath()
	if path == "" {
		return portCache{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return portCache{}
	}
	var pc portCache
	json.Unmarshal(data, &pc)
	return pc
}

func savePortCache(pc portCache) {
	path := portCachePath()
	if path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	data, _ := json.Marshal(pc)
	os.WriteFile(path, data, 0o644)
}

// pickFreePort tries to bind to preferredPort first; if that fails (or is 0),
// it falls back to an OS-assigned port. Uses proxy.BindAddr for TCP.
func pickFreePort(network string, preferredPort int) int {
	bindAddr := proxy.BindAddr

	if preferredPort > 0 {
		switch network {
		case "tcp":
			ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", bindAddr, preferredPort))
			if err == nil {
				ln.Close()
				return preferredPort
			}
		case "udp":
			ln, err := net.ListenPacket("udp", fmt.Sprintf("%s:%d", bindAddr, preferredPort))
			if err == nil {
				ln.Close()
				return preferredPort
			}
		}
		log.Printf("preferred %s port %d in use, picking a new one", network, preferredPort)
	}

	switch network {
	case "tcp":
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:0", bindAddr))
		if err != nil {
			fatalf("pick free TCP port: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		return port
	case "udp":
		ln, err := net.ListenPacket("udp", fmt.Sprintf("%s:0", bindAddr))
		if err != nil {
			fatalf("pick free UDP port: %v", err)
		}
		port := ln.LocalAddr().(*net.UDPAddr).Port
		ln.Close()
		return port
	default:
		panic("unknown network: " + network)
	}
}
