package proxy

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"time"
)

// peekHost tries to determine the hostname of a connection by reading the
// first bytes of the client's initial data.
//
// For TLS (port 443 etc.): parses the ClientHello to extract the SNI extension.
// For HTTP (port 80 etc.): parses the first request line / Host header.
//
// Returns the hostname (no port) and an io.Reader that replays the captured
// bytes followed by the rest of the connection. If detection fails the
// returned host is "" and the reader still works correctly.
//
// IMPORTANT: We use a single conn.Read instead of bufio.Peek(1024) because
// Peek blocks until 1024 bytes are buffered. Non-browser TLS clients (git,
// curl, Go net/http, Python requests) send ClientHellos of only 200-500 bytes,
// causing Peek to block indefinitely waiting for data that never arrives
// (the client is waiting for the server's response first).
func peekHost(conn net.Conn, dstPort int) (host string, r io.Reader) {
	const peekSize = 1024

	// Set a generous deadline so we don't block forever if the client
	// connects but never sends data (e.g., server-speaks-first protocols).
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, peekSize)
	n, _ := conn.Read(buf)
	conn.SetReadDeadline(time.Time{}) // clear deadline for subsequent I/O

	if n == 0 {
		return "", conn
	}
	buf = buf[:n]

	// Try TLS first (most common for modern traffic).
	if len(buf) > 5 && buf[0] == 0x16 { // TLS handshake content type
		host = parseTLSSNI(buf)
	} else {
		host = parseHTTPHost(buf)
	}

	// Return a reader that replays the captured bytes, then continues from conn.
	return host, io.MultiReader(bytes.NewReader(buf), conn)
}

// parseTLSSNI extracts the SNI hostname from a TLS ClientHello message.
// Returns "" if parsing fails or no SNI extension is present.
func parseTLSSNI(buf []byte) string {
	// TLS record: type(1) version(2) length(2) → handshake starts at [5]
	if len(buf) < 5 {
		return ""
	}
	// Record layer
	recordLen := int(buf[3])<<8 | int(buf[4])
	data := buf[5:]
	if len(data) < recordLen {
		data = data[:len(data)] // use what we have
	} else {
		data = data[:recordLen]
	}

	// Handshake header: type(1) length(3)
	if len(data) < 4 || data[0] != 1 { // 1 = ClientHello
		return ""
	}
	// Skip handshake header
	data = data[4:]

	// ClientHello: version(2) random(32) → 34 bytes
	if len(data) < 34 {
		return ""
	}
	data = data[34:]

	// Session ID: length(1) + data
	if len(data) < 1 {
		return ""
	}
	sidLen := int(data[0])
	data = data[1:]
	if len(data) < sidLen {
		return ""
	}
	data = data[sidLen:]

	// Cipher suites: length(2) + data
	if len(data) < 2 {
		return ""
	}
	csLen := int(data[0])<<8 | int(data[1])
	data = data[2:]
	if len(data) < csLen {
		return ""
	}
	data = data[csLen:]

	// Compression methods: length(1) + data
	if len(data) < 1 {
		return ""
	}
	cmLen := int(data[0])
	data = data[1:]
	if len(data) < cmLen {
		return ""
	}
	data = data[cmLen:]

	// Extensions: length(2) + data
	if len(data) < 2 {
		return ""
	}
	extLen := int(data[0])<<8 | int(data[1])
	data = data[2:]
	if len(data) < extLen {
		data = data[:len(data)]
	} else {
		data = data[:extLen]
	}

	// Walk extensions looking for SNI (type 0x0000).
	for len(data) >= 4 {
		extType := int(data[0])<<8 | int(data[1])
		eLen := int(data[2])<<8 | int(data[3])
		data = data[4:]
		if len(data) < eLen {
			break
		}
		if extType == 0 { // server_name
			return parseSNIExtension(data[:eLen])
		}
		data = data[eLen:]
	}
	return ""
}

// parseSNIExtension parses the SNI extension payload and returns the first
// host_name entry.
func parseSNIExtension(data []byte) string {
	// SNI extension: list_length(2) then entries: type(1) length(2) name
	if len(data) < 2 {
		return ""
	}
	data = data[2:] // skip list length
	for len(data) >= 3 {
		nameType := data[0]
		nameLen := int(data[1])<<8 | int(data[2])
		data = data[3:]
		if len(data) < nameLen {
			break
		}
		if nameType == 0 { // host_name
			return string(data[:nameLen])
		}
		data = data[nameLen:]
	}
	return ""
}

// parseHTTPHost reads the first few bytes looking for an HTTP request and
// extracts the Host header.
func parseHTTPHost(buf []byte) string {
	// Quick sanity check: HTTP methods start with uppercase ASCII.
	if len(buf) < 4 {
		return ""
	}
	switch {
	case string(buf[:3]) == "GET",
		string(buf[:4]) == "POST",
		string(buf[:4]) == "HEAD",
		string(buf[:3]) == "PUT",
		string(buf[:5]) == "PATCH",
		string(buf[:6]) == "DELETE",
		string(buf[:7]) == "OPTIONS",
		string(buf[:7]) == "CONNECT":
		// looks like HTTP
	default:
		return ""
	}

	// Parse just enough of the request to get Host header.
	// We use http.ReadRequest which expects a full request but we only have
	// a partial buffer. Wrap in a limited reader.
	req, err := http.ReadRequest(bufio.NewReader(limitedBytesReader(buf)))
	if err != nil {
		return ""
	}
	return req.Host
}

// limitedBytesReader wraps a byte slice as an io.Reader.
type byteSliceReader struct {
	data []byte
	pos  int
}

func limitedBytesReader(b []byte) *byteSliceReader {
	return &byteSliceReader{data: b}
}

func (r *byteSliceReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, &net.OpError{Op: "read", Err: net.ErrClosed}
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
