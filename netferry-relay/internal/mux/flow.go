package mux

import (
	"encoding/binary"
	"sync"
)

// sendWindow tracks the remaining send credit for a single mux channel.
// The sender calls Acquire before transmitting data; the receiver calls
// Release when it has consumed data and is ready for more.
type sendWindow struct {
	mu   sync.Mutex
	cond *sync.Cond
	win  int64
	dead bool
}

func newSendWindow(initial int64) *sendWindow {
	sw := &sendWindow{win: initial}
	sw.cond = sync.NewCond(&sw.mu)
	return sw
}

// Acquire blocks until n bytes of window are available.
// Returns false if the window was killed (channel closed).
func (sw *sendWindow) Acquire(n int) bool {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	for sw.win < int64(n) && !sw.dead {
		sw.cond.Wait()
	}
	if sw.dead {
		return false
	}
	sw.win -= int64(n)
	return true
}

// Release adds credit to the window and wakes a blocked Acquire.
func (sw *sendWindow) Release(credit int64) {
	sw.mu.Lock()
	sw.win += credit
	sw.mu.Unlock()
	sw.cond.Broadcast()
}

// Kill unblocks all waiting Acquire calls.
func (sw *sendWindow) Kill() {
	sw.mu.Lock()
	sw.dead = true
	sw.mu.Unlock()
	sw.cond.Broadcast()
}

// EncodeWindowUpdate encodes a credit value into a 4-byte big-endian payload.
func EncodeWindowUpdate(credit int64) []byte {
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, uint32(credit))
	return data
}

// DecodeWindowUpdate decodes a 4-byte big-endian credit value.
func DecodeWindowUpdate(data []byte) int64 {
	if len(data) < 4 {
		return 0
	}
	return int64(binary.BigEndian.Uint32(data))
}
