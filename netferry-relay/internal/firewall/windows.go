//go:build windows

package firewall

import (
	"fmt"
	"net"
	"os/exec"
)

func newDefault() Method { return &winMethod{} }

func newNamed(name string) (Method, error) {
	if name == "auto" || name == "win" || name == "socks5" {
		return &winMethod{}, nil
	}
	return nil, fmt.Errorf("firewall method %q not supported on Windows (only socks5/win)", name)
}

// CleanStaleAnchors is a no-op on Windows.
func CleanStaleAnchors() {}

// QueryOrigDst is not used in SOCKS5 mode — the destination is embedded in the
// SOCKS5 protocol header. Satisfies the interface but should not be called.
func QueryOrigDst(_ net.Conn) (string, int, error) {
	return "", 0, fmt.Errorf("QueryOrigDst not applicable in SOCKS5 mode")
}

// winMethod configures the Windows system proxy to point at our SOCKS5 listener.
// Applications that honour the WinINET/WinHTTP system proxy are automatically
// tunnelled. This does not require kernel drivers or administrator privileges.
type winMethod struct {
	proxyPort int
}

func (w *winMethod) Name() string { return "socks5" }

func (w *winMethod) Setup(subnets, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	w.proxyPort = proxyPort
	proxy := fmt.Sprintf("socks=127.0.0.1:%d", proxyPort)

	// WinHTTP proxy (affects command-line tools, WinHTTP-based apps).
	exec.Command("netsh", "winhttp", "set", "proxy", proxy).Run()

	// WinINET proxy (IE, Edge, Chrome, curl, Python requests, etc.).
	const regPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	exec.Command("reg", "add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f").Run()
	exec.Command("reg", "add", regPath, "/v", "ProxyServer", "/t", "REG_SZ", "/d", proxy, "/f").Run()

	return nil
}

func (w *winMethod) Restore() error {
	const regPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	exec.Command("netsh", "winhttp", "reset", "proxy").Run()
	exec.Command("reg", "add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f").Run()
	exec.Command("reg", "delete", regPath, "/v", "ProxyServer", "/f").Run()
	return nil
}
