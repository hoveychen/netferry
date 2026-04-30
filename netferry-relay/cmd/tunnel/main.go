// netferry-tunnel is the local sidecar that:
//   1. Connects to the remote host via SSH
//   2. Deploys netferry-server if not already present (version-cached)
//   3. Sets up local firewall rules (pf on macOS, nft/iptables on Linux)
//   4. Runs a transparent TCP proxy + optional DNS/UDP proxy via the mux protocol
//
// Log output is designed to be parsed by the Tauri sidecar.rs monitor.
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hoveychen/netferry/relay/internal/logfile"
)

var Version = "dev"

// serverStderr is the writer used for remote server stderr output.
// It is set up in main() to tee to both os.Stderr and the server log file.
var serverStderr io.Writer = os.Stderr

func main() {
	log.SetFlags(0)
	log.SetPrefix("c : ")

	cfg, earlyExit := parseAndBuildConfig(os.Args[1:])
	if earlyExit {
		return
	}

	if !cfg.Verbose {
		// Keep stderr output, but suppress extra debug noise.
		log.SetOutput(os.Stderr)
	}

	// ── Log files (client.log + server.log with size-based rotation) ────────
	if logDir, err := os.UserCacheDir(); err == nil {
		logDir = filepath.Join(logDir, "netferry", "logs")
		if cw, err := logfile.New(filepath.Join(logDir, "client.log"), logfile.DefaultMaxSize, logfile.DefaultMaxBackups); err == nil {
			log.SetOutput(io.MultiWriter(log.Writer(), cw))
		}
		if sw, err := logfile.New(filepath.Join(logDir, "server.log"), logfile.DefaultMaxSize, logfile.DefaultMaxBackups); err == nil {
			serverStderr = io.MultiWriter(os.Stderr, sw)
		}
	}

	eng, err := NewEngine(cfg)
	if err != nil {
		fatalf("%v", err)
	}

	stopCh := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		s := <-sig
		log.Printf("received signal %v, cleaning up", s)
		close(stopCh)
	}()

	if err := eng.Run(stopCh); err != nil && !errors.Is(err, ErrExitForReconnect) {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "fatal: "+format+"\n", args...)
	os.Exit(1)
}
