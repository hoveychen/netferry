package sshconn

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const dialTimeout = 30 * time.Second

// JumpHostSpec describes an explicit jump host, independent of ~/.ssh/config.
type JumpHostSpec struct {
	// Remote in [user@]host[:port] format.
	Remote string `json:"remote"`
	// IdentityFile path (optional).
	IdentityFile string `json:"identityFile,omitempty"`
	// IdentityPEM is inline PEM key material (not serialized to JSON).
	// Populated from NETFERRY_JUMP_KEY_{i} env vars by the caller.
	IdentityPEM string `json:"-"`
}

// Dial establishes an *ssh.Client using HostConfig + AuthConfig.
// It handles ProxyJump and ProxyCommand transparently.
// If jumpHosts is non-empty, they are used instead of HostConfig.ProxyJump.
func Dial(hc *HostConfig, ac AuthConfig, jumpHosts ...JumpHostSpec) (*ssh.Client, error) {
	log.Printf("ssh-dial: target=%s@%s:%d identityFile=%q jumpHosts=%d",
		hc.User, hc.HostName, hc.Port, ac.IdentityFile, len(jumpHosts))

	// Merge HostConfig overrides into AuthConfig.
	if hc.IdentityFile != "" && ac.IdentityFile == "" {
		ac.IdentityFile = hc.IdentityFile
	}

	clientCfg, err := BuildSSHConfig(hc.User, ac)
	if err != nil {
		return nil, err
	}

	addr := net.JoinHostPort(hc.HostName, strconv.Itoa(hc.Port))

	// Explicit jump hosts take precedence over everything.
	if len(jumpHosts) > 0 {
		return dialViaExplicitJumps(jumpHosts, addr, clientCfg, ac)
	}

	// ProxyCommand takes precedence over ProxyJump.
	if hc.ProxyCommand != "" {
		return dialViaProxyCommand(hc.ProxyCommand, hc.HostName, hc.Port, clientCfg)
	}

	if hc.ProxyJump != "" {
		return dialViaProxyJump(hc.ProxyJump, addr, clientCfg, ac)
	}

	// Direct connection.
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	setTCPKeepAlive(conn)
	return sshClientFromConn(conn, addr, clientCfg)
}

// sshClientFromConn upgrades a raw net.Conn to an *ssh.Client.
func sshClientFromConn(conn net.Conn, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// dialViaProxyJump connects through one or more jump hosts.
// jumpSpec may be a comma-separated list: "jump1,jump2,...,target" (OpenSSH style).
func dialViaProxyJump(jumpSpec, targetAddr string, targetCfg *ssh.ClientConfig, ac AuthConfig) (*ssh.Client, error) {
	jumps := strings.Split(jumpSpec, ",")
	if len(jumps) == 0 {
		return nil, fmt.Errorf("empty ProxyJump")
	}

	var current net.Conn
	var currentClient *ssh.Client

	for i, jump := range jumps {
		jumpHC, err := ParseSSHConfig(strings.TrimSpace(jump))
		if err != nil {
			return nil, fmt.Errorf("ProxyJump[%d] %q: %w", i, jump, err)
		}
		jumpAddr := net.JoinHostPort(jumpHC.HostName, strconv.Itoa(jumpHC.Port))

		var conn net.Conn
		if currentClient == nil {
			conn, err = net.DialTimeout("tcp", jumpAddr, dialTimeout)
			if err != nil {
				return nil, fmt.Errorf("ProxyJump dial %s: %w", jumpAddr, err)
			}
			setTCPKeepAlive(conn)
		} else {
			// Dial next hop through the previous SSH client.
			conn, err = currentClient.Dial("tcp", jumpAddr)
			if err != nil {
				return nil, fmt.Errorf("ProxyJump[%d] dial %s: %w", i, jumpAddr, err)
			}
		}

		jumpAC := ac
		if jumpHC.IdentityFile != "" {
			jumpAC.IdentityFile = jumpHC.IdentityFile
		}
		jumpCfg, err := BuildSSHConfig(jumpHC.User, jumpAC)
		if err != nil {
			conn.Close()
			return nil, err
		}
		currentClient, err = sshClientFromConn(conn, jumpAddr, jumpCfg)
		if err != nil {
			return nil, err
		}
		current = conn
	}
	_ = current

	// Final hop: dial target through last jump client.
	finalConn, err := currentClient.Dial("tcp", targetAddr)
	if err != nil {
		return nil, fmt.Errorf("ProxyJump final dial %s: %w", targetAddr, err)
	}
	return sshClientFromConn(finalConn, targetAddr, targetCfg)
}

// dialViaExplicitJumps connects through one or more explicitly specified jump hosts.
// Unlike dialViaProxyJump, it does NOT consult ~/.ssh/config for each hop.
func dialViaExplicitJumps(jumps []JumpHostSpec, targetAddr string, targetCfg *ssh.ClientConfig, ac AuthConfig) (*ssh.Client, error) {
	var currentClient *ssh.Client

	for i, jh := range jumps {
		log.Printf("jump[%d]: remote=%q identityFile=%q", i, jh.Remote, jh.IdentityFile)
		user, host := splitUserHost(jh.Remote)
		port := 22
		if idx := strings.LastIndex(host, ":"); idx >= 0 {
			if p, err := strconv.Atoi(host[idx+1:]); err == nil {
				port = p
				host = host[:idx]
			}
		}
		if user == "" {
			user = os.Getenv("USER")
			if user == "" {
				user = "root"
			}
		}

		jumpAddr := net.JoinHostPort(host, strconv.Itoa(port))

		var conn net.Conn
		var err error
		if currentClient == nil {
			conn, err = net.DialTimeout("tcp", jumpAddr, dialTimeout)
			if err != nil {
				return nil, fmt.Errorf("jump[%d] dial %s: %w", i, jumpAddr, err)
			}
			setTCPKeepAlive(conn)
		} else {
			conn, err = currentClient.Dial("tcp", jumpAddr)
			if err != nil {
				return nil, fmt.Errorf("jump[%d] dial %s: %w", i, jumpAddr, err)
			}
		}
		log.Printf("jump[%d]: TCP connected to %s", i, jumpAddr)

		jumpAC := ac
		if jh.IdentityPEM != "" {
			jumpAC.IdentityPEM = jh.IdentityPEM
		} else if jh.IdentityFile != "" {
			jumpAC.IdentityFile = jh.IdentityFile
		}
		jumpCfg, err := BuildSSHConfig(user, jumpAC)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("jump[%d] auth: %w", i, err)
		}
		currentClient, err = sshClientFromConn(conn, jumpAddr, jumpCfg)
		if err != nil {
			return nil, fmt.Errorf("jump[%d] handshake: %w", i, err)
		}
		log.Printf("jump[%d]: SSH handshake succeeded", i)
	}

	// Final hop: dial target through last jump client.
	finalConn, err := currentClient.Dial("tcp", targetAddr)
	if err != nil {
		return nil, fmt.Errorf("jump final dial %s: %w", targetAddr, err)
	}
	return sshClientFromConn(finalConn, targetAddr, targetCfg)
}

// dialViaProxyCommand runs the ProxyCommand and wraps its stdio as a net.Conn.
func dialViaProxyCommand(proxyCmd, host string, port int, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	// Replace standard placeholders.
	cmd := proxyCmd
	cmd = strings.ReplaceAll(cmd, "%h", host)
	cmd = strings.ReplaceAll(cmd, "%p", fmt.Sprintf("%d", port))

	c := exec.Command("sh", "-c", cmd)
	stdin, err := c.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ProxyCommand stdin: %w", err)
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ProxyCommand stdout: %w", err)
	}
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("ProxyCommand start: %w", err)
	}

	conn := &proxyConn{r: stdout, w: stdin, cmd: c}
	addr := fmt.Sprintf("%s:%d", host, port)
	return sshClientFromConn(conn, addr, cfg)
}

// proxyConn wraps a subprocess's stdin/stdout as net.Conn.
type proxyConn struct {
	r   io.ReadCloser
	w   io.WriteCloser
	cmd *exec.Cmd
}

func (p *proxyConn) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *proxyConn) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *proxyConn) Close() error {
	p.r.Close()
	p.w.Close()
	p.cmd.Wait()
	return nil
}
func (p *proxyConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (p *proxyConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (p *proxyConn) SetDeadline(t time.Time) error    { return nil }
func (p *proxyConn) SetReadDeadline(t time.Time) error { return nil }
func (p *proxyConn) SetWriteDeadline(t time.Time) error { return nil }

// setTCPOpts enables TCP keepalive and disables Nagle's algorithm on the
// connection. NoDelay ensures that small control frames (WINDOW_UPDATE,
// PING/PONG) are sent immediately without waiting to coalesce with data,
// which is critical for flow control responsiveness.
func setTCPKeepAlive(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(15 * time.Second)
		tc.SetNoDelay(true)
	}
}

// StartSSHKeepalive sends SSH-protocol-level keepalive requests periodically.
// This is distinct from TCP keepalive: it forces data through the SSH
// transport, which lets the SSH library detect dead connections (e.g. after a
// NAT timeout or abrupt network drop) on platforms like Windows where the OS
// may not surface TCP errors promptly.
//
// The returned stop function cancels the keepalive goroutine. If the SSH
// connection dies, the goroutine exits automatically (SendRequest will return
// an error and the ssh.Client will close all channels).
func StartSSHKeepalive(client *ssh.Client, interval time.Duration) (stop func()) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
					// Connection is dead; ssh.Client has already started
					// shutting down all channels.
					log.Printf("ssh keepalive: connection lost: %v", err)
					return
				}
			}
		}
	}()
	return func() { close(done) }
}
