//go:build !windows

package proxy

import "github.com/hoveychen/netferry/relay/internal/mux"

// ListenTransparent starts the appropriate local proxy listener.
// On Unix, this is a transparent TCP proxy (requires firewall redirect).
func ListenTransparent(port int, client *mux.MuxClient) error {
	return Listen(port, client)
}
