package mobile

import (
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"
)

// PlatformCallback is implemented by the native side (Swift/Kotlin).
// Each method uses only gomobile-compatible types.
type PlatformCallback interface {
	// ProtectSocket marks a socket fd so the OS doesn't route it back through
	// the VPN tunnel. Required on Android (VpnService.protect); iOS can no-op.
	ProtectSocket(fd int32) bool

	// OnStateChange is called when the engine state changes.
	// state is one of: "disconnected", "connecting", "connected", "error".
	OnStateChange(state string)

	// OnLog delivers a log line from the tunnel engine.
	OnLog(msg string)

	// OnStats delivers periodic stats as a JSON string.
	OnStats(statsJSON string)
}

// State constants.
const (
	StateDisconnected = "disconnected"
	StateConnecting   = "connecting"
	StateConnected    = "connected"
	StateError        = "error"
)

// Engine is the main entry point for the mobile tunnel.
// Create with NewEngine, call Start to connect, Stop to disconnect.
type Engine struct {
	callback PlatformCallback

	mu       sync.Mutex
	state    string
	tunnel   *tunnelSession
	stopCh   chan struct{}
	stoppedW sync.WaitGroup
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
	if e.state == StateConnecting || e.state == StateConnected {
		e.mu.Unlock()
		return fmt.Errorf("engine already running")
	}
	e.stopCh = make(chan struct{})
	e.setState(StateConnecting)
	e.mu.Unlock()

	// Set up logging to callback.
	log.SetFlags(0)
	log.SetPrefix("c : ")
	log.SetOutput(&callbackLogWriter{cb: e.callback})

	// Create and start tunnel session.
	session, err := newTunnelSession(cfg, e.callback, e.stopCh)
	if err != nil {
		e.setState(StateError)
		return fmt.Errorf("tunnel setup: %w", err)
	}

	e.mu.Lock()
	e.tunnel = session
	e.setState(StateConnected)
	e.mu.Unlock()

	// Start stats reporting loop.
	e.stoppedW.Add(1)
	go e.statsLoop()

	// Wait for tunnel to end.
	e.stoppedW.Add(1)
	go func() {
		defer e.stoppedW.Done()
		session.Wait()
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.state == StateConnected {
			e.setState(StateDisconnected)
		}
	}()

	return nil
}

// StartWithTUN is like Start but also reads IP packets from the given TUN fd
// and forwards TCP/DNS through the tunnel using a userspace TCP/IP stack.
//
// Use this on Android where VpnService.establish() returns a TUN fd.
// On iOS, use Start() instead — traffic is routed via NEProxySettings.
func (e *Engine) StartWithTUN(configJSON string, tunFD int32) error {
	if err := e.Start(configJSON); err != nil {
		return err
	}

	e.mu.Lock()
	session := e.tunnel
	e.mu.Unlock()

	if session == nil {
		return fmt.Errorf("tunnel not started")
	}

	cfg, _ := parseConfig(configJSON)
	fwd, err := newTunForwarder(tunFD, cfg.MTU, session.tunnelClient(), session.counters)
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
	e.setState(StateDisconnected)
	e.mu.Unlock()

	e.stoppedW.Wait()
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
