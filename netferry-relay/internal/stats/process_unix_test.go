//go:build !windows

package stats

import (
	"fmt"
	"net"
	"testing"
)

// Realistic lsof -Fcn output captured from macOS. Includes:
// - LISTEN sockets (no arrow)
// - ESTABLISHED connections (with ->)
// - Multiple fds per process
// - Processes with spaces in names
const sampleLsofOutput = `p1234
cGoogle Chrome
f18
n127.0.0.1:52976->127.0.0.1:12300
f19
n127.0.0.1:52977->127.0.0.1:12300
p5678
ccurl
f3
n127.0.0.1:56789->127.0.0.1:12300
p9999
credis-server
f6
n127.0.0.1:6379
p1111
cCursor Helper (Plugin)
f34
n127.0.0.1:59456
f40
n127.0.0.1:63000->127.0.0.1:12300
p2222
cWeChat
f100
n192.168.1.5:53526->95.100.110.14:443
`

func TestParseLsofOutput(t *testing.T) {
	addrs := map[string]struct{}{
		"127.0.0.1:52976":   {},
		"127.0.0.1:52977":   {},
		"127.0.0.1:56789":   {},
		"127.0.0.1:63000":   {},
		"192.168.1.5:53526": {}, // non-loopback: transparent proxy source
		"127.0.0.1:99999":   {}, // not present in output
	}

	result := parseLsofOutput([]byte(sampleLsofOutput), addrs)

	tests := []struct {
		addr     string
		wantProc string
	}{
		{"127.0.0.1:52976", "Google Chrome"},
		{"127.0.0.1:52977", "Google Chrome"},
		{"127.0.0.1:56789", "curl"},
		{"127.0.0.1:63000", "Cursor Helper (Plugin)"},
		{"192.168.1.5:53526", "WeChat"}, // transparent proxy source matched
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got, ok := result[tt.addr]
			if !ok {
				t.Fatalf("expected process for %s, got nothing", tt.addr)
			}
			if got != tt.wantProc {
				t.Errorf("addr %s: got %q, want %q", tt.addr, got, tt.wantProc)
			}
		})
	}

	// Address not in lsof output should not appear.
	if _, ok := result["127.0.0.1:99999"]; ok {
		t.Error("missing address should not be in result")
	}

	// LISTEN sockets should not appear.
	if _, ok := result["127.0.0.1:6379"]; ok {
		t.Error("LISTEN socket should not be in result")
	}
	if _, ok := result["127.0.0.1:59456"]; ok {
		t.Error("LISTEN socket should not be in result")
	}
}

func TestParseLsofOutput_Empty(t *testing.T) {
	result := parseLsofOutput([]byte(""), map[string]struct{}{"127.0.0.1:1234": {}})
	if len(result) != 0 {
		t.Errorf("expected empty result, got %v", result)
	}
}

func TestParseLsofOutput_NilAddrs(t *testing.T) {
	result := parseLsofOutput([]byte(sampleLsofOutput), nil)
	if len(result) != 0 {
		t.Errorf("expected empty result for nil addrs, got %v", result)
	}
}

// TestLookupProcesses_Live verifies that the real lsof-based lookup works on
// this machine. It creates a TCP listener and a client connection on loopback,
// then checks that lookupProcesses resolves the client's source address to
// this test binary's process name.
func TestLookupProcesses_Live(t *testing.T) {
	// Start a loopback listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Accept in background.
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	// Dial the listener so we have an ESTABLISHED connection.
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	server := <-accepted
	defer server.Close()

	srcAddr := client.LocalAddr().String()
	t.Logf("client srcAddr: %s -> %s", srcAddr, ln.Addr().String())

	addrs := map[string]struct{}{srcAddr: {}}
	result := lookupProcesses(addrs)

	proc, ok := result[srcAddr]
	if !ok {
		// lsof might not be available or might require elevated privileges.
		// List all lsof output for debugging.
		t.Logf("lookupProcesses returned no result for %s (lsof may not be available or needs sudo)", srcAddr)
		t.Logf("full result: %v", result)
		t.Skip("skipping: lsof did not find our connection (may need elevated privileges)")
	}

	t.Logf("resolved process: %q", proc)
	if proc == "" {
		t.Error("process name should not be empty")
	}

	// The process name should contain "test" or the go test binary name —
	// but the exact name is platform/Go-version dependent, so just check non-empty.
	fmt.Printf("Live test: %s -> process=%q\n", srcAddr, proc)
}
