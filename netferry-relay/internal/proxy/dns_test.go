package proxy

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildDNSQuery constructs a minimal DNS query for "example.com" with the
// given QTYPE.
func buildDNSQuery(qtype uint16) []byte {
	var buf bytes.Buffer
	// Header: ID, flags(RD=1), QDCOUNT=1, others=0.
	binary.Write(&buf, binary.BigEndian, uint16(0x1234)) // ID
	binary.Write(&buf, binary.BigEndian, uint16(0x0100)) // RD=1
	binary.Write(&buf, binary.BigEndian, uint16(1))      // QDCOUNT
	binary.Write(&buf, binary.BigEndian, uint16(0))      // ANCOUNT
	binary.Write(&buf, binary.BigEndian, uint16(0))      // NSCOUNT
	binary.Write(&buf, binary.BigEndian, uint16(0))      // ARCOUNT
	// QNAME: "example.com"
	for _, label := range []string{"example", "com"} {
		buf.WriteByte(byte(len(label)))
		buf.WriteString(label)
	}
	buf.WriteByte(0)                                 // root label
	binary.Write(&buf, binary.BigEndian, qtype)      // QTYPE
	binary.Write(&buf, binary.BigEndian, uint16(1))  // QCLASS=IN
	return buf.Bytes()
}

func TestIsAAAAQuery(t *testing.T) {
	cases := []struct {
		name  string
		qtype uint16
		want  bool
	}{
		{"AAAA", dnsTypeAAAA, true},
		{"A", 1, false},
		{"MX", 15, false},
		{"HTTPS", 65, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isAAAAQuery(buildDNSQuery(c.qtype))
			if got != c.want {
				t.Errorf("isAAAAQuery(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestIsAAAAQuery_RejectsResponse(t *testing.T) {
	q := buildDNSQuery(dnsTypeAAAA)
	// Set QR=1 to mark as response.
	binary.BigEndian.PutUint16(q[2:4], 0x8100)
	if isAAAAQuery(q) {
		t.Error("isAAAAQuery returned true for a response, want false")
	}
}

func TestIsAAAAQuery_TooShort(t *testing.T) {
	if isAAAAQuery([]byte{0, 0, 0}) {
		t.Error("isAAAAQuery returned true for an undersized message")
	}
}

func TestBuildEmptyNoError(t *testing.T) {
	q := buildDNSQuery(dnsTypeAAAA)
	resp := buildEmptyNoError(q)
	if resp == nil {
		t.Fatal("buildEmptyNoError returned nil for valid query")
	}
	if !bytes.Equal(resp[:2], q[:2]) {
		t.Errorf("transaction ID not preserved: got %x, want %x", resp[:2], q[:2])
	}
	flags := binary.BigEndian.Uint16(resp[2:4])
	if flags&0x8000 == 0 {
		t.Error("QR bit not set in response flags")
	}
	if flags&0x000F != 0 {
		t.Errorf("RCODE != 0 (NoError): flags=%04x", flags)
	}
	// RD bit should be preserved from query.
	if flags&0x0100 == 0 {
		t.Error("RD bit not preserved from query")
	}
	if binary.BigEndian.Uint16(resp[4:6]) != 1 {
		t.Error("QDCOUNT should be 1 (question echoed)")
	}
	for offset, name := range map[int]string{6: "ANCOUNT", 8: "NSCOUNT", 10: "ARCOUNT"} {
		if binary.BigEndian.Uint16(resp[offset:offset+2]) != 0 {
			t.Errorf("%s should be 0 in empty NoError response", name)
		}
	}
}
