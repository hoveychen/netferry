package mux

import (
	"fmt"
	"sync"

	"github.com/hoveychen/netferry/relay/internal/stats"
)

// SessionManager dispatches connections across multiple MuxPools, one per
// profile in the active group. It satisfies TunnelClient by delegating to the
// default pool so DNS/UDP keep working transparently; TCP callers that care
// about per-destination routing call PoolFor instead of OpenTCP.
type SessionManager struct {
	mu        sync.RWMutex
	pools     map[string]*MuxPool
	defaultID string
	counters  *stats.Counters
}

// NewSessionManager creates an empty manager. Register pools and set a default
// before the listener starts handling connections.
func NewSessionManager(counters *stats.Counters) *SessionManager {
	return &SessionManager{
		pools:    make(map[string]*MuxPool),
		counters: counters,
	}
}

// Register adds or replaces the pool for a profile id.
func (sm *SessionManager) Register(profileID string, pool *MuxPool) {
	sm.mu.Lock()
	sm.pools[profileID] = pool
	if sm.defaultID == "" {
		sm.defaultID = profileID
	}
	sm.mu.Unlock()
}

// SetDefault marks a previously-registered profile as the default.
func (sm *SessionManager) SetDefault(profileID string) {
	sm.mu.Lock()
	if _, ok := sm.pools[profileID]; ok {
		sm.defaultID = profileID
	}
	sm.mu.Unlock()
}

// DefaultID returns the profile id designated as default.
func (sm *SessionManager) DefaultID() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.defaultID
}

// DefaultPool returns the pool for the default profile, or nil if none.
func (sm *SessionManager) DefaultPool() *MuxPool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.pools[sm.defaultID]
}

// PoolFor resolves the pool for a TCP destination using the route rules stored
// in the stats counters. Falls back to the default pool when the destination
// has no specific rule or the rule references an unregistered profile.
func (sm *SessionManager) PoolFor(dstAddr, host string) (string, *MuxPool) {
	if sm.counters != nil {
		rm := sm.counters.LookupRouteMode(dstAddr, host)
		if rm.Kind == stats.RouteTunnel && rm.ProfileID != "" {
			sm.mu.RLock()
			if pool, ok := sm.pools[rm.ProfileID]; ok {
				defer sm.mu.RUnlock()
				return rm.ProfileID, pool
			}
			sm.mu.RUnlock()
		}
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.defaultID, sm.pools[sm.defaultID]
}

// OpenTCP satisfies TunnelClient by delegating to the default pool. Callers
// that care about per-destination routing should use PoolFor instead — this
// method has no destination-to-profile context and always picks default.
func (sm *SessionManager) OpenTCP(family int, dstIP string, dstPort int, priority int) (*ClientConn, error) {
	pool := sm.DefaultPool()
	if pool == nil {
		return nil, fmt.Errorf("sessionmanager: no default pool")
	}
	return pool.OpenTCP(family, dstIP, dstPort, priority)
}

// DNSRequest satisfies TunnelClient by delegating to the default pool.
func (sm *SessionManager) DNSRequest(data []byte) ([]byte, error) {
	pool := sm.DefaultPool()
	if pool == nil {
		return nil, fmt.Errorf("sessionmanager: no default pool")
	}
	return pool.DNSRequest(data)
}

// OpenUDP satisfies TunnelClient by delegating to the default pool.
func (sm *SessionManager) OpenUDP(family int) (*UDPChannel, error) {
	pool := sm.DefaultPool()
	if pool == nil {
		return nil, fmt.Errorf("sessionmanager: no default pool")
	}
	return pool.OpenUDP(family)
}
