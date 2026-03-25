// Package mux implements the sshuttle-compatible multiplexing protocol.
// Wire format: [S][S][channel uint16 BE][cmd uint16 BE][datalen uint16 BE][data...]
package mux

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

const (
	HDR_LEN    = 8
	MAX_CHAN    = 65535
	SYNC_HDR   = "\x00\x00SSHUTTLE0001"
	SYNC_HDR_N = len(SYNC_HDR)

	// Read/write buffer size — must fit in uint16 datalen field (max 65535).
	BUF_SIZE = 65535

	// Outbound frame channel buffer. One frame ≈ 64 KB → ~512 MB max in-flight
	// before backpressure kicks in. In practice connections saturate SSH first.
	MUX_OUT_BUF = 512

	// INBOX_SEND_TIMEOUT is how long we wait to deliver a frame to a
	// per-channel inbox before considering the consumer dead and closing
	// the channel. This prevents silent data loss for TCP streams.
	// 60 seconds allows for TCP backpressure during large uploads
	// (e.g. git push) and slow remote servers (e.g. LLM API processing).
	INBOX_SEND_TIMEOUT = 60 * time.Second

	// KEEPALIVE_INTERVAL is how often the client sends CMD_PING to the server
	// to detect dead connections. 15s strikes a balance between quick
	// detection (e.g. after a WiFi switch) and low overhead.
	KEEPALIVE_INTERVAL = 15 * time.Second

	// KEEPALIVE_TIMEOUT is how long we wait for a CMD_PONG before considering
	// the connection dead.
	KEEPALIVE_TIMEOUT = 15 * time.Second

	// SERVER_IDLE_TIMEOUT is how long the server waits without receiving any
	// frame before considering the client dead and exiting. This prevents
	// orphaned server processes when the SSH connection dies without a clean
	// shutdown (e.g. network loss, client crash). The client sends CMD_PING
	// every KEEPALIVE_INTERVAL (15s), so 60s gives 4 missed pings of margin.
	SERVER_IDLE_TIMEOUT = 60 * time.Second
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
	CMD_WINDOW_UPDATE    = uint16(0x420f)
)

const (
	// PRIORITY_OUT_BUF is the buffer size for the priority output channel.
	// Control frames (PING/PONG/DNS/WINDOW_UPDATE) are small and infrequent.
	PRIORITY_OUT_BUF = 64

	// DEFAULT_INITIAL_WINDOW is the per-channel send window in bytes.
	// The sender can transmit this many bytes before needing a WINDOW_UPDATE
	// from the receiver. 256 KB balances latency and throughput.
	DEFAULT_INITIAL_WINDOW = 256 * 1024

	// WINDOW_UPDATE_THRESHOLD controls when the receiver sends a
	// WINDOW_UPDATE back to the sender. Once consumed bytes exceed this
	// fraction of the initial window, a WINDOW_UPDATE is sent. This avoids
	// sending a WINDOW_UPDATE for every tiny Read().
	WINDOW_UPDATE_THRESHOLD = DEFAULT_INITIAL_WINDOW / 4
)

// Frame is a decoded mux protocol frame.
type Frame struct {
	Channel uint16
	Cmd     uint16
	Data    []byte
}

// WriteFrame encodes and writes one frame to w.
// If w is a *bufio.Writer the header and data are coalesced in the buffer;
// the caller is responsible for flushing.
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

// NewBufferedWriter wraps w with a bufio.Writer sized for typical mux frames.
// The writer goroutine should call Flush() after each batch of frames.
func NewBufferedWriter(w io.Writer) *bufio.Writer {
	// 128 KB buffer: fits ~2 full-size frames or many small frames.
	return bufio.NewWriterSize(w, 128*1024)
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
