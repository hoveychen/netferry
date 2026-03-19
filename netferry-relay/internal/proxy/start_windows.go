//go:build windows

package proxy

import "github.com/hoveychen/netferry/relay/internal/mux"

// ListenTransparent starts the appropriate local proxy listener.
// On Windows, this is a SOCKS5 proxy (system proxy settings are configured
// by firewall.winMethod.Setup so that applications use it automatically).
func ListenTransparent(port int, client *mux.MuxClient) error {
	return ListenSOCKS5(port, client)
}
