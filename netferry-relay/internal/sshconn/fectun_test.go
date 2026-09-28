package sshconn

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoveychen/fectun"
	"golang.org/x/crypto/ssh"
)

// startSSHServer runs a minimal in-process sshd that accepts clientPub and
// answers every global request with ok. Returns its TCP address.
func startSSHServer(t *testing.T, clientPub ssh.PublicKey) string {
	t.Helper()
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostPriv)
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if string(k.Marshal()) == string(clientPub.Marshal()) {
				return nil, nil
			}
			return nil, errUnauthorized
		},
	}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go func() {
					for ch := range chans {
						ch.Reject(ssh.Prohibited, "no channels")
					}
				}()
				for r := range reqs {
					if r.WantReply {
						r.Reply(true, nil)
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

type unauthorized struct{}

func (unauthorized) Error() string { return "unauthorized" }

var errUnauthorized = unauthorized{}

// startFectunServer puts a multi-peer fectun server in front of target.
func startFectunServer(t *testing.T, target, key string) int {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := fectun.NewServer(conn, target, fectun.Options{RateMbps: 100, Key: []byte(key)})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() { srv.Close() })
	return conn.LocalAddr().(*net.UDPAddr).Port
}

func testIdentity(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	sshPub, _ := ssh.NewPublicKey(pub)
	return string(pem.EncodeToMemory(block)), sshPub
}

func resetFectunClients(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		fectunClients.Lock()
		for k, e := range fectunClients.m {
			e.cli.Close()
			delete(fectunClients.m, k)
		}
		fectunClients.Unlock()
	})
}

// fakeBootstrap stands in for `server --fectun-up`: it hands out whatever
// key() returns and counts the calls. It also forgets cached keys.
func fakeBootstrap(t *testing.T, key func(restart bool) string) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	SetFectunBootstrap(func(c *ssh.Client, fc *FectunConfig, restart bool) (string, error) {
		n.Add(1)
		return key(restart), nil
	})
	reset := func() {
		fectunPeers.Lock()
		fectunPeers.m = make(map[string]*fectunPeer)
		fectunPeers.Unlock()
	}
	reset()
	t.Cleanup(func() { SetFectunBootstrap(nil); reset() })
	return &n
}

func constKey(k string) func(bool) string { return func(bool) string { return k } }

// hostFor points a HostConfig at the in-process sshd, which both the TCP
// bootstrap and the fectun server's target use.
func hostFor(t *testing.T, sshd string, fc *FectunConfig) *HostConfig {
	t.Helper()
	host, p, _ := net.SplitHostPort(sshd)
	port, _ := strconv.Atoi(p)
	return &HostConfig{User: "u", HostName: host, Port: port, Fectun: fc}
}

// Direct dial with Fectun set: SSH must complete over the fectun stream,
// firstHopIP must be the UDP peer, and a second Dial must reuse the same
// shared client instead of opening a second UDP session.
func TestDialOverFectun(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	resetFectunClients(t)
	pemKey, pub := testIdentity(t)
	sshd := startSSHServer(t, pub)
	port := startFectunServer(t, sshd, "k3y")
	boots := fakeBootstrap(t, constKey("k3y"))

	hc := hostFor(t, sshd, &FectunConfig{Port: port, RateMbps: 100})
	ac := AuthConfig{IdentityPEM: pemKey}

	c1, ip, err := Dial(hc, ac)
	if err != nil {
		t.Fatalf("dial over fectun: %v", err)
	}
	defer c1.Close()
	if ip == nil || !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("firstHopIP = %v, want 127.0.0.1", ip)
	}
	if ok, _, err := c1.SendRequest("keepalive@openssh.com", true, nil); err != nil || !ok {
		t.Fatalf("request over fectun: ok=%v err=%v", ok, err)
	}

	c2, _, err := Dial(hc, ac)
	if err != nil {
		t.Fatalf("second dial: %v", err)
	}
	defer c2.Close()
	fectunClients.Lock()
	n := len(fectunClients.m)
	refs := 0
	for _, e := range fectunClients.m {
		refs = e.refs
	}
	fectunClients.Unlock()
	if n != 1 || refs != 2 {
		t.Fatalf("shared clients=%d refs=%d, want 1 client with 2 refs", n, refs)
	}
	if b := boots.Load(); b != 1 {
		t.Fatalf("bootstraps = %d, want 1 (second dial must reuse the cached key)", b)
	}
}

// A cached key that the daemon no longer accepts (it was restarted with a
// new key file, or the host was rebuilt) must trigger one re-bootstrap.
func TestDialOverFectunStaleKeyRebootstraps(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	resetFectunClients(t)
	old := fectunHandshakeTimeout
	fectunHandshakeTimeout = time.Second
	t.Cleanup(func() { fectunHandshakeTimeout = old })

	pemKey, pub := testIdentity(t)
	sshd := startSSHServer(t, pub)
	port := startFectunServer(t, sshd, "fresh")
	boots := fakeBootstrap(t, constKey("fresh"))
	hc := hostFor(t, sshd, &FectunConfig{Port: port})
	fectunPeerFor(net.JoinHostPort(hc.HostName, strconv.Itoa(port))).key = "stale"

	c, _, err := Dial(hc, AuthConfig{IdentityPEM: pemKey})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close()
	if b := boots.Load(); b != 1 {
		t.Fatalf("bootstraps = %d, want 1", b)
	}
}

// Once every stream on a shared client is closed, the client must be torn
// down after fectunIdleClose — a disconnected mobile engine must not keep
// sending heartbeats forever.
func TestFectunClientClosedWhenIdle(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	resetFectunClients(t)
	old := fectunIdleClose
	fectunIdleClose = 200 * time.Millisecond
	t.Cleanup(func() { fectunIdleClose = old })

	pemKey, pub := testIdentity(t)
	sshd := startSSHServer(t, pub)
	port := startFectunServer(t, sshd, "k")
	fakeBootstrap(t, constKey("k"))
	hc := hostFor(t, sshd, &FectunConfig{Port: port})
	c, _, err := Dial(hc, AuthConfig{IdentityPEM: pemKey})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	deadline := time.Now().Add(3 * time.Second)
	for {
		fectunClients.Lock()
		n := len(fectunClients.m)
		fectunClients.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("idle shared client was not closed")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// When the UDP path never works (port blocked, foreign process on it) the
// dial must give up after bootstrap + restart, each bounded by the
// handshake timeout since there is no TCP connect step to fail, and say
// which UDP port to check.
func TestDialOverFectunUnreachableFails(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	resetFectunClients(t)
	old := fectunHandshakeTimeout
	fectunHandshakeTimeout = time.Second
	t.Cleanup(func() { fectunHandshakeTimeout = old })
	pemKey, pub := testIdentity(t)
	sshd := startSSHServer(t, pub)
	port := startFectunServer(t, sshd, "right")
	var restarts atomic.Int32
	boots := fakeBootstrap(t, func(restart bool) string {
		if restart {
			restarts.Add(1)
		}
		return "wrong"
	})
	hc := hostFor(t, sshd, &FectunConfig{Port: port})

	done := make(chan error, 1)
	go func() {
		c, _, err := Dial(hc, AuthConfig{IdentityPEM: pemKey})
		if c != nil {
			c.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "handshake") ||
			!strings.Contains(err.Error(), "UDP port "+strconv.Itoa(port)) {
			t.Fatalf("want handshake error naming the UDP port, got %v", err)
		}
		if boots.Load() != 2 || restarts.Load() != 1 {
			t.Fatalf("bootstraps=%d restarts=%d, want 2 and 1", boots.Load(), restarts.Load())
		}
	case <-time.After(2*fectunHandshakeTimeout + 10*time.Second):
		t.Fatal("dial hung past the handshake timeouts")
	}
}
