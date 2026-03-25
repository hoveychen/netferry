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

func extractWinDivert() (dir string, err error) {
	dir, err = os.MkdirTemp("", "netferry-windivert-*")
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
	os.Setenv("PATH", dir+";"+os.Getenv("PATH"))
	return dir, nil
}
