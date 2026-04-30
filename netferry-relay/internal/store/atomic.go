package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// writeJSONAtomic writes v as pretty-printed JSON to path via a temp file +
// rename, matching the desktop's serde_json::to_string_pretty layout (two-
// space indent, keys preserved in source order via json.Marshal).
func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	// json.Encoder.Encode appends a trailing newline; serde does not. Trim it
	// to avoid spurious diffs when the desktop and TUI both write the same file.
	out := bytes.TrimRight(buf.Bytes(), "\n")

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}
