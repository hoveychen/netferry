//go:build !linux || android

package sockmark

import "syscall"

// control is a no-op: only the Linux firewall methods match on SO_MARK.
func control(network, address string, c syscall.RawConn) error { return nil }
