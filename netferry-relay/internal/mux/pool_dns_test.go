package mux

import (
	"bufio"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtaci/smux"
)

func newDNSMuxTestClient(t *testing.T, handle func(*smux.Stream)) *MuxClient {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	serverSession, err := smux.Server(serverConn, smuxClientConfig())
	if err != nil {
		t.Fatalf("smux.Server: %v", err)
	}
	client := NewMuxClient(clientConn, clientConn)

	go func() {
		for {
			stream, err := serverSession.AcceptStream()
			if err != nil {
				return
			}
			go handle(stream)
		}
	}()

	t.Cleanup(func() {
		_ = client.session.Close()
		_ = serverSession.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return client
}

func readDNSMuxTestQuery(t *testing.T, stream *smux.Stream) []byte {
	t.Helper()

	reader := bufio.NewReader(stream)
	header, err := reader.ReadString('\n')
	if err != nil {
		t.Errorf("read DNS header: %v", err)
		return nil
	}
	if header != "DNS\n" {
		t.Errorf("DNS header = %q, want %q", header, "DNS\n")
		return nil
	}
	query, err := readMsg(reader)
	if err != nil {
		t.Errorf("read DNS query: %v", err)
		return nil
	}
	return query
}

func TestMuxPoolDNSFailover(t *testing.T) {
	query := []byte{0x12, 0x34, 0x01, 0x00}
	want := []byte{0x12, 0x34, 0x81, 0x80}
	var firstAttempts atomic.Int32
	var secondAttempts atomic.Int32

	first := newDNSMuxTestClient(t, func(stream *smux.Stream) {
		defer stream.Close()
		firstAttempts.Add(1)
		_ = readDNSMuxTestQuery(t, stream)
		// Keep the stream open without a response to reproduce dns read timeout.
		time.Sleep(dnsFirstAttemptTimeout + 250*time.Millisecond)
	})
	second := newDNSMuxTestClient(t, func(stream *smux.Stream) {
		defer stream.Close()
		secondAttempts.Add(1)
		if got := readDNSMuxTestQuery(t, stream); string(got) != string(query) {
			t.Errorf("second tunnel query = %v, want %v", got, query)
			return
		}
		if err := writeMsg(stream, want); err != nil {
			t.Errorf("write DNS response: %v", err)
		}
	})

	pool := NewMuxPool([]*MuxClient{first, second})
	got, err := pool.DNSRequest(query)
	if err != nil {
		t.Fatalf("DNSRequest returned first tunnel error instead of failing over: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("DNS response = %v, want %v", got, want)
	}
	if firstAttempts.Load() != 1 || secondAttempts.Load() != 1 {
		t.Fatalf("attempts = first:%d second:%d, want 1 each", firstAttempts.Load(), secondAttempts.Load())
	}
}
