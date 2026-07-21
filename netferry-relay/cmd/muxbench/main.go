// muxbench is a standalone single-stream throughput probe for the netferry
// mux transport. It reproduces the EXACT production data path — smux over an
// SSH channel to the real cmd/server binary — WITHOUT any firewall / TUN /
// gVisor layer, so a single-stream measurement isolates the mux-over-SSH
// transport itself (optionally with a jump hop to add SSH-over-SSH nesting).
//
// Diagnostic tool only; not part of the shipped tunnel.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/sshconn"

	"golang.org/x/crypto/ssh"
)

func main() {
	mode := flag.String("mode", "client", "client | sink")
	listen := flag.String("listen", ":9999", "sink: TCP listen addr")

	remote := flag.String("remote", "", "client: [user@]host[:port] of the mux server node")
	identity := flag.String("identity", "", "client: SSH private key path")
	jump := flag.String("jump", "", "client: optional jump host [user@]host[:port] (adds SSH-over-SSH nesting)")
	jumpID := flag.String("jump-identity", "", "client: jump SSH key path")
	serverCmd := flag.String("server", "/tmp/nfbench-server", "client: remote netferry-server binary path")
	sink := flag.String("sink", "127.0.0.1:9999", "client: dst addr the server dials (the sink)")
	mb := flag.Int("mb", 100, "client: megabytes to upload through one stream")
	split := flag.Bool("split", false, "client: use split-conn mode (separate data/ctrl SSH sessions)")
	flag.Parse()

	if *mode == "sink" {
		runSink(*listen)
		return
	}

	u, h, p := parseRemote(*remote)
	hc := &sshconn.HostConfig{User: u, HostName: h, Port: p, IdentityFile: *identity}
	ac := sshconn.AuthConfig{IdentityFile: *identity}
	var jumps []sshconn.JumpHostSpec
	if *jump != "" {
		jumps = append(jumps, sshconn.JumpHostSpec{Remote: *jump, IdentityFile: *jumpID})
	}

	sc, _, err := sshconn.Dial(hc, ac, jumps...)
	if err != nil {
		log.Fatalf("ssh dial: %v", err)
	}
	defer sc.Close()

	var c *mux.MuxClient
	if *split {
		c, err = buildSplit(sc, hc, ac, jumps, *serverCmd)
	} else {
		c, err = buildPlain(sc, *serverCmd)
	}
	if err != nil {
		log.Fatalf("build mux client: %v", err)
	}
	go c.Run()

	sinkHost, sinkPortStr, err := net.SplitHostPort(*sink)
	if err != nil {
		log.Fatalf("bad -sink %q: %v", *sink, err)
	}
	sinkPort, _ := strconv.Atoi(sinkPortStr)

	conn, err := c.OpenTCP(2, sinkHost, sinkPort, 3)
	if err != nil {
		log.Fatalf("OpenTCP: %v", err)
	}

	total := int64(*mb) * 1024 * 1024
	start := time.Now()
	n, err := io.CopyN(conn, zeroReader{}, total)
	_ = conn.CloseWrite()
	el := time.Since(start)
	if err != nil && err != io.EOF {
		log.Printf("copy error after %d bytes: %v", n, err)
	}
	mbps := float64(n) / 1e6 / el.Seconds()
	fmt.Printf("RESULT mode=%s split=%v jump=%q bytes=%d dur=%.2fs %.2f MB/s %.1f Mbit/s\n",
		labelMode(*split, *jump), *split, *jump, n, el.Seconds(), mbps, mbps*8)
}

func labelMode(split bool, jump string) string {
	nest := "direct"
	if jump != "" {
		nest = "jumped"
	}
	s := "plain"
	if split {
		s = "split"
	}
	return nest + "-" + s
}

// ── sink ────────────────────────────────────────────────────────────────────

func runSink(addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("sink listen %s: %v", addr, err)
	}
	fmt.Printf("sink listening on %s (discarding all bytes)\n", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			io.Copy(io.Discard, c)
		}(conn)
	}
}

// ── client mux builders (mirror cmd/tunnel/muxbuilder.go) ─────────────────────

func buildPlain(sc *ssh.Client, serverCmd string) (*mux.MuxClient, error) {
	sess, err := sc.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout: %w", err)
	}
	sess.Stderr = os.Stderr
	if err := sess.Start(serverCmd); err != nil {
		return nil, fmt.Errorf("start server: %w", err)
	}
	if err := mux.ReadSyncHeader(stdout); err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}
	return mux.NewMuxClient(stdout, stdin), nil
}

func buildSplit(sc *ssh.Client, hc *sshconn.HostConfig, ac sshconn.AuthConfig, jumps []sshconn.JumpHostSpec, serverCmd string) (*mux.MuxClient, error) {
	sid := newSessionID()

	dataSess, err := sc.NewSession()
	if err != nil {
		return nil, fmt.Errorf("data session: %w", err)
	}
	dataStdin, err := dataSess.StdinPipe()
	if err != nil {
		return nil, err
	}
	dataStdout, err := dataSess.StdoutPipe()
	if err != nil {
		return nil, err
	}
	dataSess.Stderr = os.Stderr
	if err := dataSess.Start(serverCmd + " --session-id " + sid + " --role main"); err != nil {
		return nil, fmt.Errorf("data start: %w", err)
	}

	ctrlClient, _, err := sshconn.Dial(hc, ac, jumps...)
	if err != nil {
		return nil, fmt.Errorf("ctrl dial: %w", err)
	}
	ctrlSess, err := ctrlClient.NewSession()
	if err != nil {
		return nil, err
	}
	ctrlStdin, err := ctrlSess.StdinPipe()
	if err != nil {
		return nil, err
	}
	ctrlStdout, err := ctrlSess.StdoutPipe()
	if err != nil {
		return nil, err
	}
	ctrlSess.Stderr = os.Stderr
	base := serverCmd
	if i := strings.IndexByte(serverCmd, ' '); i >= 0 {
		base = serverCmd[:i]
	}
	if err := ctrlSess.Start(base + " --session-id " + sid + " --role ctrl"); err != nil {
		return nil, fmt.Errorf("ctrl start: %w", err)
	}

	var syncErr [2]error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); syncErr[0] = mux.ReadSyncHeader(dataStdout) }()
	go func() { defer wg.Done(); syncErr[1] = mux.ReadSyncHeader(ctrlStdout) }()
	wg.Wait()
	if syncErr[0] != nil {
		return nil, fmt.Errorf("data handshake: %w", syncErr[0])
	}
	if syncErr[1] != nil {
		return nil, fmt.Errorf("ctrl handshake: %w", syncErr[1])
	}
	return mux.NewMuxClientSplit(dataStdout, dataStdin, ctrlStdout, ctrlStdin), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

type zeroReader struct{}

func (zeroReader) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = 0
	}
	return len(b), nil
}

func newSessionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("rand: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func parseRemote(s string) (user, host string, port int) {
	user, port = "root", 22
	if i := strings.LastIndex(s, "@"); i >= 0 {
		user = s[:i]
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		host = s[:i]
		port, _ = strconv.Atoi(s[i+1:])
	} else {
		host = s
	}
	return
}
