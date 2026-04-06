package mobile

import (
	"fmt"
	"log"
	"net"
	"os"
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

	// Hook socket protection on Android.
	if callback != nil {
		sshconn.SetDialFunc(func(network, addr string, timeout time.Duration) (net.Conn, error) {
			return protectedDial(network, addr, timeout, callback)
		})
		defer sshconn.SetDialFunc(nil)
	}

	sshClient, err := sshconn.Dial(hc, ac, jumpHosts...)
	if err != nil {
		return nil, fmt.Errorf("ssh connect: %w", err)
	}
	s.sshClients = append(s.sshClients, sshClient)

	// Additional pool connections.
	for i := 1; i < cfg.PoolSize; i++ {
		extra, err := sshconn.Dial(hc, ac, jumpHosts...)
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
	remotePath, err := deploy.EnsureServer(sshClient, Version)
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
	muxErrCh := make(chan error, 1)
	for _, sc := range s.sshClients {
		mc, err := startMuxClient(sc, remoteCmd)
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
