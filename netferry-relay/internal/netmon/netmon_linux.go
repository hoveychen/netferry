package netmon

import (
	"fmt"
	"log"
	"syscall"
	"unsafe"
)

// Watch opens a netlink routing socket and blocks until a network change is
// detected (interface up/down, address add/remove, route change).
// Returns nil on the first relevant change, or an error if the socket fails.
func Watch(done <-chan struct{}) error {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW, syscall.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("netmon: open netlink socket: %w", err)
	}
	defer syscall.Close(fd)

	// Subscribe to link, address, and route change groups.
	addr := &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Groups: (1 << (syscall.RTNLGRP_LINK - 1)) |
			(1 << (syscall.RTNLGRP_IPV4_IFADDR - 1)) |
			(1 << (syscall.RTNLGRP_IPV6_IFADDR - 1)) |
			(1 << (syscall.RTNLGRP_IPV4_ROUTE - 1)) |
			(1 << (syscall.RTNLGRP_IPV6_ROUTE - 1)),
	}
	if err := syscall.Bind(fd, addr); err != nil {
		return fmt.Errorf("netmon: bind netlink: %w", err)
	}

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

		// Parse netlink message header.
		hdr := (*syscall.NlMsghdr)(unsafe.Pointer(&buf[0]))
		if isRelevantChange(hdr.Type) {
			log.Printf("netmon: network change detected (type=%d), signalling reconnect", hdr.Type)
			return nil
		}
	}
}

func isRelevantChange(msgType uint16) bool {
	switch msgType {
	case syscall.RTM_NEWLINK, syscall.RTM_DELLINK,
		syscall.RTM_NEWADDR, syscall.RTM_DELADDR,
		syscall.RTM_NEWROUTE, syscall.RTM_DELROUTE:
		return true
	}
	return false
}
