//go:build !linux

package proxy

import (
	"fmt"
	"net"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// ListenTProxy is not supported on this platform.
func ListenTProxy(port int, client *mux.MuxClient, counters *stats.Counters) error {
	return fmt.Errorf("TPROXY is only supported on Linux")
}

// ListenDNSTProxy is not supported on this platform.
func ListenDNSTProxy(port int) (net.PacketConn, error) {
	return nil, fmt.Errorf("TPROXY is only supported on Linux")
}
