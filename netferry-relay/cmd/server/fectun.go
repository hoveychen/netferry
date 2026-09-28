//go:build unix

package main

// fectun support: the client carries its SSH connection to this host over
// fectun (FEC over UDP). It first reaches us over plain TCP SSH and runs
// `server --fectun-up`, which makes sure a detached `server --fectun-serve`
// daemon listens on the UDP port and prints the pre-shared key it uses. The
// daemon forwards every fectun stream to the local sshd, then exits on its
// own once no client has been around for fectunIdleExit.
//
// Per-port state lives next to the server binary (the deploy cache dir):
//
//	fectun-<port>.key   pre-shared key, 0600; generated once, shared by every
//	                    client of this user so several devices can coexist
//	fectun-<port>.pid   daemon pid, so --fectun-restart can replace it
//	fectun-<port>.log   daemon output
//	fectun-<port>.lock  serialises concurrent --fectun-up runs

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hoveychen/fectun"
)

const fectunKeyEnv = "NETFERRY_FECTUN_KEY"

// fectunIdleExit is how long the daemon keeps running with no client
// session. fectun reaps a silent peer after 30s, so this is measured from
// the last peer going away.
var fectunIdleExit = 10 * time.Minute

type fectunArgs struct {
	port    int
	rate    float64
	target  string
	restart bool
}

func fectunStatePath(port int, ext string) string {
	dir := os.TempDir()
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	return filepath.Join(dir, fmt.Sprintf("fectun-%d.%s", port, ext))
}

// fectunTarget is the sshd this SSH session came in through: SSH_CONNECTION
// is "client_ip client_port server_ip server_port".
func fectunTarget() string {
	if f := strings.Fields(os.Getenv("SSH_CONNECTION")); len(f) == 4 {
		return net.JoinHostPort(f[2], f[3])
	}
	return "127.0.0.1:22"
}

// runFectunUp ensures the daemon is running and prints its key as
// "fectun-key <hex>" on stdout for the client to parse.
func runFectunUp(a fectunArgs) error {
	if a.port <= 0 || a.port > 65535 {
		return fmt.Errorf("invalid --fectun-port %d", a.port)
	}
	lock, err := os.OpenFile(fectunStatePath(a.port, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock: %w", err)
	}

	key, err := loadOrCreateFectunKey(a.port)
	if err != nil {
		return err
	}
	if a.restart {
		stopFectunDaemon(a.port)
	}
	if running, err := fectunPortInUse(a.port); err != nil {
		return err
	} else if !running {
		if err := startFectunDaemon(a, key); err != nil {
			return err
		}
	}
	fmt.Printf("fectun-key %s\n", key)
	return nil
}

func loadOrCreateFectunKey(port int) (string, error) {
	path := fectunStatePath(port, "key")
	if b, err := os.ReadFile(path); err == nil {
		if k := strings.TrimSpace(string(b)); k != "" {
			return k, nil
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	k := hex.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(k+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write key: %w", err)
	}
	return k, nil
}

// fectunPortInUse reports whether the UDP port is already bound — by our
// daemon, normally. A foreign process on the port shows up later as a
// client handshake timeout.
func fectunPortInUse(port int) (bool, error) {
	c, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
	if err == nil {
		c.Close()
		return false, nil
	}
	if errno, ok := unwrapErrno(err); ok && errno == syscall.EADDRINUSE {
		return true, nil
	}
	return false, fmt.Errorf("probe udp port %d: %w", port, err)
}

func unwrapErrno(err error) (syscall.Errno, bool) {
	for err != nil {
		if e, ok := err.(syscall.Errno); ok {
			return e, true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return 0, false
		}
		err = u.Unwrap()
	}
	return 0, false
}

func stopFectunDaemon(port int) {
	b, err := os.ReadFile(fectunStatePath(port, "pid"))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return
	}
	// Only signal it if it still looks like our daemon: pids get reused.
	if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil &&
		!strings.Contains(string(cmdline), "--fectun-serve") {
		return
	}
	if syscall.Kill(pid, syscall.SIGTERM) != nil {
		return
	}
	for i := 0; i < 30; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	time.Sleep(200 * time.Millisecond)
}

// startFectunDaemon launches `server --fectun-serve` in its own session so it
// outlives this SSH session, and waits until it has bound the port.
func startFectunDaemon(a fectunArgs, key string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(fectunStatePath(a.port, "log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	ready, readyW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer ready.Close()

	cmd := exec.Command(exe, "--fectun-serve",
		"--fectun-port", strconv.Itoa(a.port),
		"--fectun-rate", strconv.FormatFloat(a.rate, 'g', -1, 64),
		"--fectun-target", a.target)
	// The key goes through the environment, not argv, so `ps` can't see it.
	cmd.Env = append(os.Environ(), fectunKeyEnv+"="+key)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.ExtraFiles = []*os.File{readyW} // fd 3
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		readyW.Close()
		return fmt.Errorf("start fectun daemon: %w", err)
	}
	readyW.Close()
	cmd.Process.Release()

	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(ready).ReadString('\n')
		line <- strings.TrimSpace(s)
	}()
	select {
	case s := <-line:
		if s != "ready" {
			return fmt.Errorf("fectun daemon failed to start (%q); see %s", s, fectunStatePath(a.port, "log"))
		}
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("fectun daemon did not report ready; see %s", fectunStatePath(a.port, "log"))
	}
}

// runFectunServe is the daemon body.
func runFectunServe(a fectunArgs) error {
	key := os.Getenv(fectunKeyEnv)
	ready := os.NewFile(3, "ready")
	fail := func(err error) error {
		if ready != nil {
			fmt.Fprintf(ready, "error: %v\n", err)
			ready.Close()
		}
		return err
	}
	if key == "" {
		return fail(fmt.Errorf("%s not set", fectunKeyEnv))
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: a.port})
	if err != nil {
		return fail(err)
	}
	conn.SetReadBuffer(4 << 20)
	conn.SetWriteBuffer(4 << 20)
	srv, err := fectun.NewServer(conn, a.target, fectun.Options{RateMbps: a.rate, Key: []byte(key)})
	if err != nil {
		conn.Close()
		return fail(err)
	}
	pidPath := fectunStatePath(a.port, "pid")
	os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	defer os.Remove(pidPath)

	fectun.Logf("fectun daemon %s: udp :%d → %s (rate=%g Mbps/peer)", Version, a.port, a.target, a.rate)
	if ready != nil {
		fmt.Fprintln(ready, "ready")
		ready.Close()
	}

	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	lastBusy := time.Now()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-tick.C:
			if srv.Peers() > 0 {
				lastBusy = time.Now()
			} else if time.Since(lastBusy) >= fectunIdleExit {
				fectun.Logf("fectun daemon: idle for %s, exiting", fectunIdleExit)
				return srv.Close()
			}
		}
	}
}
