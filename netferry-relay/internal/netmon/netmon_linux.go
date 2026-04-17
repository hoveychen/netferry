package netmon

import (
	"fmt"
	"log"
	"syscall"
	"time"
	"unsafe"
)

// iffLowerUp is IFF_LOWER_UP (0x10000): physical carrier is present.
// Not defined in all Go versions' syscall package, so we define it locally.
const iffLowerUp = uint32(0x10000)

// Watch opens a netlink routing socket and blocks until a network change is
// detected (interface up/down, address add/remove, route change).
// Returns nil on the first relevant change, or an error if the socket fails.
func Watch(done <-chan struct{}) error {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW, syscall.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("netmon: open netlink socket: %w", err)
	}
	defer syscall.Close(fd)

	// Subscribe to link and address change groups only.
	// Route groups (RTNLGRP_IPV4_ROUTE, RTNLGRP_IPV6_ROUTE) are excluded
	// because they fire on routine route-table updates (nft/iptables rules,
	// tunnel's own setup) and cause false-positive reconnects.
	addr := &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Groups: (1 << (syscall.RTNLGRP_LINK - 1)) |
			(1 << (syscall.RTNLGRP_IPV4_IFADDR - 1)) |
			(1 << (syscall.RTNLGRP_IPV6_IFADDR - 1)),
	}
	if err := syscall.Bind(fd, addr); err != nil {
		return fmt.Errorf("netmon: bind netlink: %w", err)
	}

	// Skip network events during the first few seconds after startup to
	// avoid reacting to route changes caused by our own firewall setup.
	startup := time.Now()

	buf := make([]byte, 4096)
	for {
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

		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				continue
			}
			return fmt.Errorf("netmon: read netlink: %w", err)
		}
		if n < syscall.SizeofNlMsghdr {
			continue
		}

		// Ignore events during the grace period after startup.
		if time.Since(startup) < 5*time.Second {
			continue
		}

		// Iterate over all netlink messages in this datagram.
		// A single recvfrom may return multiple messages concatenated;
		// we must inspect each one before deciding to ignore the batch.
		data := buf[:n]
		for len(data) >= syscall.SizeofNlMsghdr {
			hdr := (*syscall.NlMsghdr)(unsafe.Pointer(&data[0]))
			msgLen := int(hdr.Len)
			if msgLen < syscall.SizeofNlMsghdr || msgLen > len(data) {
				break // truncated or malformed
			}
			payload := data[syscall.SizeofNlMsghdr:msgLen]
			if isRelevantChange(hdr.Type, payload) {
				log.Printf("netmon: network change detected (type=%d), signalling reconnect", hdr.Type)
				return nil
			}
			// Advance past this message, honouring NLMSG_ALIGN (4-byte boundary).
			aligned := (msgLen + 3) &^ 3
			if aligned >= len(data) {
				break
			}
			data = data[aligned:]
		}
	}
}

// isRelevantChange returns true when a netlink message represents a real
// network-connectivity change that warrants a tunnel reconnect.
//
// RTM_NEWLINK is filtered carefully: cloud hypervisors (e.g. AWS ENA) call
// rtmsg_ifinfo(RTM_NEWLINK, dev, 0) periodically to re-broadcast link state
// with ifi_change=0 — no flags actually changed.  Without filtering, these
// keepalives cause spurious reconnects roughly every 30 s on EC2 instances.
//
// We reconnect only when IFF_UP, IFF_RUNNING, or IFF_LOWER_UP changes,
// which covers administrative state changes and physical carrier events.
// IFF_PROMISC / IFF_ALLMULTI etc. change when Docker starts/stops containers
// and must NOT trigger a reconnect.
func isRelevantChange(msgType uint16, payload []byte) bool {
	switch msgType {
	case syscall.RTM_DELLINK, syscall.RTM_NEWADDR, syscall.RTM_DELADDR:
		return true
	case syscall.RTM_NEWLINK:
		if len(payload) < syscall.SizeofIfInfomsg {
			// Too short to parse — conservative: treat as relevant.
			return true
		}
		info := (*syscall.IfInfomsg)(unsafe.Pointer(&payload[0]))
		// ifi_change is the bitmask of IFF_* flags that changed.
		// By convention, ifi_change=0 means re-announcement with no change.
		const connectivity = uint32(syscall.IFF_UP) | uint32(syscall.IFF_RUNNING) | iffLowerUp
		return info.Change&connectivity != 0
	}
	return false
}
