// Package mux implements the sshuttle-compatible multiplexing protocol.
// Wire format: [S][S][channel uint16 BE][cmd uint16 BE][datalen uint16 BE][data...]
package mux

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	HDR_LEN    = 8
	MAX_CHAN    = 65535
	SYNC_HDR   = "\x00\x00SSHUTTLE0001"
	SYNC_HDR_N = len(SYNC_HDR)

	// Read/write buffer size — 64 KB, removes the Python 2048-byte limit.
	BUF_SIZE = 65536

	// Outbound frame channel buffer. One frame ≈ 64 KB → ~512 MB max in-flight
	// before backpressure kicks in. In practice connections saturate SSH first.
	MUX_OUT_BUF = 512
)

// Commands (kept identical to sshuttle wire protocol for compatibility).
const (
	CMD_EXIT             = uint16(0x4200)
	CMD_PING             = uint16(0x4201)
	CMD_PONG             = uint16(0x4202)
	CMD_TCP_CONNECT      = uint16(0x4203)
	CMD_TCP_STOP_SENDING = uint16(0x4204)
	CMD_TCP_EOF          = uint16(0x4205)
	CMD_TCP_DATA         = uint16(0x4206)
	CMD_ROUTES           = uint16(0x4207)
	CMD_HOST_REQ         = uint16(0x4208)
	CMD_HOST_LIST        = uint16(0x4209)
	CMD_DNS_REQ          = uint16(0x420a)
	CMD_DNS_RESPONSE     = uint16(0x420b)
	CMD_UDP_OPEN         = uint16(0x420c)
	CMD_UDP_DATA         = uint16(0x420d)
	CMD_UDP_CLOSE        = uint16(0x420e)
)

// Frame is a decoded mux protocol frame.
type Frame struct {
	Channel uint16
	Cmd     uint16
	Data    []byte
}

// WriteFrame encodes and writes one frame to w.
func WriteFrame(w io.Writer, f Frame) error {
	if len(f.Data) > 65535 {
		return fmt.Errorf("mux: data too large: %d bytes", len(f.Data))
	}
	hdr := [HDR_LEN]byte{'S', 'S'}
	binary.BigEndian.PutUint16(hdr[2:], f.Channel)
	binary.BigEndian.PutUint16(hdr[4:], f.Cmd)
	binary.BigEndian.PutUint16(hdr[6:], uint16(len(f.Data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(f.Data) > 0 {
		_, err := w.Write(f.Data)
		return err
	}
	return nil
}

// ReadFrame reads exactly one frame from r.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [HDR_LEN]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	if hdr[0] != 'S' || hdr[1] != 'S' {
		return Frame{}, fmt.Errorf("mux: bad magic bytes 0x%02x 0x%02x", hdr[0], hdr[1])
	}
	channel := binary.BigEndian.Uint16(hdr[2:])
	cmd := binary.BigEndian.Uint16(hdr[4:])
	datalen := binary.BigEndian.Uint16(hdr[6:])

	var data []byte
	if datalen > 0 {
		data = make([]byte, datalen)
		if _, err := io.ReadFull(r, data); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Channel: channel, Cmd: cmd, Data: data}, nil
}

// ReadSyncHeader reads and verifies the "\x00\x00SSHUTTLE0001" handshake.
func ReadSyncHeader(r io.Reader) error {
	buf := make([]byte, SYNC_HDR_N)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("mux: reading sync header: %w", err)
	}
	if string(buf) != SYNC_HDR {
		return fmt.Errorf("mux: unexpected sync header: %q", buf)
	}
	return nil
}

// WriteSyncHeader writes the "\x00\x00SSHUTTLE0001" handshake.
func WriteSyncHeader(w io.Writer) error {
	_, err := io.WriteString(w, SYNC_HDR)
	return err
}
