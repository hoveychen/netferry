//go:build windows

package firewall

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// windowsIPv6State records which adapters had the ms_tcpip6 binding enabled
// before we disabled it, so Restore only re-enables those (avoids turning on
// IPv6 for adapters the user had previously disabled manually).
type windowsIPv6State struct {
	Adapters []string `json:"adapters"`
}

func disableSystemIPv6() error {
	names, err := listWindowsIPv6EnabledAdapters()
	if err != nil {
		return fmt.Errorf("enumerate adapters: %w", err)
	}
	if len(names) == 0 {
		return nil
	}

	state := windowsIPv6State{Adapters: names}
	if err := writeWindowsIPv6State(state); err != nil {
		return fmt.Errorf("persist state: %w", err)
	}

	for _, n := range names {
		// Disable-NetAdapterBinding is idempotent and returns no error if
		// already disabled; it requires admin, which netferry-relay already
		// has on Windows.
		ps := fmt.Sprintf(`Disable-NetAdapterBinding -Name %q -ComponentID 'ms_tcpip6' -ErrorAction Stop`, n)
		if out, err := runPowerShell(ps); err != nil {
			log.Printf("iface_ipv6: disable binding on %q: %v\n%s", n, err, out)
		}
	}
	log.Printf("iface_ipv6: disabled IPv6 binding on %d adapter(s)", len(names))
	return nil
}

func restoreSystemIPv6() error {
	raw, err := os.ReadFile(ifaceIPv6StateFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read state: %w", err)
	}
	var state windowsIPv6State
	if err := json.Unmarshal(raw, &state); err != nil {
		os.Remove(ifaceIPv6StateFile())
		return fmt.Errorf("parse state: %w", err)
	}

	for _, n := range state.Adapters {
		ps := fmt.Sprintf(`Enable-NetAdapterBinding -Name %q -ComponentID 'ms_tcpip6' -ErrorAction Stop`, n)
		if out, err := runPowerShell(ps); err != nil {
			log.Printf("iface_ipv6: enable binding on %q: %v\n%s", n, err, out)
		}
	}
	os.Remove(ifaceIPv6StateFile())
	log.Printf("iface_ipv6: restored IPv6 binding on %d adapter(s)", len(state.Adapters))
	return nil
}

// listWindowsIPv6EnabledAdapters returns names of adapters whose ms_tcpip6
// binding is currently enabled. We only touch those so Restore doesn't turn
// on IPv6 for adapters the user had already disabled.
func listWindowsIPv6EnabledAdapters() ([]string, error) {
	ps := `Get-NetAdapterBinding -ComponentID 'ms_tcpip6' | Where-Object { $_.Enabled } | Select-Object -ExpandProperty Name`
	out, err := runPowerShell(ps)
	if err != nil {
		return nil, fmt.Errorf("query adapters: %w\n%s", err, out)
	}
	var names []string
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

func runPowerShell(script string) (string, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeWindowsIPv6State(state windowsIPv6State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(ifaceIPv6StateFile(), data, 0o600)
}
