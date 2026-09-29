// Package mux implements stream-multiplexed tunnelling over SSH sessions.
// Transport: smux (github.com/xtaci/smux) sessions over SSH stdin/stdout.
//
// Per-stream wire protocol
// ========================
// Client-opened streams begin with a newline-terminated header:
//
//	"TCP <family> <ip> <port>\n"   — proxy a TCP connection
//	"DNS\n"                         — forward one DNS query
//	"UDP <family>\n"                — open a UDP datagram channel
//
// Server-opened streams begin with:
//
//	"ROUTES\n"                      — followed by "family,ip,width\n" lines
//
// After the header, TCP and UDP streams exchange length-prefixed messages:
//
//	[uint16 BE length][payload bytes]
//
// A zero-length message signals a half-close (equivalent to TCP FIN from
// that direction). DNS streams use the same framing for one query/response.
//
// A length of 0xFFFF (never a data frame, see BUF_SIZE) marks an error frame
// sent by the server on a TCP stream, e.g. when dialing the destination fails:
//
//	[0xFFFF][uint16 BE length][error message]
//
// The client surfaces it as a *RemoteError from Read.
package mux

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

const (
	// SYNC_HDR is written by the server on startup; the client reads it to
	// confirm the remote binary started correctly.
	SYNC_HDR   = "\x00\x00SSHUTTLE0001"
	SYNC_HDR_N = len(SYNC_HDR)

	// BUF_SIZE is the maximum payload written in a single writeMsg call.
	// Fits in a uint16, leaving one value (0) reserved for half-close.
	BUF_SIZE = 65534

	// Keepalive timings (used by smux config and health logging).
	KEEPALIVE_INTERVAL = 15 * time.Second
	KEEPALIVE_TIMEOUT  = 15 * time.Second

	// DEFAULT_INITIAL_WINDOW is exported so cmd/tunnel/main.go compiles
	// without changes; smux ignores it (manages its own window).
	DEFAULT_INITIAL_WINDOW = 4 * 1024 * 1024
)

// WriteSyncHeader writes the handshake marker to w.
func WriteSyncHeader(w io.Writer) error {
	_, err := io.WriteString(w, SYNC_HDR)
	return err
}

// ReadSyncHeader reads and verifies the handshake marker from r.
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

// writeMsg writes a length-prefixed message to w.
// payload==nil or len==0 encodes as a zero-length frame (half-close signal).
func writeMsg(w io.Writer, payload []byte) error {
	if len(payload) > BUF_SIZE {
		return fmt.Errorf("mux: payload too large: %d bytes", len(payload))
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(payload)))
	buf := make([]byte, 2+len(payload))
	copy(buf[:2], hdr[:])
	copy(buf[2:], payload)
	_, err := w.Write(buf)
	return err
}

// errFrameMarker is the length value that introduces an error frame.
const errFrameMarker = 0xFFFF

// maxErrMsg caps the message carried by an error frame.
const maxErrMsg = 1024

// RemoteError is an error reported by the server over a stream, e.g. the
// destination could not be dialed.
type RemoteError struct{ Msg string }

func (e *RemoteError) Error() string { return "remote: " + e.Msg }

// WriteErrorMsg writes an error frame carrying msg to w.
func WriteErrorMsg(w io.Writer, msg string) error {
	if len(msg) > maxErrMsg {
		msg = msg[:maxErrMsg]
	}
	buf := make([]byte, 4+len(msg))
	binary.BigEndian.PutUint16(buf[:2], errFrameMarker)
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(msg)))
	copy(buf[4:], msg)
	_, err := w.Write(buf)
	return err
}

// readMsg reads one length-prefixed message from r.
// Returns an empty slice for a half-close frame (length == 0), and a
// *RemoteError for an error frame.
func readMsg(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(hdr[:])
	if n == 0 {
		return nil, nil // half-close signal
	}
	if n == errFrameMarker {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, err
		}
		msg := make([]byte, binary.BigEndian.Uint16(hdr[:]))
		if _, err := io.ReadFull(r, msg); err != nil {
			return nil, err
		}
		return nil, &RemoteError{Msg: string(msg)}
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
