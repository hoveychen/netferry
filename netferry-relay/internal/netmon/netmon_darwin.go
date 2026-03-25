package netmon

import (
	"fmt"
	"log"
	"syscall"
	"time"
)

// Watch opens a BSD routing socket (AF_ROUTE) and blocks until a network
// change is detected (interface up/down, address add/remove, route change).
// Returns nil on the first relevant change, or an error if the socket fails.
//
// Typical usage:
//
//	go func() { netChangeCh <- netmon.Watch(ctx) }()
func Watch(done <-chan struct{}) error {
	fd, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, 0)
	if err != nil {
		return fmt.Errorf("netmon: open routing socket: %w", err)
	}
	defer syscall.Close(fd)

	// Skip network events during the first few seconds after startup to
	// avoid reacting to route changes caused by our own firewall setup.
	startup := time.Now()

	buf := make([]byte, 4096)
	for {
		// Check if we should stop before blocking on read.
		select {
		case <-done:
			return nil
		default:
		}

		// Set a read timeout so we can periodically check the done channel.
		tv := syscall.Timeval{Sec: 2}
		if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
			return fmt.Errorf("netmon: set timeout: %w", err)
		}

		n, err := syscall.Read(fd, buf)
		if err != nil {
			// Timeout — loop back and check done.
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				continue
			}
			return fmt.Errorf("netmon: read routing socket: %w", err)
		}
		if n < 4 {
			continue
		}

		// Ignore events during the grace period after startup.
		if time.Since(startup) < 5*time.Second {
			continue
		}

		// Parse the routing message type (offset 3 in the rt_msghdr).
		msgType := buf[3]
		if isRelevantChange(msgType) {
			log.Printf("netmon: network change detected (type=%d), signalling reconnect", msgType)
			return nil
		}
	}
}

// isRelevantChange returns true for routing message types that indicate
// a meaningful network topology change.
//
// RTM_ADD / RTM_DELETE / RTM_CHANGE are intentionally excluded: they fire
// on routine route-table updates (ARP, tunnel's own firewall rules, etc.)
// and would cause false-positive reconnects. We only care about interface
// state changes and address add/remove, which reliably indicate a real
// network switch (WiFi change, cable unplug, VPN up/down).
func isRelevantChange(msgType byte) bool {
	const (
		RTM_NEWADDR  = 0xc
		RTM_DELADDR  = 0xd
		RTM_IFINFO   = 0xe
		RTM_IFINFO2  = 0x12
	)
	switch msgType {
	case RTM_NEWADDR, RTM_DELADDR,
		RTM_IFINFO, RTM_IFINFO2:
		return true
	}
	return false
}
