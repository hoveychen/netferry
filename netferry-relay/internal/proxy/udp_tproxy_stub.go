//go:build !linux

package proxy

import (
	"fmt"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// ListenUDPTProxy is not supported on this platform.
func ListenUDPTProxy(port int, client *mux.MuxClient, counters *stats.Counters) error {
	return fmt.Errorf("UDP TPROXY only supported on Linux")
}
