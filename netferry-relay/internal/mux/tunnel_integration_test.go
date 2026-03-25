//go:build integration
// +build integration

package mux

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hoveychen/netferry/relay/internal/sshconn"
)

// TestIntegration_TLS_Through_Tunnel verifies that a full TLS/HTTPS connection
// works end-to-end through the SSH tunnel. This catches issues where small TLS
// ClientHellos (< 1024 bytes) previously caused the proxy to hang.
//
// Requires: SSH access to own-api-hk (set SSH_TEST_HOST env var to override).
func TestIntegration_TLS_Through_Tunnel(t *testing.T) {
	sshHost := os.Getenv("SSH_TEST_HOST")
	if sshHost == "" {
		sshHost = "own-api-hk"
	}

	// Connect via SSH
	hc, err := sshconn.ParseSSHConfig(sshHost)
	if err != nil {
		t.Fatalf("parse ssh config for %s: %v", sshHost, err)
	}
	ac := sshconn.AuthConfig{}
	client, err := sshconn.Dial(hc, ac)
	if err != nil {
		t.Fatalf("ssh dial %s: %v", sshHost, err)
	}
	defer client.Close()

	// Start the remote server binary
	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	stdin, _ := sess.StdinPipe()
	stdout, _ := sess.StdoutPipe()
	sess.Stderr = os.Stderr

	remoteBin := "/root/.cache/netferry/server-dev-linux-amd64"
	if err := sess.Start(remoteBin); err != nil {
		t.Fatalf("start remote server: %v", err)
	}

	// Read sync header
	if err := ReadSyncHeader(stdout); err != nil {
		t.Fatalf("sync header: %v", err)
	}

	// Create mux client
	muxClient := NewMuxClient(stdout, stdin)
	muxErr := make(chan error, 1)
	go func() { muxErr <- muxClient.Run() }()

	// Drain CMD_ROUTES
	select {
	case <-muxClient.RoutesCh():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for routes")
	case err := <-muxErr:
		t.Fatalf("mux error: %v", err)
	}

	// Test 1: HTTPS to GitHub API (IP-based, avoids DNS)
	t.Run("HTTPS_GitHub_API", func(t *testing.T) {
		conn, err := muxClient.OpenTCP(2, "140.82.121.6", 443)
		if err != nil {
			t.Fatalf("OpenTCP: %v", err)
		}
		defer conn.Close()

		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: "api.github.com",
		})
		if err := tlsConn.Handshake(); err != nil {
			t.Fatalf("TLS handshake: %v", err)
		}
		defer tlsConn.Close()

		// Send HTTP request
		req, _ := http.NewRequest("GET", "https://api.github.com/", nil)
		req.Header.Set("User-Agent", "netferry-test")
		req.Header.Set("Connection", "close")
		if err := req.Write(tlsConn); err != nil {
			t.Fatalf("write HTTP request: %v", err)
		}

		resp, err := http.ReadResponse(bufio.NewReader(tlsConn), req)
		if err != nil {
			t.Fatalf("read HTTP response: %v", err)
		}
		defer resp.Body.Close()

		t.Logf("GitHub API: %s", resp.Status)
		if resp.StatusCode != 200 {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})

	// Test 2: HTTPS to httpbin.org via domain name (tests server-side DNS resolution)
	t.Run("HTTPS_httpbin", func(t *testing.T) {
		conn, err := muxClient.OpenTCP(2, "httpbin.org", 443)
		if err != nil {
			t.Fatalf("OpenTCP: %v", err)
		}
		defer conn.Close()

		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: "httpbin.org",
		})
		if err := tlsConn.Handshake(); err != nil {
			t.Fatalf("TLS handshake: %v", err)
		}
		defer tlsConn.Close()

		fmt.Fprintf(tlsConn, "GET /get HTTP/1.1\r\nHost: httpbin.org\r\nConnection: close\r\n\r\n")

		body, err := io.ReadAll(tlsConn)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		t.Logf("httpbin response: %d bytes", len(body))
		if !strings.Contains(string(body), "200 OK") {
			t.Errorf("expected 200 OK, got: %s", string(body[:min(300, len(body))]))
		}
	})
}
