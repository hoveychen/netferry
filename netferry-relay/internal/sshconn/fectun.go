package sshconn

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/hoveychen/fectun"
)

// FectunConfig selects fectun (Reed-Solomon FEC over UDP) as the transport
// for the first raw hop. The hop's host must run a multi-peer fectun server
// on Port whose -target is that host's sshd; the SSH port in the remote spec
// is then irrelevant because the server decides where streams land.
//
// FEC trades bandwidth for loss tolerance: at the default K/M=20/15 about
// 43% of the line rate is parity. It only pays off on links with heavy
// random loss where TCP (even with BBR) collapses.
type FectunConfig struct {
	Port int `json:"port"`
	// Key is the pre-shared key; must match the server's. Empty disables
	// packet authentication, which lets anyone reach the server's target.
	Key string `json:"key,omitempty"`
	// K/M are the data/parity shard counts (0 = fectun default). The
	// server adopts whatever the client sends.
	K int `json:"k,omitempty"`
	M int `json:"m,omitempty"`
	// RateMbps caps this client's upload line rate including parity
	// (0 = fectun default). Never set it above the link's real capacity.
	RateMbps float64 `json:"rateMbps,omitempty"`
}

// Enabled reports whether fc selects fectun.
func (fc *FectunConfig) Enabled() bool { return fc != nil && fc.Port > 0 }

func (fc *FectunConfig) options() fectun.Options {
	o := fectun.Options{K: fc.K, M: fc.M, RateMbps: fc.RateMbps}
	if fc.Key != "" {
		o.Key = []byte(fc.Key)
	}
	return o
}

// udpListenFunc overrides how the fectun UDP socket is created, so mobile
// platforms can VpnService.protect() it before any packet leaves.
var udpListenFunc func() (*net.UDPConn, error)

// SetUDPListenFunc sets a custom UDP socket factory for fectun transports.
// Pass nil to restore the default (unbound ephemeral port).
func SetUDPListenFunc(fn func() (*net.UDPConn, error)) {
	udpListenFunc = fn
}

func listenUDP() (*net.UDPConn, error) {
	if udpListenFunc != nil {
		return udpListenFunc()
	}
	c, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	c.SetReadBuffer(4 << 20)
	c.SetWriteBuffer(4 << 20)
	return c, nil
}

// fectunIdleClose is how long a shared fectun client lingers after its last
// stream closes. Long enough to absorb a reconnect storm (every pool member
// re-dials within seconds of a drop) without tearing the UDP session down
// and back up; short enough that a disconnected mobile engine stops sending
// heartbeats (10/s) soon after.
var fectunIdleClose = 60 * time.Second

// fectunHandshakeTimeout bounds the SSH handshake over a fectun stream.
// OpenStream returns immediately even when the server is unreachable, so
// without a deadline the handshake would block forever — there is no TCP
// connect step to time out.
const fectunHandshakeTimeout = 30 * time.Second

// fectunClients shares one fectun.Client per (peer, parameters). Every SSH
// connection to the same hop (pool members, split ctrl/data, reconnects)
// must ride the same client: each client enforces RateMbps on its own, so
// one client per connection would multiply the real send rate by the pool
// size and overrun the link.
var fectunClients = struct {
	sync.Mutex
	m map[string]*fectunEntry
}{m: make(map[string]*fectunEntry)}

type fectunEntry struct {
	key  string
	cli  *fectun.Client
	refs int
	idle *time.Timer
}

func (e *fectunEntry) release() {
	fectunClients.Lock()
	defer fectunClients.Unlock()
	e.refs--
	if e.refs > 0 {
		return
	}
	e.idle = time.AfterFunc(fectunIdleClose, func() {
		fectunClients.Lock()
		if e.refs > 0 || fectunClients.m[e.key] != e {
			fectunClients.Unlock()
			return
		}
		delete(fectunClients.m, e.key)
		fectunClients.Unlock()
		log.Printf("fectun: closing idle client to %s (%s)", e.cli.Peer(), e.cli.Stats())
		e.cli.Close()
	})
}

// dialFectun opens a fectun stream to host:fc.Port.
func dialFectun(host string, fc *FectunConfig) (net.Conn, error) {
	raddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(fc.Port)))
	if err != nil {
		return nil, fmt.Errorf("fectun resolve %s: %w", host, err)
	}
	key := fmt.Sprintf("%s|%d|%d|%g|%s", raddr, fc.K, fc.M, fc.RateMbps, fc.Key)

	fectunClients.Lock()
	e := fectunClients.m[key]
	if e == nil {
		conn, err := listenUDP()
		if err != nil {
			fectunClients.Unlock()
			return nil, fmt.Errorf("fectun udp socket: %w", err)
		}
		cli, err := fectun.NewClient(conn, raddr, fc.options())
		if err != nil {
			fectunClients.Unlock()
			conn.Close()
			return nil, err
		}
		e = &fectunEntry{key: key, cli: cli}
		fectunClients.m[key] = e
		log.Printf("fectun: new client to %s (k=%d m=%d rate=%g auth=%v)",
			raddr, fc.K, fc.M, fc.RateMbps, fc.Key != "")
	}
	if e.idle != nil {
		e.idle.Stop()
		e.idle = nil
	}
	e.refs++
	fectunClients.Unlock()

	return &fectunConn{Conn: e.cli.OpenStream(), entry: e}, nil
}

// fectunConn is one stream on a shared client; Close drops its reference.
type fectunConn struct {
	net.Conn
	entry *fectunEntry
	once  sync.Once
}

func (c *fectunConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.entry.release)
	return err
}

func init() {
	// fectun logs every stream open/close to stdout by default; route it
	// through our logger instead so it lands next to the SSH dial lines.
	fectun.Logf = log.Printf
}
