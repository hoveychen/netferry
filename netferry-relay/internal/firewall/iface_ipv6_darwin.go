//go:build darwin

package firewall

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// darwinIPv6State captures one service's original IPv6 ConfigMethod so we can
// restore it. Keyed by the human-readable service name used by networksetup
// (e.g. "Wi-Fi", "Thunderbolt Bridge"). ConfigMethod matches scutil's values:
// "Automatic", "LinkLocal", "Manual", "Off", or more exotic ones like "6to4".
type darwinIPv6State struct {
	Services map[string]string `json:"services"`
}

func disableSystemIPv6() error {
	// Enumerate services and their current ConfigMethod via scutil.
	services, err := listDarwinIPv6Services()
	if err != nil {
		return fmt.Errorf("enumerate network services: %w", err)
	}
	if len(services) == 0 {
		return nil
	}

	state := darwinIPv6State{Services: services}
	if err := writeIfaceIPv6State(state); err != nil {
		return fmt.Errorf("persist state: %w", err)
	}

	for name, method := range services {
		if method == "Off" {
			// Already off — skip the call but keep the entry so Restore
			// leaves it alone (set to Off again, which is a no-op).
			continue
		}
		if out, err := runCmd("networksetup", "-setv6off", name); err != nil {
			log.Printf("iface_ipv6: setv6off %q failed: %v\n%s", name, err, out)
			// Don't abort; try the rest of the services.
		}
	}
	log.Printf("iface_ipv6: disabled IPv6 on %d service(s)", len(services))
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
	var state darwinIPv6State
	if err := json.Unmarshal(raw, &state); err != nil {
		// Corrupt state — remove it and move on. Better than wedging forever.
		os.Remove(ifaceIPv6StateFile())
		return fmt.Errorf("parse state: %w", err)
	}

	for name, method := range state.Services {
		var args []string
		switch method {
		case "Automatic":
			args = []string{"-setv6automatic", name}
		case "LinkLocal":
			args = []string{"-setv6LinkLocal", name}
		case "Off":
			args = []string{"-setv6off", name}
		case "Manual":
			// Manual carries per-service address/prefix/router which we did
			// not snapshot (complex to restore safely). Fall back to
			// Automatic and warn; the user can re-pin if they need it.
			log.Printf("iface_ipv6: service %q was Manual; restoring to Automatic (original manual address not preserved)", name)
			args = []string{"-setv6automatic", name}
		default:
			// "6to4" or other exotic modes we don't know how to set via
			// networksetup. Restore to Automatic as the safest default.
			log.Printf("iface_ipv6: service %q had unknown ConfigMethod %q; restoring to Automatic", name, method)
			args = []string{"-setv6automatic", name}
		}
		if out, err := runCmd("networksetup", args...); err != nil {
			log.Printf("iface_ipv6: restore %q (%s) failed: %v\n%s", name, method, err, out)
		}
	}

	os.Remove(ifaceIPv6StateFile())
	log.Printf("iface_ipv6: restored IPv6 on %d service(s)", len(state.Services))
	return nil
}

// listDarwinIPv6Services returns a map of service name → ConfigMethod for every
// network service that has an IPv6 Setup entry.
func listDarwinIPv6Services() (map[string]string, error) {
	uuids, err := scutilListServiceUUIDs()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(uuids))
	for _, uuid := range uuids {
		method, err := scutilGetConfigMethod(uuid)
		if err != nil {
			log.Printf("iface_ipv6: read ConfigMethod for %s: %v", uuid, err)
			continue
		}
		name, err := scutilGetServiceName(uuid)
		if err != nil || name == "" {
			log.Printf("iface_ipv6: read service name for %s: %v", uuid, err)
			continue
		}
		out[name] = method
	}
	return out, nil
}

var scutilSubKeyRE = regexp.MustCompile(`Setup:/Network/Service/([^/]+)/IPv6`)

func scutilListServiceUUIDs() ([]string, error) {
	out, err := runScutil("list Setup:/Network/Service/[^/]+/IPv6\n")
	if err != nil {
		return nil, err
	}
	var uuids []string
	for _, m := range scutilSubKeyRE.FindAllStringSubmatch(out, -1) {
		uuids = append(uuids, m[1])
	}
	return uuids, nil
}

var scutilConfigMethodRE = regexp.MustCompile(`(?m)^\s*ConfigMethod\s*:\s*(\S+)`)

func scutilGetConfigMethod(uuid string) (string, error) {
	out, err := runScutil(fmt.Sprintf("show Setup:/Network/Service/%s/IPv6\n", uuid))
	if err != nil {
		return "", err
	}
	m := scutilConfigMethodRE.FindStringSubmatch(out)
	if len(m) < 2 {
		// No ConfigMethod key usually means "not configured" — treat as Off.
		return "Off", nil
	}
	return m[1], nil
}

var scutilUserDefinedNameRE = regexp.MustCompile(`(?m)^\s*UserDefinedName\s*:\s*(.+?)\s*$`)

func scutilGetServiceName(uuid string) (string, error) {
	out, err := runScutil(fmt.Sprintf("show Setup:/Network/Service/%s\n", uuid))
	if err != nil {
		return "", err
	}
	m := scutilUserDefinedNameRE.FindStringSubmatch(out)
	if len(m) < 2 {
		return "", nil
	}
	return strings.TrimSpace(m[1]), nil
}

func runScutil(cmdText string) (string, error) {
	cmd := exec.Command("scutil")
	cmd.Stdin = strings.NewReader(cmdText)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

func writeIfaceIPv6State(state darwinIPv6State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(ifaceIPv6StateFile(), data, 0o600)
}
