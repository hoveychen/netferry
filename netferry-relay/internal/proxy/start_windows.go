//go:build windows

package proxy

import (
	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// ListenTransparent starts the appropriate local proxy listener.
// On Windows with WinDivert, this is a transparent TCP proxy (QueryOrigDstFunc
// is set). Otherwise falls back to SOCKS5 proxy.
func ListenTransparent(port int, client *mux.MuxClient, counters *stats.Counters) error {
	if QueryOrigDstFunc != nil {
		return Listen(port, client, counters)
	}
	return ListenSOCKS5(port, client, counters)
}
