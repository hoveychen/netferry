package mux

import (
	"bytes"
	"io"
	"reflect"
	"testing"
	"time"
)

// ---- splitComma tests -------------------------------------------------------

func TestSplitComma_Basic(t *testing.T) {
	got := splitComma("a,b,c", 3)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitComma_FewerThanN(t *testing.T) {
	// Only 2 commas → 2 parts, n=5 still returns available parts.
	got := splitComma("a,b", 5)
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitComma_NoComma(t *testing.T) {
	got := splitComma("abc", 3)
	want := []string{"abc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitComma_EmptyString(t *testing.T) {
	got := splitComma("", 3)
	want := []string{""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitComma_MoreCommasThanN(t *testing.T) {
	// n=3 means at most 3 parts; last part absorbs remainder including commas.
	got := splitComma("a,b,c,d,e", 3)
	want := []string{"a", "b", "c,d,e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// ---- parseTCPConnect tests --------------------------------------------------

func TestParseTCPConnect_Valid(t *testing.T) {
	family, ip, port, err := parseTCPConnect([]byte("2,10.0.0.1,80"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if family != 2 {
		t.Errorf("family: got %d, want 2", family)
	}
	if ip != "10.0.0.1" {
		t.Errorf("ip: got %q, want \"10.0.0.1\"", ip)
	}
	if port != 80 {
		t.Errorf("port: got %d, want 80", port)
	}
}

func TestParseTCPConnect_IPv6(t *testing.T) {
	family, ip, port, err := parseTCPConnect([]byte("10,::1,443"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if family != 10 {
		t.Errorf("family: got %d, want 10", family)
	}
	if ip != "::1" {
		t.Errorf("ip: got %q, want \"::1\"", ip)
	}
	if port != 443 {
		t.Errorf("port: got %d, want 443", port)
	}
}

func TestParseTCPConnect_Invalid_TooFewParts(t *testing.T) {
	_, _, _, err := parseTCPConnect([]byte("2,10.0.0.1"))
	if err == nil {
		t.Fatal("expected error for too few parts, got nil")
	}
}

func TestParseTCPConnect_Invalid_Empty(t *testing.T) {
	_, _, _, err := parseTCPConnect([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty data, got nil")
	}
}

func TestParseTCPConnect_Invalid_BadFamily(t *testing.T) {
	_, _, _, err := parseTCPConnect([]byte("notanint,10.0.0.1,80"))
	if err == nil {
		t.Fatal("expected error for bad family, got nil")
	}
}

func TestParseTCPConnect_Invalid_BadPort(t *testing.T) {
	_, _, _, err := parseTCPConnect([]byte("2,10.0.0.1,notaport"))
	if err == nil {
		t.Fatal("expected error for bad port, got nil")
	}
}

// ---- MuxServer dispatch tests -----------------------------------------------

// pipeConn wires two in-memory pipes so we get synchronous read/write pairs.
type pipeConn struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func newPipePair() (server pipeConn, client pipeConn) {
	// server reads from clientToServer, writes to serverToClient.
	csr, csw := io.Pipe() // client→server
	scr, scw := io.Pipe() // server→client
	server = pipeConn{r: csr, w: scw}
	client = pipeConn{r: scr, w: csw}
	return
}

// TestMuxServer_Ping verifies that a CMD_PING frame is answered with CMD_PONG.
func TestMuxServer_Ping(t *testing.T) {
	serverPipe, clientPipe := newPipePair()

	srv := NewMuxServer(serverPipe.r, serverPipe.w, ServerHandlers{})
	go srv.Run() //nolint:errcheck

	// Write a PING frame from the "client" side.
	pingData := []byte("keepalive")
	if err := WriteFrame(clientPipe.w, Frame{Channel: 0, Cmd: CMD_PING, Data: pingData}); err != nil {
		t.Fatalf("WriteFrame ping: %v", err)
	}

	// Read the PONG back.
	done := make(chan Frame, 1)
	go func() {
		f, _ := ReadFrame(clientPipe.r)
		done <- f
	}()

	select {
	case f := <-done:
		if f.Cmd != CMD_PONG {
			t.Errorf("expected CMD_PONG, got %04x", f.Cmd)
		}
		if !bytes.Equal(f.Data, pingData) {
			t.Errorf("pong data mismatch: got %v, want %v", f.Data, pingData)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for PONG")
	}

	// Tear down.
	clientPipe.w.Close()
	clientPipe.r.Close()
	serverPipe.r.Close()
	serverPipe.w.Close()
}

// TestMuxServer_Exit verifies that CMD_EXIT causes Run() to return an error.
func TestMuxServer_Exit(t *testing.T) {
	serverPipe, clientPipe := newPipePair()

	srv := NewMuxServer(serverPipe.r, serverPipe.w, ServerHandlers{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run()
	}()

	if err := WriteFrame(clientPipe.w, Frame{Channel: 0, Cmd: CMD_EXIT}); err != nil {
		t.Fatalf("WriteFrame exit: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected non-nil error after CMD_EXIT, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run() to return after CMD_EXIT")
	}

	clientPipe.w.Close()
	clientPipe.r.Close()
	serverPipe.r.Close()
	serverPipe.w.Close()
}

// TestMuxServer_TCPConnectDispatch verifies that CMD_TCP_CONNECT triggers the
// NewTCP handler with the correct parsed values.
func TestMuxServer_TCPConnectDispatch(t *testing.T) {
	serverPipe, clientPipe := newPipePair()

	type tcpArgs struct {
		channel uint16
		family  int
		ip      string
		port    int
	}
	received := make(chan tcpArgs, 1)

	handlers := ServerHandlers{
		NewTCP: func(channel uint16, family int, dstIP string, dstPort int) {
			received <- tcpArgs{channel, family, dstIP, dstPort}
		},
	}

	srv := NewMuxServer(serverPipe.r, serverPipe.w, handlers)
	go srv.Run() //nolint:errcheck

	// Send a CMD_TCP_CONNECT from the client.
	if err := WriteFrame(clientPipe.w, Frame{
		Channel: 7,
		Cmd:     CMD_TCP_CONNECT,
		Data:    []byte("2,192.168.1.1,8080"),
	}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	select {
	case args := <-received:
		if args.channel != 7 {
			t.Errorf("channel: got %d, want 7", args.channel)
		}
		if args.family != 2 {
			t.Errorf("family: got %d, want 2", args.family)
		}
		if args.ip != "192.168.1.1" {
			t.Errorf("ip: got %q, want \"192.168.1.1\"", args.ip)
		}
		if args.port != 8080 {
			t.Errorf("port: got %d, want 8080", args.port)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for NewTCP callback")
	}

	clientPipe.w.Close()
	clientPipe.r.Close()
	serverPipe.r.Close()
	serverPipe.w.Close()
}

// TestMuxServer_DNSDispatch verifies that CMD_DNS_REQ triggers the DNSReq handler.
func TestMuxServer_DNSDispatch(t *testing.T) {
	serverPipe, clientPipe := newPipePair()

	dnsData := []byte{0xAB, 0xCD, 0x01, 0x00} // fake DNS query bytes

	received := make(chan []byte, 1)
	handlers := ServerHandlers{
		DNSReq: func(channel uint16, data []byte) {
			received <- data
		},
	}

	srv := NewMuxServer(serverPipe.r, serverPipe.w, handlers)
	go srv.Run() //nolint:errcheck

	if err := WriteFrame(clientPipe.w, Frame{
		Channel: 1,
		Cmd:     CMD_DNS_REQ,
		Data:    dnsData,
	}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(data, dnsData) {
			t.Errorf("DNS data mismatch: got %v, want %v", data, dnsData)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for DNSReq callback")
	}

	clientPipe.w.Close()
	clientPipe.r.Close()
	serverPipe.r.Close()
	serverPipe.w.Close()
}

// TestMuxServer_CloseChannel verifies CloseChannel removes the channel and
// closes the inbox.
func TestMuxServer_CloseChannel(t *testing.T) {
	serverPipe, clientPipe := newPipePair()

	opened := make(chan struct{}, 1)
	handlers := ServerHandlers{
		NewTCP: func(channel uint16, family int, dstIP string, dstPort int) {
			opened <- struct{}{}
		},
	}

	srv := NewMuxServer(serverPipe.r, serverPipe.w, handlers)
	go srv.Run() //nolint:errcheck

	if err := WriteFrame(clientPipe.w, Frame{
		Channel: 5,
		Cmd:     CMD_TCP_CONNECT,
		Data:    []byte("2,10.0.0.1,80"),
	}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	select {
	case <-opened:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for NewTCP")
	}

	// Channel 5 should now be in the map.
	if inbox := srv.InboxFor(5); inbox == nil {
		t.Error("expected inbox for channel 5, got nil")
	}

	srv.CloseChannel(5)

	if inbox := srv.InboxFor(5); inbox != nil {
		t.Error("expected nil inbox after CloseChannel, got non-nil")
	}

	clientPipe.w.Close()
	clientPipe.r.Close()
	serverPipe.r.Close()
	serverPipe.w.Close()
}

// TestMuxServer_SendTo verifies that SendTo enqueues a frame that the client can read.
func TestMuxServer_SendTo(t *testing.T) {
	serverPipe, clientPipe := newPipePair()

	srv := NewMuxServer(serverPipe.r, serverPipe.w, ServerHandlers{})
	go srv.Run() //nolint:errcheck

	// Server proactively sends a frame to the client.
	srv.SendTo(99, CMD_TCP_DATA, []byte("payload"))

	done := make(chan Frame, 1)
	go func() {
		f, _ := ReadFrame(clientPipe.r)
		done <- f
	}()

	select {
	case f := <-done:
		if f.Channel != 99 || f.Cmd != CMD_TCP_DATA {
			t.Errorf("unexpected frame: channel=%d cmd=%04x", f.Channel, f.Cmd)
		}
		if string(f.Data) != "payload" {
			t.Errorf("data: got %q, want \"payload\"", f.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for frame from server")
	}

	clientPipe.w.Close()
	clientPipe.r.Close()
	serverPipe.r.Close()
	serverPipe.w.Close()
}
