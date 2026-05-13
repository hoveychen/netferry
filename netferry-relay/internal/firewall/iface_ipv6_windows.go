//go:build windows

package firewall

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"
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
		if !isValidAdapterName(n) {
			// State from an older buggy run that saved names in the console
			// codepage; JSON marshalling replaced the non-UTF-8 bytes with
			// U+FFFD, so the original name is unrecoverable. Skip rather
			// than burn ~3-5s per adapter on a PowerShell call that is
			// guaranteed to fail with exit status 1.
			log.Printf("iface_ipv6: dropping corrupt restore entry %q (likely from a pre-fix run)", n)
			continue
		}
		ps := fmt.Sprintf(`Enable-NetAdapterBinding -Name %q -ComponentID 'ms_tcpip6' -ErrorAction Stop`, n)
		if out, err := runPowerShell(ps); err != nil {
			log.Printf("iface_ipv6: enable binding on %q: %v\n%s", n, err, out)
		}
	}
	os.Remove(ifaceIPv6StateFile())
	log.Printf("iface_ipv6: restored IPv6 binding on %d adapter(s)", len(state.Adapters))
	return nil
}

// isValidAdapterName rejects names that round-tripped through a non-UTF-8
// codepage. encoding/json silently substitutes invalid UTF-8 bytes with
// U+FFFD, so a saved name like "以太网" written by a pre-fix run comes back
// as "��太��" — useless for matching the live adapter.
func isValidAdapterName(name string) bool {
	if !utf8.ValidString(name) {
		return false
	}
	return !strings.ContainsRune(name, utf8.RuneError)
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
	// Force PowerShell to emit UTF-8 on stdout/stderr. Without this, on a
	// non-English Windows (e.g. zh-CN where the console codepage is 936),
	// adapter names like "以太网" come back as GBK bytes — not valid UTF-8 —
	// and any name we feed back into PowerShell ends up mojibaked and fails
	// to match a real adapter. The shim is pure ASCII so it is safe to
	// concatenate regardless of the current console codepage.
	const utf8Shim = "$OutputEncoding = [System.Text.Encoding]::UTF8; [Console]::OutputEncoding = [System.Text.Encoding]::UTF8; "
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", utf8Shim+script)
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
