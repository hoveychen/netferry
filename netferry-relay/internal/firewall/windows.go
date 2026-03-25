//go:build windows

package firewall

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"syscall"
)

func newDefault() Method {
	if winDivertAvailable() {
		return &winDivertMethod{}
	}
	return &winMethod{}
}

func newNamed(name string) (Method, error) {
	switch name {
	case "auto":
		return newDefault(), nil
	case "win", "socks5":
		return &winMethod{}, nil
	case "windivert":
		return &winDivertMethod{}, nil
	}
	return nil, fmt.Errorf("firewall method %q not supported on Windows (socks5/win/windivert)", name)
}

// winDivertAvailable checks if the WinDivert DLL can be loaded.
func winDivertAvailable() bool {
	dll, err := syscall.LoadDLL("WinDivert.dll")
	if err != nil {
		return false
	}
	dll.Release()
	return true
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
	dnsPort   int
	dnsIface  string // network interface name used for DNS override
	prevDNS   string // previous DNS setting for restore
}

func (w *winMethod) Name() string { return "socks5" }

func (w *winMethod) SupportedFeatures() []Feature {
	return []Feature{FeatureDNS}
}

func (w *winMethod) Setup(subnets []SubnetRule, excludes []string, proxyPort, dnsPort int, dnsServers []string) error {
	w.proxyPort = proxyPort
	proxy := fmt.Sprintf("socks=127.0.0.1:%d", proxyPort)

	// WinHTTP proxy (affects command-line tools, WinHTTP-based apps).
	exec.Command("netsh", "winhttp", "set", "proxy", proxy).Run()

	// WinINET proxy (IE, Edge, Chrome, curl, Python requests, etc.).
	const regPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	exec.Command("reg", "add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f").Run()
	exec.Command("reg", "add", regPath, "/v", "ProxyServer", "/t", "REG_SZ", "/d", proxy, "/f").Run()

	// Set local DNS server so DNS queries are tunnelled.
	// The DNS proxy listens on 127.0.0.1:dnsPort and forwards via the mux.
	if dnsPort > 0 {
		w.dnsPort = dnsPort
		iface := detectActiveInterface()
		if iface != "" {
			w.dnsIface = iface
			// Save current DNS for restore.
			w.prevDNS = getCurrentDNS(iface)
			exec.Command("netsh", "interface", "ip", "set", "dns",
				iface, "static", fmt.Sprintf("127.0.0.1"), "primary").Run()
		}
	}

	return nil
}

func (w *winMethod) Restore() error {
	const regPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	exec.Command("netsh", "winhttp", "reset", "proxy").Run()
	exec.Command("reg", "add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f").Run()
	exec.Command("reg", "delete", regPath, "/v", "ProxyServer", "/f").Run()

	// Restore DNS settings.
	if w.dnsIface != "" {
		if w.prevDNS != "" {
			exec.Command("netsh", "interface", "ip", "set", "dns",
				w.dnsIface, "static", w.prevDNS, "primary").Run()
		} else {
			// Restore to DHCP.
			exec.Command("netsh", "interface", "ip", "set", "dns",
				w.dnsIface, "dhcp").Run()
		}
	}
	return nil
}

func listMethodFeatures() map[string][]Feature {
	m := map[string][]Feature{
		"windivert": (&winDivertMethod{}).SupportedFeatures(),
		"socks5":    (&winMethod{}).SupportedFeatures(),
	}
	return m
}

// detectActiveInterface returns the name of the active network interface.
func detectActiveInterface() string {
	out, err := exec.Command("netsh", "interface", "ip", "show", "config").Output()
	if err != nil {
		return ""
	}
	// Find the first interface with a default gateway.
	lines := strings.Split(string(out), "\n")
	var currentIface string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Configuration for interface") {
			// Extract interface name between quotes.
			start := strings.Index(line, "\"")
			end := strings.LastIndex(line, "\"")
			if start >= 0 && end > start {
				currentIface = line[start+1 : end]
			}
		}
		if strings.Contains(line, "Default Gateway") && !strings.HasSuffix(strings.TrimSpace(line), ":") {
			if currentIface != "" {
				return currentIface
			}
		}
	}
	return ""
}

// getCurrentDNS returns the current static DNS server for the interface, or "" for DHCP.
func getCurrentDNS(iface string) string {
	out, err := exec.Command("netsh", "interface", "ip", "show", "dns", iface).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "DHCP") {
			return "" // DHCP-configured
		}
		// Look for an IP address line.
		if ip := net.ParseIP(line); ip != nil {
			return line
		}
		// "Statically Configured DNS Servers: X.X.X.X"
		if idx := strings.LastIndex(line, ":"); idx >= 0 {
			candidate := strings.TrimSpace(line[idx+1:])
			if net.ParseIP(candidate) != nil {
				return candidate
			}
		}
	}
	return ""
}
