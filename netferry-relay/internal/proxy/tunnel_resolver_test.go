package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/hoveychen/netferry/relay/internal/mux"
)

// fakeDNSClient answers every A query with 203.0.113.7 and counts requests.
type fakeDNSClient struct{ queries int }

func (f *fakeDNSClient) OpenTCP(int, string, int, int) (*mux.ClientConn, error) {
	return nil, errors.New("unused")
}
func (f *fakeDNSClient) OpenUDP(int) (*mux.UDPChannel, error) { return nil, errors.New("unused") }

func (f *fakeDNSClient) DNSRequest(q []byte) ([]byte, error) {
	f.queries++
	// Echo header + question only; Go appends an EDNS0 OPT record we drop.
	end := 12
	for q[end] != 0 {
		end += int(q[end]) + 1
	}
	end += 5 // root label + QTYPE + QCLASS
	qtype := binary.BigEndian.Uint16(q[end-4:])
	resp := append([]byte(nil), q[:end]...)
	resp[2] |= 0x80                          // QR
	resp[3] = 0x80                           // RA, NOERROR
	binary.BigEndian.PutUint16(resp[10:], 0) // ARCOUNT
	if qtype != 1 {                          // only A records
		return resp, nil
	}
	binary.BigEndian.PutUint16(resp[6:], 1) // ANCOUNT
	resp = append(resp, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 203, 0, 113, 7)
	return resp, nil
}

func TestTunnelResolverUsesDNSRequest(t *testing.T) {
	f := &fakeDNSClient{}
	addrs, err := tunnelResolver(f).LookupIPAddr(context.Background(), "example.test")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range addrs {
		if a.IP.String() == "203.0.113.7" {
			found = true
		}
	}
	if !found || f.queries == 0 {
		t.Fatalf("addrs=%v queries=%d", addrs, f.queries)
	}
}
