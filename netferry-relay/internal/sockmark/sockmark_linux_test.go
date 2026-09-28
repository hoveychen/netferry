//go:build linux && !android

package sockmark

import (
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

func soMark(t *testing.T, c syscall.Conn) int {
	t.Helper()
	rc, err := c.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var mark int
	var gerr error
	rc.Control(func(fd uintptr) {
		mark, gerr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK)
	})
	if gerr != nil {
		t.Fatalf("getsockopt SO_MARK: %v", gerr)
	}
	return mark
}

func TestDialTimeoutAndListenUDPSetMark(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("SO_MARK requires CAP_NET_ADMIN")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	c, err := DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("DialTimeout: %v", err)
	}
	defer c.Close()
	if got := soMark(t, c.(*net.TCPConn)); got != Bypass {
		t.Errorf("TCP SO_MARK = %#x, want %#x", got, Bypass)
	}

	u, err := ListenUDP()
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer u.Close()
	if got := soMark(t, u); got != Bypass {
		t.Errorf("UDP SO_MARK = %#x, want %#x", got, Bypass)
	}
}

// Without CAP_NET_ADMIN the mark cannot be set, but the dial must still work.
func TestDialTimeoutWorksUnprivileged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("covers the unprivileged path")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c, err := DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("DialTimeout: %v", err)
	}
	c.Close()
}
