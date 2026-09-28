package sshconn

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/hoveychen/fectun"
	"golang.org/x/crypto/ssh"
)

// FectunConfig selects fectun (Reed-Solomon FEC over UDP) as the transport
// for the first raw hop. Nothing needs to be installed on that host: the
// client first reaches it over plain TCP SSH, deploys the server binary and
// runs `server --fectun-up`, which starts a fectun daemon on UDP Port that
// forwards to the host's sshd and hands back the daemon's key (see
// SetFectunBootstrap). Only UDP Port must be open in the host's firewall.
//
// FEC trades bandwidth for loss tolerance: at the default K/M=20/15 about
// 43% of the line rate is parity. It only pays off on links with heavy
// random loss where TCP (even with BBR) collapses.
type FectunConfig struct {
	Port int `json:"port"`
	// Key is the daemon's pre-shared key. Runtime only: it is learned from
	// the bootstrap, never configured.
	Key string `json:"-"`
	// K/M are the data/parity shard counts (0 = fectun default). The
	// server adopts whatever the client sends.
	K int `json:"k,omitempty"`
	M int `json:"m,omitempty"`
	// RateMbps caps the line rate including parity in both directions: it
	// limits this client's uploads and is passed to the daemon it brings up
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
var fectunHandshakeTimeout = 15 * time.Second

// FectunBootstrapFunc brings up the fectun daemon on the host behind c (a
// plain TCP SSH connection) and returns its key. restart asks it to replace
// a daemon that is already running.
type FectunBootstrapFunc func(c *ssh.Client, fc *FectunConfig, restart bool) (key string, err error)

var fectunBootstrap FectunBootstrapFunc

// SetFectunBootstrap installs the bootstrap used by fectun first hops. The
// binary that embeds the server builds (tunnel, mobile engine) must set it.
func SetFectunBootstrap(fn FectunBootstrapFunc) {
	fectunBootstrap = fn
}

// fectunPeer caches the key learned for one fectun endpoint (host:udpPort)
// so pool members and reconnects skip the TCP bootstrap. gen counts
// bootstraps: a caller whose key went stale only bootstraps again if nobody
// else has done so since it read the key, so a reconnect storm costs one
// bootstrap, not one per pool member.
type fectunPeer struct {
	mu  sync.Mutex
	key string
	gen int
}

var fectunPeers = struct {
	sync.Mutex
	m map[string]*fectunPeer
}{m: make(map[string]*fectunPeer)}

func fectunPeerFor(endpoint string) *fectunPeer {
	fectunPeers.Lock()
	defer fectunPeers.Unlock()
	p := fectunPeers.m[endpoint]
	if p == nil {
		p = &fectunPeer{}
		fectunPeers.m[endpoint] = p
	}
	return p
}

func (p *fectunPeer) current() (string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.key, p.gen
}

// refresh runs boot unless another caller already bootstrapped after seen,
// in which case it returns that caller's key.
func (p *fectunPeer) refresh(seen int, boot func() (string, error)) (string, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gen != seen && p.key != "" {
		return p.key, p.gen, nil
	}
	key, err := boot()
	if err != nil {
		return "", p.gen, err
	}
	p.key = key
	p.gen++
	return p.key, p.gen, nil
}

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

// fectunFirstHop opens the first-hop SSH client over fectun. It tries the
// cached key first; when there is none or it no longer works (daemon
// restarted, idled out, host rebooted) it bootstraps the daemon over TCP
// SSH and retries, and as a last resort restarts the daemon.
func fectunFirstHop(addr string, cfg *ssh.ClientConfig, fc *FectunConfig) (*ssh.Client, net.IP, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, nil, err
	}
	endpoint := net.JoinHostPort(host, strconv.Itoa(fc.Port))
	peer := fectunPeerFor(endpoint)

	try := func(key string) (*ssh.Client, net.IP, error) {
		f := *fc
		f.Key = key
		conn, err := dialFectun(host, &f)
		if err != nil {
			return nil, nil, err
		}
		ip := remoteIPFromConn(conn)
		c, err := sshClientFromConn(conn, addr, cfg)
		if err != nil {
			return nil, nil, err
		}
		return c, ip, nil
	}
	boot := func(restart bool) func() (string, error) {
		return func() (string, error) {
			if fectunBootstrap == nil {
				return "", errors.New("fectun bootstrap not configured")
			}
			log.Printf("fectun: bringing up daemon on %s over TCP SSH (restart=%v)", endpoint, restart)
			c, _, err := tcpClient(addr, cfg)
			if err != nil {
				return "", fmt.Errorf("fectun bootstrap: %w", err)
			}
			defer c.Close()
			key, err := fectunBootstrap(c, fc, restart)
			if err != nil {
				return "", fmt.Errorf("fectun bootstrap: %w", err)
			}
			return key, nil
		}
	}

	key, gen := peer.current()
	var lastErr error
	if key != "" {
		c, ip, err := try(key)
		if err == nil {
			return c, ip, nil
		}
		log.Printf("fectun: %s with cached key: %v", endpoint, err)
		lastErr = err
	}
	for _, restart := range []bool{false, true} {
		key, gen, err = peer.refresh(gen, boot(restart))
		if err != nil {
			return nil, nil, err
		}
		c, ip, err := try(key)
		if err == nil {
			return c, ip, nil
		}
		log.Printf("fectun: %s after bootstrap (restart=%v): %v", endpoint, restart, err)
		lastErr = err
	}
	return nil, nil, fmt.Errorf("fectun via UDP %s: %w (is UDP port %d open in the server's firewall / security group?)",
		endpoint, lastErr, fc.Port)
}
