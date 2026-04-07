package mobile

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/hoveychen/netferry/relay/internal/sshconn"
)

// PlatformCallback is implemented by the native side (Swift/Kotlin).
// Each method uses only gomobile-compatible types.
type PlatformCallback interface {
	// ProtectSocket marks a socket fd so the OS doesn't route it back through
	// the VPN tunnel. Required on Android (VpnService.protect); iOS can no-op.
	ProtectSocket(fd int32) bool

	// OnStateChange is called when the engine state changes.
	// state is one of: "disconnected", "connecting", "connected", "reconnecting", "error".
	OnStateChange(state string)

	// OnLog delivers a log line from the tunnel engine.
	OnLog(msg string)

	// OnStats delivers periodic stats as a JSON string.
	OnStats(statsJSON string)

	// OnPortsChanged is called after a successful reconnection when the local
	// SOCKS5/DNS proxy ports have changed. iOS must call setTunnelNetworkSettings
	// with the new ports; Android can no-op (uses TUN forwarder directly).
	OnPortsChanged(socksPort int32, dnsPort int32)
}

// State constants.
const (
	StateDisconnected  = "disconnected"
	StateConnecting    = "connecting"
	StateConnected     = "connected"
	StateReconnecting  = "reconnecting"
	StateError         = "error"
)

const reconnectInterval = 5 * time.Second

// Engine is the main entry point for the mobile tunnel.
// Create with NewEngine, call Start to connect, Stop to disconnect.
type Engine struct {
	callback PlatformCallback

	mu       sync.Mutex
	state    string
	tunnel   *tunnelSession
	stopCh   chan struct{}
	stoppedW sync.WaitGroup

	// Stored for reconnection.
	configJSON string
	cfg        *Config
	useTUN     bool
	tunFile    *os.File // non-nil for Android TUN mode; owned by Engine
}

// NewEngine creates a new engine with the given platform callback.
func NewEngine(callback PlatformCallback) *Engine {
	// Aggressive GC for mobile memory constraints (iOS NE has 50MB limit).
	debug.SetGCPercent(10)
	debug.SetMemoryLimit(40 * 1024 * 1024) // 40MB soft limit

	return &Engine{
		callback: callback,
		state:    StateDisconnected,
	}
}

// Start connects to the SSH server and starts local SOCKS5 + DNS proxy
// servers. After Start returns successfully, call GetSOCKSPort() and
// GetDNSPort() to learn which ports the native side should route traffic to
// via the TUN/VPN interface.
//
// configJSON is a JSON-serialized Config.
//
// This method blocks until the tunnel is established or an error occurs.
// Call Stop() from another goroutine to shut down.
func (e *Engine) Start(configJSON string) error {
	cfg, err := parseConfig(configJSON)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	e.mu.Lock()
	if e.state == StateConnecting || e.state == StateConnected || e.state == StateReconnecting {
		e.mu.Unlock()
		return fmt.Errorf("engine already running")
	}
	e.stopCh = make(chan struct{})
	e.configJSON = configJSON
	e.cfg = cfg
	e.setState(StateConnecting)
	e.mu.Unlock()

	// Set up logging to callback.
	log.SetFlags(0)
	log.SetPrefix("c : ")
	log.SetOutput(&callbackLogWriter{cb: e.callback})

	// Hook Android socket protection for SSH dialing.
	e.setDialFunc()

	// Create and start tunnel session.
	session, err := newTunnelSession(cfg, e.callback, e.stopCh)
	if err != nil {
		e.clearDialFunc()
		e.setState(StateError)
		return fmt.Errorf("tunnel setup: %w", err)
	}

	e.mu.Lock()
	e.tunnel = session
	e.setState(StateConnected)
	e.mu.Unlock()

	// Start stats reporting and reconnect watcher.
	e.stoppedW.Add(2)
	go e.statsLoop()
	go e.reconnectWatcher()

	return nil
}

// StartWithTUN is like Start but also reads IP packets from the given TUN fd
// and forwards TCP/DNS through the tunnel using a userspace TCP/IP stack.
//
// Use this on Android where VpnService.establish() returns a TUN fd.
// On iOS, use Start() instead — traffic is routed via NEProxySettings.
func (e *Engine) StartWithTUN(configJSON string, tunFD int32) error {
	// Wrap the fd in an os.File owned by the Engine. The Engine manages its
	// lifecycle; tunForwarder borrows it without closing.
	tunFile := os.NewFile(uintptr(tunFD), "tun")
	if tunFile == nil {
		return fmt.Errorf("invalid TUN fd %d", tunFD)
	}

	e.mu.Lock()
	e.useTUN = true
	e.tunFile = tunFile
	e.mu.Unlock()

	if err := e.Start(configJSON); err != nil {
		e.mu.Lock()
		e.tunFile = nil
		e.useTUN = false
		e.mu.Unlock()
		tunFile.Close()
		return err
	}

	e.mu.Lock()
	session := e.tunnel
	e.mu.Unlock()

	if session == nil {
		e.Stop()
		return fmt.Errorf("tunnel not started")
	}

	fwd, err := newTunForwarder(tunFile, e.cfg.MTU, session.tunnelClient(), session.counters)
	if err != nil {
		e.Stop()
		return fmt.Errorf("tun forwarder: %w", err)
	}
	session.setTunForwarder(fwd)
	return nil
}

// Stop shuts down the tunnel and releases all resources.
func (e *Engine) Stop() {
	e.mu.Lock()
	if e.stopCh != nil {
		select {
		case <-e.stopCh:
			// Already closed.
		default:
			close(e.stopCh)
		}
	}
	if e.tunnel != nil {
		e.tunnel.Close()
		e.tunnel = nil
	}
	tunFile := e.tunFile
	e.tunFile = nil
	e.useTUN = false
	e.setState(StateDisconnected)
	e.mu.Unlock()

	// Close TUN fd IMMEDIATELY so the OS tears down VPN routing right away,
	// before waiting for goroutines (which may take seconds during reconnect).
	if tunFile != nil {
		tunFile.Close()
	}

	e.stoppedW.Wait()
	e.clearDialFunc()
}

// GetState returns the current engine state.
func (e *Engine) GetState() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// GetSOCKSPort returns the local SOCKS5 proxy port.
// The native side should configure the TUN/VPN to route TCP traffic here.
// Returns 0 if the engine is not running.
func (e *Engine) GetSOCKSPort() int32 {
	e.mu.Lock()
	t := e.tunnel
	e.mu.Unlock()
	if t == nil || t.stack == nil {
		return 0
	}
	return int32(t.stack.SOCKSPort())
}

// GetDNSPort returns the local DNS relay port.
// The native side should configure the TUN/VPN to route DNS traffic here.
// Returns 0 if the engine is not running.
func (e *Engine) GetDNSPort() int32 {
	e.mu.Lock()
	t := e.tunnel
	e.mu.Unlock()
	if t == nil || t.stack == nil {
		return 0
	}
	return int32(t.stack.DNSPort())
}

// GetStats returns current tunnel statistics as a JSON string.
func (e *Engine) GetStats() string {
	e.mu.Lock()
	t := e.tunnel
	e.mu.Unlock()
	if t == nil {
		return "{}"
	}
	snap := t.Stats()
	data, _ := json.Marshal(snap)
	return string(data)
}

func (e *Engine) setState(s string) {
	e.state = s
	if e.callback != nil {
		e.callback.OnStateChange(s)
	}
}

// reconnectWatcher monitors the active tunnel session and triggers auto-
// reconnect when the session ends unexpectedly (not user-initiated Stop).
func (e *Engine) reconnectWatcher() {
	defer e.stoppedW.Done()
	for {
		e.mu.Lock()
		session := e.tunnel
		e.mu.Unlock()
		if session == nil {
			return
		}

		// Block until the tunnel session ends.
		session.Wait()

		// Check if this was a user-initiated stop.
		select {
		case <-e.stopCh:
			return
		default:
		}

		// Unexpected disconnect — clean up dead session and reconnect.
		log.Println("tunnel session ended unexpectedly, attempting reconnect...")
		e.mu.Lock()
		if e.tunnel != nil {
			e.tunnel.Close()
			e.tunnel = nil
		}
		e.setState(StateReconnecting)
		e.mu.Unlock()

		if !e.reconnectLoop() {
			return // stopped by user
		}
		// reconnectLoop succeeded — loop back to watch the new session.
	}
}

// reconnectLoop retries creating a new tunnel session with a fixed interval.
// Returns true on success, false if cancelled via stopCh.
func (e *Engine) reconnectLoop() bool {
	attempt := 0
	for {
		attempt++

		// Wait before retrying.
		select {
		case <-e.stopCh:
			return false
		case <-time.After(reconnectInterval):
		}

		log.Printf("reconnect attempt #%d", attempt)

		e.setDialFunc()
		session, err := newTunnelSession(e.cfg, e.callback, e.stopCh)
		if err != nil {
			log.Printf("reconnect attempt #%d failed: %v", attempt, err)
			continue
		}

		// Re-create TUN forwarder for Android if needed.
		e.mu.Lock()
		useTUN := e.useTUN
		tunFile := e.tunFile
		e.mu.Unlock()

		if useTUN && tunFile != nil {
			fwd, err := newTunForwarder(tunFile, e.cfg.MTU, session.tunnelClient(), session.counters)
			if err != nil {
				log.Printf("reconnect: tun forwarder: %v", err)
				session.Close()
				continue
			}
			session.setTunForwarder(fwd)
		}

		e.mu.Lock()
		e.tunnel = session
		e.setState(StateConnected)
		e.mu.Unlock()

		// Notify iOS of new SOCKS5/DNS ports (they change on reconnect).
		if !useTUN && e.callback != nil && session.stack != nil {
			e.callback.OnPortsChanged(
				int32(session.stack.SOCKSPort()),
				int32(session.stack.DNSPort()),
			)
		}

		log.Printf("reconnect succeeded on attempt #%d", attempt)
		return true
	}
}

// setDialFunc hooks Android socket protection for SSH dialing.
func (e *Engine) setDialFunc() {
	if e.callback != nil {
		sshconn.SetDialFunc(func(network, addr string, timeout time.Duration) (net.Conn, error) {
			return protectedDial(network, addr, timeout, e.callback)
		})
	}
}

func (e *Engine) clearDialFunc() {
	sshconn.SetDialFunc(nil)
}

func (e *Engine) statsLoop() {
	defer e.stoppedW.Done()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			stats := e.GetStats()
			if e.callback != nil {
				e.callback.OnStats(stats)
			}
		}
	}
}

// callbackLogWriter sends log output to the PlatformCallback.
type callbackLogWriter struct {
	cb PlatformCallback
}

func (w *callbackLogWriter) Write(p []byte) (int, error) {
	if w.cb != nil {
		w.cb.OnLog(string(p))
	}
	return len(p), nil
}
