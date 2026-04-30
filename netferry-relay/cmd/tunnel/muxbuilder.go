package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoveychen/netferry/relay/internal/mux"
	"github.com/hoveychen/netferry/relay/internal/sshconn"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

// newSessionID returns a short random hex string used to coordinate the data
// and ctrl SSH sessions in split-conn mode.
func newSessionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("rand: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// startSplitMuxClient is the fatal wrapper around trySplitMuxClient for
// initial connection setup.
func startSplitMuxClient(
	sc *ssh.Client,
	hc *sshconn.HostConfig,
	ac sshconn.AuthConfig,
	jumpHosts []sshconn.JumpHostSpec,
	remoteCmd string,
	member, total int,
) *mux.MuxClient {
	c, err := trySplitMuxClient(sc, hc, ac, jumpHosts, remoteCmd, member, total)
	if err != nil {
		fatalf("%v", err)
	}
	return c
}

// tryMuxClient creates a non-split MuxClient on the given SSH connection.
func tryMuxClient(sc *ssh.Client, remoteCmd string, member, total int) (*mux.MuxClient, error) {
	sess, err := sc.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new ssh session %d/%d: %w", member, total, err)
	}
	sessStdin, err := sess.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("session %d stdin: %w", member, err)
	}
	sessStdout, err := sess.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("session %d stdout: %w", member, err)
	}
	sess.Stderr = serverStderr
	if err := sess.Start(remoteCmd); err != nil {
		return nil, fmt.Errorf("start remote server (session %d): %w", member, err)
	}
	if err := mux.ReadSyncHeader(sessStdout); err != nil {
		return nil, fmt.Errorf("server handshake (session %d): %w", member, err)
	}
	return mux.NewMuxClient(sessStdout, sessStdin), nil
}

// trySplitMuxClient opens two SSH sessions (data + ctrl) and returns a
// split-conn MuxClient. Returns an error instead of calling fatalf so it
// can be used in reconnection loops.
func trySplitMuxClient(
	sc *ssh.Client,
	hc *sshconn.HostConfig,
	ac sshconn.AuthConfig,
	jumpHosts []sshconn.JumpHostSpec,
	remoteCmd string,
	member, total int,
) (*mux.MuxClient, error) {
	sid := newSessionID()

	// ── data session ─────────────────────────────────────────────────────────
	dataSess, err := sc.NewSession()
	if err != nil {
		return nil, fmt.Errorf("split data session %d/%d: %w", member, total, err)
	}
	dataStdin, err := dataSess.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("split data session %d/%d stdin: %w", member, total, err)
	}
	dataStdout, err := dataSess.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("split data session %d/%d stdout: %w", member, total, err)
	}
	dataSess.Stderr = serverStderr

	dataCmd := remoteCmd + " --session-id " + sid + " --role main"
	if err := dataSess.Start(dataCmd); err != nil {
		return nil, fmt.Errorf("split data session %d/%d start: %w", member, total, err)
	}

	// ── ctrl session ──────────────────────────────────────────────────────────
	ctrlClient, err := sshconn.Dial(hc, ac, jumpHosts...)
	if err != nil {
		return nil, fmt.Errorf("split ctrl SSH connect %d/%d: %w", member, total, err)
	}
	sshconn.StartSSHKeepalive(ctrlClient, 30*time.Second, nil)

	ctrlSess, err := ctrlClient.NewSession()
	if err != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split ctrl session %d/%d: %w", member, total, err)
	}
	ctrlStdin, err := ctrlSess.StdinPipe()
	if err != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split ctrl session %d/%d stdin: %w", member, total, err)
	}
	ctrlStdout, err := ctrlSess.StdoutPipe()
	if err != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split ctrl session %d/%d stdout: %w", member, total, err)
	}
	ctrlSess.Stderr = serverStderr

	ctrlCmd := remoteCmd
	if idx := strings.IndexByte(remoteCmd, ' '); idx >= 0 {
		ctrlCmd = remoteCmd[:idx]
	}
	ctrlCmd += " --session-id " + sid + " --role ctrl"
	if err := ctrlSess.Start(ctrlCmd); err != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split ctrl session %d/%d start: %w", member, total, err)
	}

	// ── read sync headers concurrently ────────────────────────────────────────
	var syncErr [2]error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		syncErr[0] = mux.ReadSyncHeader(dataStdout)
	}()
	go func() {
		defer wg.Done()
		syncErr[1] = mux.ReadSyncHeader(ctrlStdout)
	}()
	wg.Wait()

	if syncErr[0] != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split data handshake %d/%d: %w", member, total, syncErr[0])
	}
	if syncErr[1] != nil {
		ctrlClient.Close()
		return nil, fmt.Errorf("split ctrl handshake %d/%d: %w", member, total, syncErr[1])
	}

	return mux.NewMuxClientSplit(dataStdout, dataStdin, ctrlStdout, ctrlStdin), nil
}

// connectPoolMember dials a fresh SSH connection and creates a MuxClient.
// Used by reconnectPoolMember for reconnecting dead pool members. The existing
// TunnelCounters pointer is passed in so cumulative counts survive the reconnect.
func connectPoolMember(
	hc *sshconn.HostConfig,
	ac sshconn.AuthConfig,
	jumpHosts []sshconn.JumpHostSpec,
	remoteCmd string,
	split bool,
	counters *stats.Counters,
	tc *stats.TunnelCounters,
	member, total int,
) (*mux.MuxClient, error) {
	sc, err := sshconn.Dial(hc, ac, jumpHosts...)
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}
	sshconn.StartSSHKeepalive(sc, 30*time.Second, buildRTTCallback(counters, tc, false))

	var c *mux.MuxClient
	if split {
		c, err = trySplitMuxClient(sc, hc, ac, jumpHosts, remoteCmd, member, total)
	} else {
		c, err = tryMuxClient(sc, remoteCmd, member, total)
	}
	if err != nil {
		sc.Close()
		return nil, err
	}
	c.SetCounters(counters)
	if tc != nil {
		c.SetTunnelIndex(member, tc)
	}
	return c, nil
}

// maxReconnectAttempts is the number of consecutive reconnect failures before
// a pool member gives up and signals a fatal error (triggering a full tunnel
// restart via Tauri).
const maxReconnectAttempts = 10

// reconnectPoolMember runs the given MuxClient and, when it dies, reconnects
// with exponential backoff and replaces it in the pool. If reconnection fails
// maxReconnectAttempts times in a row, it sends a fatal error on fatalCh so
// the process can exit and let Tauri restart the whole tunnel.
func reconnectPoolMember(
	pool *mux.MuxPool,
	idx, total int,
	initial *mux.MuxClient,
	hc *sshconn.HostConfig,
	ac sshconn.AuthConfig,
	jumpHosts []sshconn.JumpHostSpec,
	remoteCmd string,
	split bool,
	counters *stats.Counters,
	tc *stats.TunnelCounters,
	fatalCh chan<- error,
) {
	c := initial
	backoff := 5 * time.Second
	const maxBackoff = 60 * time.Second

	if tc != nil {
		tc.SetState(stats.TunnelAlive)
	}

	for {
		if err := c.Run(); err != nil {
			log.Printf("mux pool member %d/%d closed: %v", idx+1, total, err)
		}

		if tc != nil {
			tc.SetState(stats.TunnelReconnecting)
		}

		// Reconnect loop with exponential backoff.
		attempts := 0
		for {
			attempts++
			if attempts > maxReconnectAttempts {
				log.Printf("mux pool member %d/%d: giving up after %d failed reconnect attempts", idx+1, total, maxReconnectAttempts)
				if tc != nil {
					tc.SetState(stats.TunnelDead)
				}
				select {
				case fatalCh <- fmt.Errorf("pool member %d/%d: exhausted %d reconnect attempts", idx+1, total, maxReconnectAttempts):
				default:
				}
				return
			}

			log.Printf("mux pool member %d/%d: reconnecting in %v (attempt %d/%d)", idx+1, total, backoff, attempts, maxReconnectAttempts)
			time.Sleep(backoff)

			newClient, err := connectPoolMember(hc, ac, jumpHosts, remoteCmd, split, counters, tc, idx+1, total)
			if err != nil {
				log.Printf("mux pool member %d/%d reconnect failed: %v", idx+1, total, err)
				backoff = min(backoff*2, maxBackoff)
				continue
			}

			pool.ReplaceClient(idx, newClient)
			c = newClient
			backoff = 5 * time.Second
			if tc != nil {
				tc.SetState(stats.TunnelAlive)
			}
			log.Printf("mux pool member %d/%d: reconnected successfully", idx+1, total)
			break
		}
	}
}

// buildRTTCallback returns a keepalive RTT observer that records measurements
// in both the per-tunnel counter (if non-nil) and, when primary is true, the
// global counter (for backward-compatible display when pool size == 1).
func buildRTTCallback(counters *stats.Counters, tc *stats.TunnelCounters, primary bool) func(time.Duration) {
	return func(rtt time.Duration) {
		if tc != nil {
			tc.ObserveRTT(rtt)
		}
		if primary {
			counters.ObserveKeepaliveRTT(rtt)
		}
	}
}
