//go:build windows

package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed windivert/WinDivert.dll windivert/WinDivert64.sys
var windivertFS embed.FS

// extractWinDivert writes the embedded WinDivert DLL and driver to a temp
// directory and adds that directory to the DLL search path so that
// syscall.LoadDLL("WinDivert.dll") finds it automatically.
func extractWinDivert() (dir string, err error) {
	dir, err = os.MkdirTemp("", "netferry-probe-windivert-*")
	if err != nil {
		return "", fmt.Errorf("mkdtemp: %w", err)
	}

	for _, name := range []string{"WinDivert.dll", "WinDivert64.sys"} {
		data, err := windivertFS.ReadFile("windivert/" + name)
		if err != nil {
			return dir, fmt.Errorf("read embedded %s: %w", name, err)
		}
		dst := filepath.Join(dir, name)
		if err := os.WriteFile(dst, data, 0o755); err != nil {
			return dir, fmt.Errorf("write %s: %w", dst, err)
		}
	}

	// Prepend to PATH so LoadDLL finds it
	os.Setenv("PATH", dir+";"+os.Getenv("PATH"))
	return dir, nil
}
