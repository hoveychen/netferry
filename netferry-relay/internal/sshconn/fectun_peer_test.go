package sshconn

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A reconnect storm with a stale key must cost one bootstrap, not one per
// pool member.
func TestFectunPeerRefreshSingleFlight(t *testing.T) {
	p := &fectunPeer{key: "old", gen: 1}
	_, seen := p.current()
	var boots atomic.Int32
	boot := func() (string, error) {
		boots.Add(1)
		time.Sleep(20 * time.Millisecond)
		return "new", nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, _, err := p.refresh(seen, boot)
			if err != nil || key != "new" {
				t.Errorf("got %q, %v", key, err)
			}
		}()
	}
	wg.Wait()
	if n := boots.Load(); n != 1 {
		t.Fatalf("bootstraps = %d, want 1", n)
	}
	// A caller that already saw the new key and still failed bootstraps again.
	_, seen = p.current()
	if _, _, err := p.refresh(seen, boot); err != nil || boots.Load() != 2 {
		t.Fatalf("second refresh: boots=%d err=%v", boots.Load(), err)
	}
}

func TestFectunPeerRefreshFirstTime(t *testing.T) {
	p := &fectunPeer{}
	key, gen, err := p.refresh(0, func() (string, error) { return "k", nil })
	if err != nil || key != "k" || gen != 1 {
		t.Fatalf("got %q gen=%d err=%v", key, gen, err)
	}
}
