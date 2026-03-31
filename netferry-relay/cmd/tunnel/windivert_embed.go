//go:build windows

package main

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

//go:embed windivert/WinDivert.dll windivert/WinDivert64.sys
var windivertFS embed.FS

// extractWinDivert extracts the embedded WinDivert DLL and kernel driver to a
// fixed, content-addressed directory under %LOCALAPPDATA%\NetFerry\windivert\.
//
// Unlike a random temp directory, this approach:
//   - Reuses existing files across restarts (no re-extraction needed).
//   - Tolerates a locked .sys file from a previous crash — the kernel driver
//     is already loaded, and WinDivertOpen will simply open a new handle.
//   - Avoids installer conflicts — the files are NOT in the app install
//     directory, so MSI/NSIS upgrades never try to overwrite a locked driver.
//   - Uses a content hash in the directory name so version upgrades extract
//     to a new path without touching the old (possibly locked) files.
func extractWinDivert() (dir string, err error) {
	// Compute a content hash of the embedded files to create a
	// version-specific directory name.
	h := sha256.New()
	for _, name := range []string{"WinDivert.dll", "WinDivert64.sys"} {
		data, err := windivertFS.ReadFile("windivert/" + name)
		if err != nil {
			return "", fmt.Errorf("read embedded %s: %w", name, err)
		}
		h.Write(data)
	}
	hash := fmt.Sprintf("%x", h.Sum(nil))[:12]

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		localAppData = os.TempDir()
	}
	dir = filepath.Join(localAppData, "NetFerry", "windivert", hash)

	// Check if files already exist and are the right size. If so, skip
	// extraction — even if the .sys is locked by the kernel, that's fine.
	allPresent := true
	for _, name := range []string{"WinDivert.dll", "WinDivert64.sys"} {
		embedded, _ := windivertFS.ReadFile("windivert/" + name)
		dst := filepath.Join(dir, name)
		info, err := os.Stat(dst)
		if err != nil || info.Size() != int64(len(embedded)) {
			allPresent = false
			break
		}
	}

	if !allPresent {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", dir, err)
		}
		for _, name := range []string{"WinDivert.dll", "WinDivert64.sys"} {
			data, _ := windivertFS.ReadFile("windivert/" + name)
			dst := filepath.Join(dir, name)
			if err := os.WriteFile(dst, data, 0o755); err != nil {
				// Write may fail if the file is locked (driver loaded from
				// a previous crash). That's OK — the existing file IS the
				// right version (same content hash), so we can use it as-is.
				log.Printf("windivert: write %s: %v (using existing file)", dst, err)
			}
		}
	}

	os.Setenv("PATH", dir+";"+os.Getenv("PATH"))
	log.Printf("windivert: using %s", dir)
	return dir, nil
}
