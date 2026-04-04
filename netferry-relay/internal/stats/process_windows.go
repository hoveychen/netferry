//go:build windows

package stats

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"time"
)

// lookupProcesses resolves process names for the given set of local TCP
// source addresses (e.g. "127.0.0.1:56789"). Returns addr -> process name.
// Uses netstat + tasklist on Windows.
func lookupProcesses(addrs map[string]struct{}) map[string]string {
	if len(addrs) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Step 1: netstat -ano to get local addr -> PID mapping.
	// Output lines look like:
	//   TCP    127.0.0.1:56789        127.0.0.1:12300        ESTABLISHED     1234
	netstatOut, err := exec.CommandContext(ctx, "netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return nil
	}

	addrToPID := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(netstatOut)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "TCP") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		localAddr := fields[1]
		pid := fields[4]
		if _, ok := addrs[localAddr]; ok {
			addrToPID[localAddr] = pid
		}
	}

	if len(addrToPID) == 0 {
		return nil
	}

	// Step 2: tasklist to get PID -> process name mapping.
	// Output (CSV, no header):  "chrome.exe","1234","Console","1","123,456 K"
	taskOut, err := exec.CommandContext(ctx, "tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return nil
	}

	pidToName := make(map[string]string)
	scanner = bufio.NewScanner(strings.NewReader(string(taskOut)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ",", 3)
		if len(parts) < 2 {
			continue
		}
		name := strings.Trim(parts[0], "\"")
		pid := strings.Trim(parts[1], "\"")
		pidToName[pid] = strings.TrimSuffix(name, ".exe")
	}

	// Step 3: combine addr -> PID -> name.
	result := make(map[string]string)
	for addr, pid := range addrToPID {
		if name, ok := pidToName[pid]; ok {
			result[addr] = name
		}
	}
	return result
}
