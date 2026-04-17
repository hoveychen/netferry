//go:build linux

package netmon

import (
	"syscall"
	"testing"
	"unsafe"
)

// makeIfInfoPayload serialises an IfInfomsg into a byte slice.
func makeIfInfoPayload(flags, change uint32) []byte {
	info := syscall.IfInfomsg{
		Family: syscall.AF_UNSPEC,
		Type:   1, // ARPHRD_ETHER
		Index:  2,
		Flags:  flags,
		Change: change,
	}
	buf := make([]byte, syscall.SizeofIfInfomsg)
	*(*syscall.IfInfomsg)(unsafe.Pointer(&buf[0])) = info
	return buf
}

// makeNlMsg builds a single netlink message (header + payload) as a byte slice.
func makeNlMsg(msgType uint16, payload []byte) []byte {
	totalLen := syscall.SizeofNlMsghdr + len(payload)
	buf := make([]byte, totalLen)
	hdr := (*syscall.NlMsghdr)(unsafe.Pointer(&buf[0]))
	hdr.Len = uint32(totalLen)
	hdr.Type = msgType
	hdr.Flags = 0
	hdr.Seq = 1
	hdr.Pid = 0
	copy(buf[syscall.SizeofNlMsghdr:], payload)
	return buf
}

// appendNlMsg appends msg to buf, padded to NLMSG_ALIGN(4) boundary.
func appendNlMsg(buf, msg []byte) []byte {
	buf = append(buf, msg...)
	// NLMSG_ALIGN: pad to next 4-byte boundary
	for len(buf)%4 != 0 {
		buf = append(buf, 0)
	}
	return buf
}

func TestIsRelevantChange(t *testing.T) {
	up := uint32(syscall.IFF_UP)
	running := uint32(syscall.IFF_RUNNING)

	tests := []struct {
		name    string
		msgType uint16
		payload []byte
		want    bool
	}{
		// RTM_DELLINK always triggers reconnect (interface deleted).
		{
			name:    "dellink",
			msgType: syscall.RTM_DELLINK,
			payload: makeIfInfoPayload(0, 0),
			want:    true,
		},
		// Address changes always trigger reconnect.
		{
			name:    "newaddr",
			msgType: syscall.RTM_NEWADDR,
			want:    true,
		},
		{
			name:    "deladdr",
			msgType: syscall.RTM_DELADDR,
			want:    true,
		},
		// RTM_NEWLINK: IFF_RUNNING changed → real link event.
		{
			name:    "newlink_running_changed",
			msgType: syscall.RTM_NEWLINK,
			payload: makeIfInfoPayload(up|running, running),
			want:    true,
		},
		// RTM_NEWLINK: IFF_UP changed → interface brought up/down.
		{
			name:    "newlink_up_changed",
			msgType: syscall.RTM_NEWLINK,
			payload: makeIfInfoPayload(up, up),
			want:    true,
		},
		// RTM_NEWLINK: IFF_LOWER_UP changed → physical carrier event.
		{
			name:    "newlink_lower_up_changed",
			msgType: syscall.RTM_NEWLINK,
			payload: makeIfInfoPayload(up|running|iffLowerUp, iffLowerUp),
			want:    true,
		},
		// RTM_NEWLINK: ifi_change=0 → AWS ENA keepalive re-announcement,
		// nothing actually changed.  Must NOT trigger reconnect.
		{
			name:    "newlink_no_change_aws_ena_false_positive",
			msgType: syscall.RTM_NEWLINK,
			payload: makeIfInfoPayload(up|running, 0),
			want:    false,
		},
		// RTM_NEWLINK: only IFF_PROMISC changed → Docker start/stop modifies
		// this on bridge/veth interfaces; must NOT trigger reconnect.
		{
			name:    "newlink_promisc_only_docker_false_positive",
			msgType: syscall.RTM_NEWLINK,
			payload: makeIfInfoPayload(up|running|syscall.IFF_PROMISC, syscall.IFF_PROMISC),
			want:    false,
		},
		// RTM_NEWLINK: only IFF_NOARP changed → irrelevant flag, must NOT reconnect.
		{
			name:    "newlink_noarp_only",
			msgType: syscall.RTM_NEWLINK,
			payload: makeIfInfoPayload(up|running, uint32(syscall.IFF_NOARP)),
			want:    false,
		},
		// RTM_NEWLINK with truncated payload → conservative: treat as relevant.
		{
			name:    "newlink_short_payload",
			msgType: syscall.RTM_NEWLINK,
			payload: []byte{0x00, 0x01},
			want:    true,
		},
		// RTM_NEWLINK with nil payload → conservative.
		{
			name:    "newlink_nil_payload",
			msgType: syscall.RTM_NEWLINK,
			payload: nil,
			want:    true,
		},
		// RTM_NEWLINK with exactly SizeofIfInfomsg bytes → boundary case, parse OK.
		{
			name:    "newlink_exact_size_payload_running_change",
			msgType: syscall.RTM_NEWLINK,
			payload: makeIfInfoPayload(up|running, running),
			want:    true,
		},
		// RTM_NEWLINK with attributes trailing IfInfomsg (real kernel messages
		// append RTA attributes after the fixed header).
		{
			name: "newlink_with_trailing_attrs_no_flag_change",
			msgType: syscall.RTM_NEWLINK,
			payload: append(makeIfInfoPayload(up|running, 0), []byte{0,1,2,3,4,5,6,7}...),
			want:    false,
		},
		// Unrelated message types must not trigger reconnect.
		{
			name:    "other_type_ignored",
			msgType: 42,
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isRelevantChange(tc.msgType, tc.payload)
			if got != tc.want {
				t.Errorf("isRelevantChange(type=%d) = %v, want %v",
					tc.msgType, got, tc.want)
			}
		})
	}
}

// TestWatchBatchMessages exercises the multi-message iteration loop.
// A single datagram containing [no-op RTM_NEWLINK][RTM_NEWADDR] must trigger
// a reconnect for the address event even though the first message is inert.
func TestWatchBatchMessages(t *testing.T) {
	// Build a datagram with two messages back-to-back.
	noop := makeNlMsg(syscall.RTM_NEWLINK, makeIfInfoPayload(
		uint32(syscall.IFF_UP|syscall.IFF_RUNNING), 0, // ifi_change=0: no-op
	))
	addr := makeNlMsg(syscall.RTM_NEWADDR, []byte{})

	var datagram []byte
	datagram = appendNlMsg(datagram, noop)
	datagram = appendNlMsg(datagram, addr)

	// Simulate the main loop body: iterate over messages.
	triggered := false
	data := datagram
	for len(data) >= syscall.SizeofNlMsghdr {
		hdr := (*syscall.NlMsghdr)(unsafe.Pointer(&data[0]))
		msgLen := int(hdr.Len)
		if msgLen < syscall.SizeofNlMsghdr || msgLen > len(data) {
			break
		}
		payload := data[syscall.SizeofNlMsghdr:msgLen]
		if isRelevantChange(hdr.Type, payload) {
			triggered = true
			break
		}
		aligned := (msgLen + 3) &^ 3
		if aligned >= len(data) {
			break
		}
		data = data[aligned:]
	}

	if !triggered {
		t.Error("batch: RTM_NEWADDR in second message should have triggered reconnect")
	}
}

// TestWatchBatchNoOpOnly ensures that a datagram containing only no-op
// RTM_NEWLINK messages does NOT trigger a reconnect.
func TestWatchBatchNoOpOnly(t *testing.T) {
	noop1 := makeNlMsg(syscall.RTM_NEWLINK, makeIfInfoPayload(
		uint32(syscall.IFF_UP|syscall.IFF_RUNNING), 0,
	))
	noop2 := makeNlMsg(syscall.RTM_NEWLINK, makeIfInfoPayload(
		uint32(syscall.IFF_UP|syscall.IFF_RUNNING), 0,
	))

	var datagram []byte
	datagram = appendNlMsg(datagram, noop1)
	datagram = appendNlMsg(datagram, noop2)

	triggered := false
	data := datagram
	for len(data) >= syscall.SizeofNlMsghdr {
		hdr := (*syscall.NlMsghdr)(unsafe.Pointer(&data[0]))
		msgLen := int(hdr.Len)
		if msgLen < syscall.SizeofNlMsghdr || msgLen > len(data) {
			break
		}
		payload := data[syscall.SizeofNlMsghdr:msgLen]
		if isRelevantChange(hdr.Type, payload) {
			triggered = true
			break
		}
		aligned := (msgLen + 3) &^ 3
		if aligned >= len(data) {
			break
		}
		data = data[aligned:]
	}

	if triggered {
		t.Error("batch of two no-op RTM_NEWLINK messages should NOT trigger reconnect")
	}
}

