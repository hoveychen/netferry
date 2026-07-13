package mobile

import (
	"encoding/binary"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hoveychen/netferry/relay/internal/stats"
)

// TestTunForwarderCloseUnblocksReader verifies that Close() returns even when
// readFromTUN is blocked on tunFile.Read(). Without the fix, Close() hangs
// indefinitely because readFromTUN never observes ctx.Done — this is the
// Android "profile stays connected, DNS spam" bug.
func TestTunForwarderCloseUnblocksReader(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	tf, err := newTunForwarder(r, 1500, nil, stats.NewCounters())
	if err != nil {
		t.Fatalf("newTunForwarder: %v", err)
	}

	// Give readFromTUN a moment to enter its blocking Read.
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		tf.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tunForwarder.Close() did not return within 2s — readFromTUN goroutine is leaking")
	}
}

// ── serveDNSFlow tests ────────────────────────────────────────────────────────

// fakePacketConn is an in-memory net.PacketConn: queued inbound datagrams are
// returned by ReadFrom, outbound datagrams are recorded. Closing the incoming
// queue makes ReadFrom return net.ErrClosed, ending the flow.
type fakePacketConn struct {
	incoming chan []byte

	mu       sync.Mutex
	written  [][]byte
	deadline time.Time
}

func newFakePacketConn(queries ...[]byte) *fakePacketConn {
	c := &fakePacketConn{incoming: make(chan []byte, len(queries))}
	for _, q := range queries {
		c.incoming <- q
	}
	close(c.incoming)
	return c
}

var fakeSrcAddr = &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 5555}

func (c *fakePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	var timeout <-chan time.Time
	c.mu.Lock()
	dl := c.deadline
	c.mu.Unlock()
	if !dl.IsZero() {
		timer := time.NewTimer(time.Until(dl))
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case pkt, ok := <-c.incoming:
		if !ok {
			return 0, nil, net.ErrClosed
		}
		return copy(p, pkt), fakeSrcAddr, nil
	case <-timeout:
		return 0, nil, os.ErrDeadlineExceeded
	}
}

func (c *fakePacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	buf := make([]byte, len(p))
	copy(buf, p)
	c.mu.Lock()
	c.written = append(c.written, buf)
	c.mu.Unlock()
	return len(p), nil
}

func (c *fakePacketConn) writtenPackets() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.written...)
}

func (c *fakePacketConn) Close() error { return nil }
func (c *fakePacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(10, 0, 0, 2), Port: 53}
}
func (c *fakePacketConn) SetDeadline(t time.Time) error { c.SetReadDeadline(t); return nil }
func (c *fakePacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}
func (c *fakePacketConn) SetWriteDeadline(time.Time) error { return nil }

// fakeDNSResolver returns a canned response or error for every query.
type fakeDNSResolver struct {
	resp []byte
	err  error
}

func (f *fakeDNSResolver) DNSRequest(data []byte) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	// Echo a minimal valid response for the query (QR bit set, NoError).
	resp := make([]byte, len(data))
	copy(resp, data)
	resp[2] |= 0x80
	return resp, nil
}

// dnsQuery builds a minimal valid DNS query (header + root-name A question).
func dnsQuery(txid uint16) []byte {
	q := make([]byte, 17)
	binary.BigEndian.PutUint16(q[0:2], txid)
	binary.BigEndian.PutUint16(q[2:4], 0x0100) // RD
	binary.BigEndian.PutUint16(q[4:6], 1)      // QDCOUNT
	// QNAME=root(0x00), QTYPE=A, QCLASS=IN
	q[13] = 1
	q[15] = 1
	return q
}

func isServFail(resp []byte) bool {
	return len(resp) >= 12 && resp[2]&0x80 != 0 && resp[3]&0x0f == 2
}

// TestServeDNSFlowErrorRepliesServFail verifies that when the tunnel DNS
// request fails, the app still gets a SERVFAIL response instead of silence
// (silence forces the OS resolver to wait out its full timeout — the Edge
// "dns_probe_started" hang).
func TestServeDNSFlowErrorRepliesServFail(t *testing.T) {
	conn := newFakePacketConn(dnsQuery(0x1234))
	serveDNSFlow(conn, &fakeDNSResolver{err: errors.New("mux closed")}, stats.NewCounters())

	pkts := conn.writtenPackets()
	if len(pkts) != 1 {
		t.Fatalf("expected 1 SERVFAIL response, got %d packets", len(pkts))
	}
	if !isServFail(pkts[0]) {
		t.Fatalf("expected SERVFAIL response, got % x", pkts[0])
	}
}

// TestServeDNSFlowEmptyResponseRepliesServFail verifies that an empty tunnel
// response (e.g. server stream-limit rejection) is turned into SERVFAIL
// rather than a bogus zero-length UDP packet.
func TestServeDNSFlowEmptyResponseRepliesServFail(t *testing.T) {
	conn := newFakePacketConn(dnsQuery(0x2345))
	serveDNSFlow(conn, &fakeDNSResolver{resp: []byte{}}, stats.NewCounters())

	pkts := conn.writtenPackets()
	if len(pkts) != 1 {
		t.Fatalf("expected 1 SERVFAIL response, got %d packets", len(pkts))
	}
	if !isServFail(pkts[0]) {
		t.Fatalf("expected SERVFAIL response, got % x", pkts[0])
	}
}

// TestServeDNSFlowServesAllQueries verifies that every datagram on the flow is
// answered — the OS resolver retries on the same socket (same 4-tuple), so
// serving only the first datagram silently swallows all retries.
func TestServeDNSFlowServesAllQueries(t *testing.T) {
	q1, q2 := dnsQuery(0x0001), dnsQuery(0x0002)
	conn := newFakePacketConn(q1, q2)
	counters := stats.NewCounters()
	serveDNSFlow(conn, &fakeDNSResolver{}, counters)

	pkts := conn.writtenPackets()
	if len(pkts) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(pkts))
	}
	txids := map[uint16]bool{}
	for _, p := range pkts {
		if len(p) < 12 {
			t.Fatalf("short response: % x", p)
		}
		txids[binary.BigEndian.Uint16(p[0:2])] = true
	}
	if !txids[0x0001] || !txids[0x0002] {
		t.Fatalf("responses missing a txid: %v", txids)
	}
	if got := counters.DNSTotal.Load(); got != 2 {
		t.Fatalf("expected DNS counter 2, got %d", got)
	}
}
