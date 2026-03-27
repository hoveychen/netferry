//go:build !windows

package proxy

import (
	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// ListenTransparent starts the appropriate local proxy listener.
// On Unix, this is a transparent TCP proxy (requires firewall redirect).
func ListenTransparent(port int, client mux.TunnelClient, counters *stats.Counters) error {
	if UseTProxy {
		return ListenTProxy(port, client, counters)
	}
	return Listen(port, client, counters)
}
