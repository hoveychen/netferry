package mobile

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoveychen/netferry/relay/internal/deploy"
	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/sshconn"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// tunnelSession holds the state for an active tunnel connection.
type tunnelSession struct {
	cfg      *Config
	callback PlatformCallback
	counters *stats.Counters

	sshClients []*ssh.Client
	muxClients []*mux.MuxClient
	tunnel     mux.TunnelClient
	stack      *tunStack

	tunFwd *tunForwarder // non-nil when StartWithTUN is used (Android)

	mu     sync.Mutex
	stopCh chan struct{}
	doneWg sync.WaitGroup

	// Rate tracking (updated each Stats() call).
	prevRx int64
	prevTx int64
}

func newTunnelSession(cfg *Config, callback PlatformCallback, stopCh chan struct{}) (*tunnelSession, error) {
	s := &tunnelSession{
		cfg:      cfg,
		callback: callback,
		counters: stats.NewCounters(),
		stopCh:   stopCh,
	}

	// ── SSH connection ──────────────────────────────────────────────────────
	hc, err := sshconn.ParseSSHConfig(cfg.Remote)
	if err != nil {
		return nil, fmt.Errorf("ssh config: %w", err)
	}

	if cfg.Fectun.Enabled() {
		hc.Fectun = cfg.Fectun
		log.Printf("first hop over fectun (udp port %d)", cfg.Fectun.Port)
	}

	ac := sshconn.AuthConfig{
		IdentityPEM: cfg.IdentityKey,
	}

	// Parse jump hosts.
	var jumpHosts []sshconn.JumpHostSpec
	for _, jh := range cfg.JumpHosts {
		jumpHosts = append(jumpHosts, sshconn.JumpHostSpec{
			Remote:      jh.Remote,
			IdentityPEM: jh.IdentityKey,
		})
	}

	log.Printf("connecting to %s@%s:%d", hc.User, hc.HostName, hc.Port)

	// NOTE: Socket protection (SetDialFunc) must be set by the caller (Engine)
	// before calling newTunnelSession so it persists across reconnections.

	sshClient, _, err := sshconn.Dial(hc, ac, jumpHosts...)
	if err != nil {
		return nil, fmt.Errorf("ssh connect: %w", err)
	}
	s.sshClients = append(s.sshClients, sshClient)

	// Additional pool connections.
	for i := 1; i < cfg.PoolSize; i++ {
		extra, _, err := sshconn.Dial(hc, ac, jumpHosts...)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("ssh pool %d/%d: %w", i+1, cfg.PoolSize, err)
		}
		s.sshClients = append(s.sshClients, extra)
	}

	// SSH keepalive.
	for _, sc := range s.sshClients {
		sshconn.StartSSHKeepalive(sc, 30*time.Second, nil)
	}

	// ── Deploy server ───────────────────────────────────────────────────────
	remotePath, err := deploy.EnsureServer(sshClient, version)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("deploy server: %w", err)
	}
	log.Printf("remote server: %s", remotePath)

	// ── Build server command ────────────────────────────────────────────────
	remoteCmd := remotePath
	if cfg.AutoNets {
		remoteCmd += " --auto-nets"
	}
	if cfg.DNSTarget != "" {
		remoteCmd += " --to-ns " + cfg.DNSTarget
	}

	// ── Start mux clients ───────────────────────────────────────────────────
	// Snapshot the data connections: in split mode each one spawns an extra
	// ctrl SSH connection that is appended to s.sshClients below, so we must
	// not range over the slice while it grows.
	dataClients := s.sshClients
	muxErrCh := make(chan error, 1)
	for _, sc := range dataClients {
		var mc *mux.MuxClient
		if cfg.SplitConn {
			var ctrlClient *ssh.Client
			mc, ctrlClient, err = startSplitMuxClient(sc, hc, ac, jumpHosts, remoteCmd)
			if err == nil {
				// Track the ctrl connection so Close() tears it down.
				// Its keepalive is already started inside startSplitMuxClient.
				s.sshClients = append(s.sshClients, ctrlClient)
			}
		} else {
			mc, err = startMuxClient(sc, remoteCmd)
		}
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("mux client: %w", err)
		}
		mc.SetCounters(s.counters)
		s.muxClients = append(s.muxClients, mc)
	}

	if len(s.muxClients) == 1 {
		s.tunnel = s.muxClients[0]
		s.doneWg.Add(1)
		go func() {
			defer s.doneWg.Done()
			if err := s.muxClients[0].Run(); err != nil {
				log.Printf("mux closed: %v", err)
			}
			select {
			case muxErrCh <- fmt.Errorf("mux session ended"):
			default:
			}
		}()
	} else {
		strategy := mux.LBLeastLoaded
		if cfg.TCPBalanceMode == "round-robin" {
			strategy = mux.LBRoundRobin
		}
		pool := mux.NewMuxPoolWithStrategy(s.muxClients, strategy)
		s.tunnel = pool
		for i := range s.muxClients {
			idx := i
			s.doneWg.Add(1)
			go func() {
				defer s.doneWg.Done()
				if err := s.muxClients[idx].Run(); err != nil {
					log.Printf("mux pool member %d closed: %v", idx+1, err)
				}
			}()
		}
	}

	// Drain routes from first client.
	firstClient := s.muxClients[0]
	select {
	case <-firstClient.RoutesCh():
	case <-time.After(3 * time.Second):
	}

	// ── Start SOCKS5 proxy + DNS relay ──────────────────────────────────────
	stack, err := newTunStack(cfg, s.tunnel, s.counters)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("local proxy: %w", err)
	}
	s.stack = stack

	log.Println("Connected to server.")
	return s, nil
}

func (s *tunnelSession) tunnelClient() mux.TunnelClient {
	return s.tunnel
}

func (s *tunnelSession) setTunForwarder(fwd *tunForwarder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tunFwd = fwd
}

func (s *tunnelSession) Close() {
	s.mu.Lock()
	fwd := s.tunFwd
	s.mu.Unlock()
	if fwd != nil {
		fwd.Close()
	}
	if s.stack != nil {
		s.stack.Close()
	}
	for _, sc := range s.sshClients {
		sc.Close()
	}
	s.doneWg.Wait()
}

func (s *tunnelSession) Wait() {
	s.doneWg.Wait()
}

// closeSSH closes the underlying SSH clients without tearing down the
// tunForwarder/stack. The next AcceptStream on the smux session errors,
// MuxClient.Run() returns, doneWg drains, and reconnectWatcher's Wait()
// unblocks — driving the existing reconnect path on demand instead of
// waiting for the smux KeepAliveTimeout to fire.
func (s *tunnelSession) closeSSH() {
	for _, sc := range s.sshClients {
		sc.Close()
	}
}

// Stats returns a snapshot of current tunnel statistics.
// Rate fields are computed as deltas since the last call.
func (s *tunnelSession) Stats() statsSnapshot {
	curRx := s.counters.RxTotal.Load()
	curTx := s.counters.TxTotal.Load()

	rxRate := curRx - s.prevRx
	txRate := curTx - s.prevTx
	s.prevRx = curRx
	s.prevTx = curTx

	return statsSnapshot{
		RxBytesPerSec: rxRate,
		TxBytesPerSec: txRate,
		TotalRxBytes:  curRx,
		TotalTxBytes:  curTx,
		ActiveConns:   s.counters.ActiveTCP.Load(),
		TotalConns:    s.counters.TotalTCP.Load(),
		DNSQueries:    s.counters.DNSTotal.Load(),
	}
}

func startMuxClient(sc *ssh.Client, remoteCmd string) (*mux.MuxClient, error) {
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
	if err := sess.Start(remoteCmd); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	if err := mux.ReadSyncHeader(stdout); err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}
	return mux.NewMuxClient(stdout, stdin), nil
}

// startSplitMuxClient opens a split-conn MuxClient: bulk data (PSH/FIN) travels
// on a session over the existing connection sc, while control frames
// (SYN/NOP/UPD) plus fully-ctrl-routed streams (DNS) travel on a second,
// freshly-dialed SSH connection.  Separating them keeps DNS resolution and new
// stream setup responsive even while a bulk transfer saturates the data
// connection.  Mirrors the desktop tunnel's trySplitMuxClient.
//
// Returns the ctrl SSH client so the caller can track it for teardown; its
// keepalive is started here.
func startSplitMuxClient(sc *ssh.Client, hc *sshconn.HostConfig, ac sshconn.AuthConfig, jumpHosts []sshconn.JumpHostSpec, remoteCmd string) (*mux.MuxClient, *ssh.Client, error) {
	sid, err := newSplitSessionID()
	if err != nil {
		return nil, nil, err
	}

	// ── data session (bulk PSH/FIN) over the existing connection ──────────────
	dataSess, err := sc.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("split data session: %w", err)
	}
	dataStdin, err := dataSess.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("split data stdin: %w", err)
	}
	dataStdout, err := dataSess.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("split data stdout: %w", err)
	}
	dataSess.Stderr = os.Stderr
	if err := dataSess.Start(remoteCmd + " --session-id " + sid + " --role main"); err != nil {
		return nil, nil, fmt.Errorf("split data start: %w", err)
	}

	// ── ctrl session (SYN/NOP/UPD + DNS) over a fresh SSH connection ──────────
	ctrlClient, _, err := sshconn.Dial(hc, ac, jumpHosts...)
	if err != nil {
		return nil, nil, fmt.Errorf("split ctrl dial: %w", err)
	}
	sshconn.StartSSHKeepalive(ctrlClient, 30*time.Second, nil)

	ctrlSess, err := ctrlClient.NewSession()
	if err != nil {
		ctrlClient.Close()
		return nil, nil, fmt.Errorf("split ctrl session: %w", err)
	}
	ctrlStdin, err := ctrlSess.StdinPipe()
	if err != nil {
		ctrlClient.Close()
		return nil, nil, fmt.Errorf("split ctrl stdin: %w", err)
	}
	ctrlStdout, err := ctrlSess.StdoutPipe()
	if err != nil {
		ctrlClient.Close()
		return nil, nil, fmt.Errorf("split ctrl stdout: %w", err)
	}
	ctrlSess.Stderr = os.Stderr
	// The ctrl relay only needs the server binary path — strip data-role args.
	ctrlCmd := remoteCmd
	if i := strings.IndexByte(remoteCmd, ' '); i >= 0 {
		ctrlCmd = remoteCmd[:i]
	}
	if err := ctrlSess.Start(ctrlCmd + " --session-id " + sid + " --role ctrl"); err != nil {
		ctrlClient.Close()
		return nil, nil, fmt.Errorf("split ctrl start: %w", err)
	}

	// ── read both sync headers concurrently ───────────────────────────────────
	var syncErr [2]error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); syncErr[0] = mux.ReadSyncHeader(dataStdout) }()
	go func() { defer wg.Done(); syncErr[1] = mux.ReadSyncHeader(ctrlStdout) }()
	wg.Wait()
	if syncErr[0] != nil {
		ctrlClient.Close()
		return nil, nil, fmt.Errorf("split data handshake: %w", syncErr[0])
	}
	if syncErr[1] != nil {
		ctrlClient.Close()
		return nil, nil, fmt.Errorf("split ctrl handshake: %w", syncErr[1])
	}

	return mux.NewMuxClientSplit(dataStdout, dataStdin, ctrlStdout, ctrlStdin), ctrlClient, nil
}

// newSplitSessionID returns a short random hex ID used to pair the data and
// ctrl SSH sessions of a split-conn mux client.
func newSplitSessionID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
