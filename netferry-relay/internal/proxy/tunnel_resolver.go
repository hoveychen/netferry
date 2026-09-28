package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"time"

	"github.com/hoveychen/netferry/relay/internal/mux"
)

// tunnelResolver returns a resolver whose queries go through the tunnel's
// remote DNS, so answers match the tunnel exit rather than the local network.
// Used where a hostname must become an IP on this side, e.g. the server's
// UDP handler only accepts IP destinations.
func tunnelResolver(client mux.TunnelClient) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return &dnsShimConn{client: client}, nil
		},
	}
}

// dnsShimConn is a fake stream conn for net.Resolver: Go frames each query
// with a 2-byte length (it is not a PacketConn), we forward the message via
// DNSRequest and hand back the length-framed response.
type dnsShimConn struct {
	client mux.TunnelClient
	in     bytes.Buffer
	out    bytes.Buffer
}

func (c *dnsShimConn) Write(p []byte) (int, error) {
	c.in.Write(p)
	for c.in.Len() >= 2 {
		n := int(binary.BigEndian.Uint16(c.in.Bytes()[:2]))
		if c.in.Len() < 2+n {
			break
		}
		c.in.Next(2)
		query := append([]byte(nil), c.in.Next(n)...)
		resp, err := c.client.DNSRequest(query)
		if err != nil {
			return 0, err
		}
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(resp)))
		c.out.Write(l[:])
		c.out.Write(resp)
	}
	return len(p), nil
}

func (c *dnsShimConn) Read(p []byte) (int, error) {
	if c.out.Len() == 0 {
		return 0, io.EOF
	}
	return c.out.Read(p)
}

func (c *dnsShimConn) Close() error                     { return nil }
func (c *dnsShimConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *dnsShimConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *dnsShimConn) SetDeadline(time.Time) error      { return nil }
func (c *dnsShimConn) SetReadDeadline(time.Time) error  { return nil }
func (c *dnsShimConn) SetWriteDeadline(time.Time) error { return nil }
