// netferry-tunnel is the local sidecar that:
//   1. Connects to the remote host via SSH
//   2. Deploys netferry-server if not already present (version-cached)
//   3. Sets up local firewall rules (pf on macOS, nft/iptables on Linux)
//   4. Runs a transparent TCP proxy + optional DNS proxy via the mux protocol
//
// Log output is designed to be parsed by the Tauri sidecar.rs monitor.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hoveychen/netferry/relay/internal/deploy"
	"github.com/hoveychen/netferry/relay/internal/firewall"
	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/proxy"
	"github.com/hoveychen/netferry/relay/internal/sshconn"
)

var Version = "dev"

func main() {
	log.SetFlags(0)
	log.SetPrefix("c : ")

	// ── CLI flags ────────────────────────────────────────────────────────────
	var (
		remote       = flag.String("remote", "", "SSH target: [user@]host[:port]")
		identity     = flag.String("identity", "", "SSH private key path")
		autoNets     = flag.Bool("auto-nets", false, "add remote routes to proxy subnets")
		dns          = flag.Bool("dns", false, "intercept DNS requests")
		dnsTarget    = flag.String("dns-target", "", "remote DNS server IP[@port]")
		method       = flag.String("method", "auto", "firewall method: auto|pf|nft|ipt")
		noIPv6       = flag.Bool("no-ipv6", false, "disable IPv6 handling")
		verbose      = flag.Bool("v", false, "verbose logging")
		extraSSHOpts = flag.String("extra-ssh-opts", "", "extra SSH options")
		showVersion  = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	subnets := flag.Args()

	if *showVersion {
		fmt.Println(Version)
		os.Exit(0)
	}
	if *remote == "" {
		fmt.Fprintln(os.Stderr, "fatal: --remote is required")
		flag.Usage()
		os.Exit(1)
	}

	_ = noIPv6 // IPv6 support reserved for Phase 2

	if !*verbose {
		// Keep stderr output, but suppress extra debug noise.
		log.SetOutput(os.Stderr)
	}

	// ── SSH config resolution ────────────────────────────────────────────────
	hc, err := sshconn.ParseSSHConfig(*remote)
	if err != nil {
		fatalf("ssh config: %v", err)
	}

	ac := sshconn.AuthConfig{
		IdentityFile: *identity,
		ExtraOptions: *extraSSHOpts,
	}

	// ── SSH connection ───────────────────────────────────────────────────────
	log.Printf("connecting to %s@%s:%d", hc.User, hc.HostName, hc.Port)
	sshClient, err := sshconn.Dial(hc, ac)
	if err != nil {
		fatalf("ssh connect: %v", err)
	}
	defer sshClient.Close()

	// SSH server IP must be excluded from firewall rules to prevent loop.
	sshServerIP := deploy.RemoteIP(sshClient)
	excludes := []string{
		sshServerIP + "/32",
		"127.0.0.0/8",
		"169.254.0.0/16",
	}

	// ── Deploy server binary ─────────────────────────────────────────────────
	remotePath, err := deploy.EnsureServer(sshClient, Version)
	if err != nil {
		fatalf("deploy server: %v", err)
	}
	log.Printf("remote server: %s", remotePath)

	// ── Start remote server session ──────────────────────────────────────────
	sess, err := sshClient.NewSession()
	if err != nil {
		fatalf("new ssh session: %v", err)
	}
	defer sess.Close()

	var serverArgs []string
	if *autoNets {
		serverArgs = append(serverArgs, "--auto-nets")
	}
	if *dnsTarget != "" {
		serverArgs = append(serverArgs, "--to-ns", *dnsTarget)
	}
	if *verbose {
		serverArgs = append(serverArgs, "--verbose")
	}

	remoteCmd := remotePath
	if len(serverArgs) > 0 {
		remoteCmd += " " + strings.Join(serverArgs, " ")
	}

	sessStdin, err := sess.StdinPipe()
	if err != nil {
		fatalf("session stdin: %v", err)
	}
	sessStdout, err := sess.StdoutPipe()
	if err != nil {
		fatalf("session stdout: %v", err)
	}
	sess.Stderr = os.Stderr

	if err := sess.Start(remoteCmd); err != nil {
		fatalf("start remote server: %v", err)
	}

	// ── Read sync header ─────────────────────────────────────────────────────
	if err := mux.ReadSyncHeader(sessStdout); err != nil {
		fatalf("server handshake: %v — is the deployed binary corrupted?", err)
	}

	// ── Start mux client ─────────────────────────────────────────────────────
	muxClient := mux.NewMuxClient(sessStdout, sessStdin)
	muxErrCh := make(chan error, 1)
	go func() {
		muxErrCh <- muxClient.Run()
	}()

	// Collect CMD_ROUTES if --auto-nets (arrives within ~200ms of connect).
	var autoNetRoutes []string
	if *autoNets {
		select {
		case routes := <-muxClient.RoutesCh():
			autoNetRoutes = routes
			log.Printf("auto-nets: %d routes received", len(autoNetRoutes))
		case <-time.After(5 * time.Second):
			log.Printf("auto-nets: timeout waiting for routes")
		case err := <-muxErrCh:
			fatalf("mux: %v", err)
		}
	} else {
		// Drain the empty CMD_ROUTES sent by the server unconditionally.
		select {
		case <-muxClient.RoutesCh():
		case <-time.After(3 * time.Second):
		case err := <-muxErrCh:
			fatalf("mux: %v", err)
		}
	}

	effectiveSubnets := append(subnets, autoNetRoutes...)
	if len(effectiveSubnets) == 0 {
		fatalf("no subnets to proxy — specify at least one CIDR (e.g. 0.0.0.0/0)")
	}

	// ── Firewall setup ───────────────────────────────────────────────────────
	firewall.CleanStaleAnchors()

	var fw firewall.Method
	if *method == "auto" {
		fw = firewall.NewAuto()
	} else {
		fw, err = firewall.New(*method)
		if err != nil {
			fatalf("firewall: %v", err)
		}
	}
	log.Printf("firewall: using %s", fw.Name())

	var dnsServers []string
	dnsPort := 0
	if *dns {
		dnsServers = proxy.DetectDNSServers()
		dnsPort = mustPickFreePort("udp")
		log.Printf("DNS: servers=%v localPort=%d", dnsServers, dnsPort)
	}

	proxyPort := mustPickFreePort("tcp")

	if err := fw.Setup(effectiveSubnets, excludes, proxyPort, dnsPort, dnsServers); err != nil {
		fatalf("firewall setup: %v", err)
	}

	// Ensure firewall cleanup on any exit path.
	defer fw.Restore()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		s := <-sig
		log.Printf("received signal %v, cleaning up", s)
		fw.Restore()
		os.Exit(0)
	}()

	// ── Signal tunnel is ready (Tauri sidecar.rs watches for this exact line) ─
	fmt.Fprintln(os.Stderr, "c : Connected to server.")

	// ── Start DNS proxy ───────────────────────────────────────────────────────
	if *dns {
		go func() {
			if err := proxy.ListenDNS(dnsPort, muxClient); err != nil {
				log.Printf("DNS proxy: %v", err)
			}
		}()
	}

	// ── Start TCP proxy (transparent on Unix, SOCKS5 on Windows) ─────────────
	proxyErrCh := make(chan error, 1)
	go func() {
		proxyErrCh <- proxy.ListenTransparent(proxyPort, muxClient)
	}()

	select {
	case err := <-muxErrCh:
		if err != nil {
			log.Printf("mux closed: %v", err)
		}
	case err := <-proxyErrCh:
		if err != nil {
			log.Printf("proxy closed: %v", err)
		}
	}
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "fatal: "+format+"\n", args...)
	os.Exit(1)
}

func mustPickFreePort(network string) int {
	switch network {
	case "tcp":
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fatalf("pick free TCP port: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		return port
	case "udp":
		ln, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			fatalf("pick free UDP port: %v", err)
		}
		port := ln.LocalAddr().(*net.UDPAddr).Port
		ln.Close()
		return port
	default:
		panic("unknown network: " + network)
	}
}
