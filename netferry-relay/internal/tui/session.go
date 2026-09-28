package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/stats"
	"github.com/hoveychen/netferry/relay/internal/store"
)

// Engine is the slice of cmd/tunnel's *Engine the session drives. It lives
// behind an interface because Engine is in package main.
type Engine interface {
	Counters() *stats.Counters
	ReadyCh() <-chan struct{}
	ReadyOK() bool
	Run(stop <-chan struct{}) error
	Close()
	ReleaseFirewall()
}

// ConnectSpec is what the user asked to connect: one profile (solo) or the
// active group, whose seed is its first child — the same seed the desktop
// passes to the tunnel.
type ConnectSpec struct {
	Profile  profile.Profile
	Group    *store.Group
	Children []profile.Profile
	Settings store.GlobalSettings
}

// Starter builds and returns an engine for spec (not yet running).
type Starter func(ConnectSpec) (Engine, error)

type Status string

const (
	StatusDisconnected Status = "disconnected"
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
	StatusReconnecting Status = "reconnecting"
	StatusError        Status = "error"
)

// ReconnectInterval matches the desktop's fixed retry cadence (a var so
// tests can shorten it).
var ReconnectInterval = 5 * time.Second

// countdownStep is how often the reconnect countdown message updates.
var countdownStep = time.Second

// Rules is what the desktop pushes to /priorities, /routes and /group. The
// session hands it to every engine it creates, before Run, and live.
type Rules struct {
	Priorities map[string]int
	Routes     map[string]store.RouteMode // already compiled (rule groups + overrides)
	Group      *stats.ActiveGroup         // nil in solo mode
}

// TunnelError is one error-looking log line, shown on the Errors tab and in
// the connection-error dialog.
type TunnelError struct {
	Message string
	At      time.Time
}

// errorKeywords is the desktop's is_error_line list (sidecar.rs).
var errorKeywords = []string{
	"fatal:", "warning:", "connection refused", "connection reset",
	"no route to host", "ssh: connect to host", "permission denied",
	"host key verification failed",
}

const maxTunnelErrors = 50

// Event is delivered to the session's sink on every state change and every
// stats message from the running engine.
type Event struct {
	// State is set on status/message/deploy changes.
	State *SessionState
	// Exactly one of the following is set for engine stream messages.
	Stats         *stats.Snapshot
	Conn          *stats.ConnEvent
	ConnSnapshot  []stats.ConnEvent
	DestSnapshot  []stats.DestinationSnapshot
	ConnSnapReset bool // ConnSnapshot is authoritative (may be empty)
}

// SessionState is an immutable snapshot of the session for rendering.
type SessionState struct {
	Status       Status
	Message      string
	Spec         *ConnectSpec
	Attempt      int
	DeployReason string
	DeploySent   int64
	DeployTotal  int64
	Errors       []TunnelError // newest last
}

// Session owns the connect → ready → drop → reconnect lifecycle.
type Session struct {
	start       Starter
	sink        func(Event)
	isReconnect func(error) bool

	qmu    sync.Mutex
	queue  []Event
	qready chan struct{} // 1-buffered wakeup for the pump

	mu     sync.Mutex
	state  SessionState
	rules  Rules
	eng    Engine
	stopCh chan struct{} // closes to stop the current run loop
	gen    int           // bumps on every Connect/Disconnect; stale loops exit
}

// NewSession returns an idle session. sink is called from background
// goroutines; isReconnect reports whether an engine error means "the link
// dropped, recreate me" (ErrExitForReconnect).
func NewSession(start Starter, isReconnect func(error) bool, sink func(Event)) *Session {
	s := &Session{
		start:       start,
		sink:        sink,
		isReconnect: isReconnect,
		state:       SessionState{Status: StatusDisconnected},
		qready:      make(chan struct{}, 1),
	}
	go s.pump()
	return s
}

// emit queues ev for in-order delivery to the sink. Never blocks, so it is
// safe to call with s.mu held even if the sink calls back into the session.
func (s *Session) emit(ev Event) {
	s.qmu.Lock()
	s.queue = append(s.queue, ev)
	s.qmu.Unlock()
	select {
	case s.qready <- struct{}{}:
	default:
	}
}

func (s *Session) pump() {
	for range s.qready {
		for {
			s.qmu.Lock()
			if len(s.queue) == 0 {
				s.qmu.Unlock()
				break
			}
			batch := s.queue
			s.queue = nil
			s.qmu.Unlock()
			for _, ev := range batch {
				s.sink(ev)
			}
		}
	}
}

// State returns a copy of the current state.
func (s *Session) State() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copyStateLocked()
}

func (s *Session) copyStateLocked() SessionState {
	st := s.state
	st.Errors = append([]TunnelError(nil), s.state.Errors...)
	return st
}

// Active reports whether a tunnel is running or being brought up.
func (s *Session) Active() bool {
	switch s.State().Status {
	case StatusConnecting, StatusConnected, StatusReconnecting:
		return true
	}
	return false
}

// Counters returns the running engine's counters, or nil.
func (s *Session) Counters() *stats.Counters {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng == nil {
		return nil
	}
	return s.eng.Counters()
}

func (s *Session) emitStateLocked() {
	st := s.copyStateLocked()
	s.emit(Event{State: &st})
}

func (s *Session) setLocked(status Status, msg string) {
	s.state.Status = status
	s.state.Message = msg
	s.emitStateLocked()
}

// SetRules replaces the rules and applies them to the running engine.
func (s *Session) SetRules(r Rules) {
	s.mu.Lock()
	s.rules = r
	eng := s.eng
	s.mu.Unlock()
	if eng != nil {
		applyRules(eng.Counters(), r)
	}
}

func applyRules(c *stats.Counters, r Rules) {
	prios := map[string]int{}
	for k, v := range r.Priorities {
		prios[k] = v
	}
	c.SetPriorities(prios)
	routes := make(map[string]stats.RouteMode, len(r.Routes))
	for k, v := range r.Routes {
		routes[k] = stats.RouteMode{Kind: stats.RouteKind(v.Kind), ProfileID: v.ProfileID}
	}
	c.SetRouteModes(routes)
	c.SetActiveGroup(r.Group)
}

// Connect starts a new session. It fails if one is already running.
func (s *Session) Connect(spec ConnectSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state.Status {
	case StatusConnecting, StatusConnected, StatusReconnecting:
		return errors.New("a tunnel is already running")
	}
	s.gen++
	gen := s.gen
	stop := make(chan struct{})
	s.stopCh = stop
	sp := spec
	s.state = SessionState{Status: StatusConnecting, Message: "Starting tunnel…", Spec: &sp}
	s.emitStateLocked()
	go s.loop(gen, sp, stop)
	return nil
}

// Disconnect stops the tunnel (or an in-progress reconnect countdown).
func (s *Session) Disconnect() {
	s.mu.Lock()
	if s.stopCh != nil {
		close(s.stopCh)
		s.stopCh = nil
	}
	s.gen++
	running := s.eng != nil
	s.mu.Unlock()
	if !running {
		// No engine will report back; settle the state here.
		s.mu.Lock()
		if s.state.Status != StatusError {
			s.setLocked(StatusDisconnected, "Disconnected")
		}
		s.mu.Unlock()
	}
}

// ObserveLog feeds one tunnel log line to the session so it can track deploy
// progress and collect error lines, as sidecar.rs does with stderr.
func (s *Session) ObserveLog(line string) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "c :"))
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	if rest, ok := strings.CutPrefix(trimmed, "deploy-reason: "); ok {
		reason := strings.Fields(rest)
		if len(reason) > 0 {
			s.state.DeployReason = reason[0]
			changed = true
		}
	}
	if rest, ok := strings.CutPrefix(trimmed, "deploy-progress: "); ok {
		var sent, total int64
		if _, err := fmt.Sscanf(rest, "%d/%d", &sent, &total); err == nil {
			s.state.DeploySent, s.state.DeployTotal = sent, total
			changed = true
		}
	}
	lower := strings.ToLower(line)
	for _, kw := range errorKeywords {
		if strings.Contains(lower, kw) {
			s.state.Errors = append(s.state.Errors, TunnelError{Message: strings.TrimSpace(line), At: time.Now()})
			if len(s.state.Errors) > maxTunnelErrors {
				s.state.Errors = s.state.Errors[len(s.state.Errors)-maxTunnelErrors:]
			}
			changed = true
			break
		}
	}
	if changed {
		s.emitStateLocked()
	}
}

// current reports whether gen is still the live generation.
func (s *Session) currentLocked(gen int) bool { return s.gen == gen }

// loop runs engines until the user stops or a first connect fails.
func (s *Session) loop(gen int, spec ConnectSpec, stop <-chan struct{}) {
	everConnected := false
	attempt := 0
	var lastEng Engine // engine whose firewall may still be installed

	release := func() {
		if lastEng != nil {
			lastEng.ReleaseFirewall()
			lastEng = nil
		}
	}

	for {
		eng, err := s.start(spec)
		if err != nil {
			if !everConnected {
				release()
				s.finish(gen, StatusError, err.Error())
				return
			}
			attempt++
			s.update(gen, StatusReconnecting, fmt.Sprintf("Reconnect failed (attempt #%d): %v", attempt, err), attempt)
			if !s.countdown(gen, stop, attempt+1) {
				release()
				return
			}
			continue
		}

		s.mu.Lock()
		if !s.currentLocked(gen) {
			s.mu.Unlock()
			eng.Close()
			release()
			return
		}
		s.eng = eng
		rules := s.rules
		s.mu.Unlock()
		applyRules(eng.Counters(), rules)
		unsubscribe := s.forward(eng.Counters())

		engStop := make(chan struct{})
		done := make(chan error, 1)
		go func() { done <- eng.Run(engStop) }()

		var runErr error
		ended, readyNow := false, false
		select {
		case <-eng.ReadyCh():
			if eng.ReadyOK() {
				readyNow = true
				lastEng = nil // the new engine owns the firewall now
				everConnected = true
				attempt = 0
				s.update(gen, StatusConnected, "Tunnel established", 0)
			}
		case runErr = <-done:
			ended = true
		case <-stop:
		}
		if !ended {
			select {
			case runErr = <-done:
			case <-stop:
				close(engStop)
				runErr = <-done
			}
		}
		unsubscribe()
		eng.Close()
		s.mu.Lock()
		if s.eng == eng {
			s.eng = nil
		}
		s.mu.Unlock()

		userStopped := isClosed(stop)
		dropped := s.isReconnect(runErr)
		if dropped {
			lastEng = eng // rules kept installed until the next engine replaces them
		}
		if userStopped {
			release()
			s.finish(gen, StatusDisconnected, "Disconnected")
			return
		}
		if !everConnected {
			release()
			msg := "Tunnel process exited unexpectedly"
			if runErr != nil {
				msg = runErr.Error()
			}
			s.finish(gen, StatusError, msg)
			return
		}
		// Was connected and the link went away (or a reconnect attempt died
		// before becoming ready): keep retrying on the fixed cadence.
		attempt++
		if readyNow || dropped {
			s.update(gen, StatusReconnecting, "Network lost, waiting to reconnect…", attempt)
		} else {
			msg := "tunnel exited"
			if runErr != nil {
				msg = runErr.Error()
			}
			s.update(gen, StatusReconnecting, fmt.Sprintf("Reconnect failed (attempt #%d): %s", attempt, msg), attempt)
		}
		if !s.countdown(gen, stop, attempt) {
			release()
			return
		}
	}
}

// countdown ticks the reconnect status each second. Returns false if the
// user stopped (the state is then settled to disconnected).
func (s *Session) countdown(gen int, stop <-chan struct{}, attempt int) bool {
	for left := ReconnectInterval; left > 0; left -= countdownStep {
		s.update(gen, StatusReconnecting, fmt.Sprintf("Reconnecting in %ds (attempt #%d)…", int((left+time.Second-1)/time.Second), attempt), attempt)
		select {
		case <-stop:
			s.finish(gen, StatusDisconnected, "Disconnected")
			return false
		case <-time.After(countdownStep):
		}
	}
	s.update(gen, StatusReconnecting, fmt.Sprintf("Reconnecting now (attempt #%d)…", attempt), attempt)
	return true
}

func (s *Session) update(gen int, st Status, msg string, attempt int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.currentLocked(gen) {
		return
	}
	s.state.Attempt = attempt
	if st == StatusConnected {
		s.state.DeployReason, s.state.DeploySent, s.state.DeployTotal = "", 0, 0
	}
	s.setLocked(st, msg)
}

// finish settles a terminal state. A stale generation still settles when the
// session is not already running something newer (Disconnect bumps gen).
func (s *Session) finish(gen int, st Status, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.currentLocked(gen) && s.stopCh != nil {
		return // a newer Connect owns the state
	}
	s.state.Attempt = 0
	s.setLocked(st, msg)
}

// forward pumps the engine's SSE-equivalent stream into the sink as typed
// events. Returns the unsubscribe func.
func (s *Session) forward(c *stats.Counters) func() {
	ch, cancel := c.Subscribe()
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-quit:
				return
			case msg := <-ch:
				if ev, ok := parseSSE(msg); ok {
					s.emit(ev)
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			close(quit)
		})
	}
}

// parseSSE decodes one "event: X\ndata: Y\n\n" message.
func parseSSE(msg string) (Event, bool) {
	var name, data string
	for _, line := range strings.Split(msg, "\n") {
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			name = v
		} else if v, ok := strings.CutPrefix(line, "data: "); ok {
			data = v
		}
	}
	var ev Event
	var err error
	switch name {
	case "stats":
		ev.Stats = &stats.Snapshot{}
		err = json.Unmarshal([]byte(data), ev.Stats)
	case "connection":
		ev.Conn = &stats.ConnEvent{}
		err = json.Unmarshal([]byte(data), ev.Conn)
	case "connections_snapshot":
		ev.ConnSnapReset = true
		err = json.Unmarshal([]byte(data), &ev.ConnSnapshot)
	case "destinations_snapshot":
		err = json.Unmarshal([]byte(data), &ev.DestSnapshot)
		if ev.DestSnapshot == nil {
			ev.DestSnapshot = []stats.DestinationSnapshot{}
		}
	default:
		return ev, false
	}
	return ev, err == nil
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
