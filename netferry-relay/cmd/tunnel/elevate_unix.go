//go:build darwin || linux

package main

import (
	"fmt"
	"os"
	"strings"
)

// requireRoot fatals when the current process is not running as root. The
// firewall methods used on darwin/linux (pf, nft, ipt, tproxy) all require
// privileged operations (open /dev/pf, load nft/ipt rules, bind tproxy).
// Failing here yields a clearer message than the deep "permission denied"
// surfaced from fw.Setup() much later in the connection lifecycle.
//
// reason is included in the error so the message distinguishes CLI vs TUI.
func requireRoot(reason string) {
	if os.Geteuid() == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "fatal: %s needs root privileges to install firewall rules.\n", reason)
	fmt.Fprintf(os.Stderr, "       re-run with sudo, e.g.\n")
	fmt.Fprintf(os.Stderr, "         sudo %s\n", strings.Join(os.Args, " "))
	fmt.Fprintf(os.Stderr, "       (or pass --method=socks5 to run a local SOCKS5 proxy without elevation).\n")
	os.Exit(1)
}
