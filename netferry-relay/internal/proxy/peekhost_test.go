package proxy

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"
)

// buildSmallTLSClientHello generates a minimal TLS ClientHello with SNI.
// This produces a ClientHello smaller than 1024 bytes, which is typical
// for non-browser TLS clients (git, curl, Go net/http, Python requests).
func buildSmallTLSClientHello(serverName string) []byte {
	// Use Go's TLS library to generate a real ClientHello.
	// We capture it by doing a TLS handshake with a pipe.
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	helloCh := make(chan []byte, 1)
	go func() {
		defer clientConn.Close()
		tlsConn := tls.Client(clientConn, &tls.Config{
			ServerName:         serverName,
			InsecureSkipVerify: true,
		})
		// Start handshake — this writes the ClientHello.
		tlsConn.Handshake() //nolint:errcheck
	}()

	go func() {
		// Read whatever the client sends (the ClientHello record).
		buf := make([]byte, 4096)
		n, _ := serverConn.Read(buf)
		if n > 0 {
			helloCh <- buf[:n]
		} else {
			helloCh <- nil
		}
	}()

	select {
	case hello := <-helloCh:
		return hello
	case <-time.After(2 * time.Second):
		return nil
	}
}

// TestPeekHost_SmallTLSClientHello verifies that peekHost does NOT block
// when the TLS ClientHello is smaller than 1024 bytes.
// This was a real bug: bufio.Peek(1024) blocks until 1024 bytes are
// available, but non-browser TLS clients (git, curl, Go) send ClientHellos
// of only 300-500 bytes, causing the proxy to hang indefinitely.
func TestPeekHost_SmallTLSClientHello(t *testing.T) {
	hello := buildSmallTLSClientHello("api.example.com")
	if hello == nil {
		t.Fatal("failed to build TLS ClientHello")
	}
	if len(hello) >= 1024 {
		t.Skipf("ClientHello is %d bytes (>= 1024), cannot test small ClientHello path", len(hello))
	}
	t.Logf("ClientHello size: %d bytes (< 1024, good for testing)", len(hello))

	// Create a pipe: write the ClientHello to one end, peekHost reads from the other.
	clientConn, proxyConn := net.Pipe()

	// Write the ClientHello and keep the connection open (simulating a real TLS client
	// that's waiting for the server's response).
	go func() {
		clientConn.Write(hello)
		// Do NOT close — the client is waiting for ServerHello.
	}()
	defer clientConn.Close()

	// peekHost must return within a reasonable time, not block forever.
	type result struct {
		host string
		r    io.Reader
	}
	resultCh := make(chan result, 1)
	go func() {
		host, r := peekHost(proxyConn, 443)
		resultCh <- result{host, r}
	}()

	select {
	case res := <-resultCh:
		if res.host != "api.example.com" {
			t.Errorf("peekHost returned host=%q, want %q", res.host, "api.example.com")
		}
		// Verify that the reader replays the full ClientHello.
		buf := make([]byte, len(hello))
		n, err := io.ReadFull(res.r, buf)
		if err != nil {
			t.Fatalf("reading replayed data: %v (got %d bytes)", err, n)
		}
		if !bytes.Equal(buf, hello) {
			t.Error("replayed data does not match original ClientHello")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("peekHost blocked for >3s — this is the bug! Peek(1024) hangs when ClientHello < 1024 bytes")
	}
}

// TestPeekHost_HTTPRequest verifies peekHost extracts Host from HTTP requests.
func TestPeekHost_HTTPRequest(t *testing.T) {
	httpReq := []byte("GET / HTTP/1.1\r\nHost: example.com\r\nUser-Agent: test\r\n\r\n")

	clientConn, proxyConn := net.Pipe()
	go func() {
		clientConn.Write(httpReq)
	}()
	defer clientConn.Close()

	type result struct {
		host string
		r    io.Reader
	}
	resultCh := make(chan result, 1)
	go func() {
		host, r := peekHost(proxyConn, 80)
		resultCh <- result{host, r}
	}()

	select {
	case res := <-resultCh:
		if res.host != "example.com" {
			t.Errorf("peekHost returned host=%q, want %q", res.host, "example.com")
		}
		// Verify data replay.
		buf := make([]byte, len(httpReq))
		n, err := io.ReadFull(res.r, buf)
		if err != nil {
			t.Fatalf("reading replayed data: %v (got %d bytes)", err, n)
		}
		if !bytes.Equal(buf, httpReq) {
			t.Error("replayed data does not match original HTTP request")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("peekHost blocked for >3s on HTTP request")
	}
}

// TestPeekHost_DataIntegrity verifies that ALL data flows through correctly
// after peekHost, including data sent after the initial peek.
func TestPeekHost_DataIntegrity(t *testing.T) {
	hello := buildSmallTLSClientHello("stream.example.com")
	if hello == nil {
		t.Fatal("failed to build TLS ClientHello")
	}

	clientConn, proxyConn := net.Pipe()
	extraData := []byte("this is additional data after the ClientHello")

	go func() {
		clientConn.Write(hello)
		// Simulate the client sending more data after a brief pause.
		time.Sleep(100 * time.Millisecond)
		clientConn.Write(extraData)
		clientConn.Close()
	}()

	type result struct {
		host string
		r    io.Reader
	}
	resultCh := make(chan result, 1)
	go func() {
		host, r := peekHost(proxyConn, 443)
		resultCh <- result{host, r}
	}()

	select {
	case res := <-resultCh:
		if res.host != "stream.example.com" {
			t.Errorf("host=%q, want %q", res.host, "stream.example.com")
		}
		// Read ALL data: should be ClientHello + extraData.
		allData, err := io.ReadAll(res.r)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		expected := append(hello, extraData...)
		if !bytes.Equal(allData, expected) {
			t.Errorf("data mismatch: got %d bytes, want %d bytes", len(allData), len(expected))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("peekHost blocked")
	}
}
