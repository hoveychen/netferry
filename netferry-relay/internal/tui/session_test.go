package tui

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/stats"
)

var errDrop = errors.New("drop")

type fakeEngine struct {
	c        *stats.Counters
	ready    chan struct{}
	readyOK  bool
	runErr   chan error // Run returns what is sent here (or nil on stop)
	released atomic.Bool
	closed   atomic.Bool
}

func newFake(readyOK bool) *fakeEngine {
	return &fakeEngine{c: stats.NewCounters(), ready: make(chan struct{}), readyOK: readyOK, runErr: make(chan error, 1)}
}

func (f *fakeEngine) Counters() *stats.Counters { return f.c }
func (f *fakeEngine) ReadyCh() <-chan struct{}  { return f.ready }
func (f *fakeEngine) ReadyOK() bool             { return f.readyOK }
func (f *fakeEngine) Close()                    { f.closed.Store(true) }
func (f *fakeEngine) ReleaseFirewall()          { f.released.Store(true) }
func (f *fakeEngine) Run(stop <-chan struct{}) error {
	if f.readyOK {
		close(f.ready)
	}
	select {
	case err := <-f.runErr:
		if !f.readyOK {
			close(f.ready)
		}
		return err
	case <-stop:
		return nil
	}
}

type harness struct {
	t       *testing.T
	mu      sync.Mutex
	engines []*fakeEngine
	next    func() (*fakeEngine, error)
	s       *Session
	states  chan Status
}

func newHarness(t *testing.T) *harness {
	ReconnectInterval, countdownStep = 30*time.Millisecond, 10*time.Millisecond
	h := &harness{t: t, states: make(chan Status, 256)}
	h.next = func() (*fakeEngine, error) { return newFake(true), nil }
	h.s = NewSession(func(ConnectSpec) (Engine, error) {
		e, err := h.next()
		if err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.engines = append(h.engines, e)
		h.mu.Unlock()
		return e, nil
	}, func(err error) bool { return errors.Is(err, errDrop) }, func(ev Event) {
		if ev.State != nil {
			h.states <- ev.State.Status
		}
	})
	return h
}

func (h *harness) waitFor(want Status) {
	h.t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case st := <-h.states:
			if st == want {
				return
			}
		case <-deadline:
			h.t.Fatalf("timed out waiting for %s (now %s)", want, h.s.State().Status)
		}
	}
}

func (h *harness) engine(i int) *fakeEngine {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.engines[i]
}

func spec() ConnectSpec { return ConnectSpec{Profile: profile.Profile{ID: "p1"}} }

func TestSessionAppliesRulesAndReconnects(t *testing.T) {
	h := newHarness(t)
	h.s.SetRules(Rules{
		Priorities: map[string]int{"a.com": 5},
		Routes:     stats.RouteTable{Overrides: map[string]stats.RouteMode{"*.b.com": {Kind: stats.RouteDirect}}},
	})
	if err := h.s.Connect(spec()); err != nil {
		t.Fatal(err)
	}
	h.waitFor(StatusConnected)
	e0 := h.engine(0)
	if got := e0.c.LookupRouteMode("", "a.b.com"); got.Kind != stats.RouteDirect {
		t.Fatalf("routes not applied before run: %+v", e0.c.RouteTable())
	}
	if e0.c.Priorities()["a.com"] != 5 {
		t.Fatalf("priorities not applied: %+v", e0.c.Priorities())
	}
	if err := h.s.Connect(spec()); err == nil {
		t.Fatal("second Connect while running should fail")
	}

	// Live rule change reaches the running engine.
	h.s.SetRules(Rules{Routes: stats.RouteTable{Groups: []stats.RouteGroup{{Domains: []string{"x.com"}, Route: stats.RouteMode{Kind: stats.RouteBlocked}}}}})
	if e0.c.LookupRouteMode("", "x.com").Kind != stats.RouteBlocked {
		t.Fatal("live SetRules not applied")
	}

	// Link drops → reconnecting → a fresh engine, which gets the latest rules.
	e0.runErr <- errDrop
	h.waitFor(StatusReconnecting)
	h.waitFor(StatusConnected)
	e1 := h.engine(1)
	if e1.c.LookupRouteMode("", "www.x.com").Kind != stats.RouteBlocked {
		t.Fatal("reconnected engine missing rules")
	}
	if !e0.closed.Load() {
		t.Fatal("dropped engine not closed")
	}
	if e0.released.Load() {
		t.Fatal("kept firewall must be handed to the next engine, not released")
	}

	h.s.Disconnect()
	h.waitFor(StatusDisconnected)
	if !e1.closed.Load() {
		t.Fatal("engine not closed on disconnect")
	}
}

func TestSessionFirstConnectFailureIsErrorWithoutRetry(t *testing.T) {
	h := newHarness(t)
	h.next = func() (*fakeEngine, error) { return nil, errors.New("ssh: connect to host x: refused") }
	h.s.Connect(spec())
	h.waitFor(StatusError)
	time.Sleep(80 * time.Millisecond)
	if st := h.s.State(); st.Status != StatusError || st.Message == "" {
		t.Fatalf("state = %+v", st)
	}
}

func TestSessionDisconnectDuringCountdownReleasesFirewall(t *testing.T) {
	h := newHarness(t)
	ReconnectInterval = time.Hour // stay in the countdown
	h.s.Connect(spec())
	h.waitFor(StatusConnected)
	e0 := h.engine(0)
	e0.runErr <- errDrop
	h.waitFor(StatusReconnecting)
	h.s.Disconnect()
	h.waitFor(StatusDisconnected)
	deadline := time.Now().Add(time.Second)
	for !e0.released.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !e0.released.Load() {
		t.Fatal("firewall kept for reconnect was not released after giving up")
	}
}

func TestObserveLogTracksDeployAndErrors(t *testing.T) {
	h := newHarness(t)
	h.s.ObserveLog("c : deploy-reason: size-mismatch remote=1 local=2")
	h.s.ObserveLog("c : deploy-progress: 512/2048")
	h.s.ObserveLog("c : ssh: connect to host 1.2.3.4 port 22: Connection refused")
	h.s.ObserveLog("c : Accept TCP: ok")
	st := h.s.State()
	if st.DeployReason != "size-mismatch" || st.DeploySent != 512 || st.DeployTotal != 2048 {
		t.Fatalf("deploy: %+v", st)
	}
	if len(st.Errors) != 1 {
		t.Fatalf("errors: %+v", st.Errors)
	}
}
