// Package nexttrace locates, downloads (pinned + SHA-256 verified) and runs
// the NextTrace traceroute tool. It is a Go port of the desktop's
// netferry-desktop/src-tauri/src/traceroute.rs and shares its install
// location, so a binary fetched by either app is reused by the other.
package nexttrace

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/store"
)

// Version is the pinned upstream release; bump together with the hashes.
const Version = "v1.6.4"

// installDir is the subdirectory of the app data dir.
const installDir = "nexttrace"

// GeoSources are the -d data sources the desktop offers.
var GeoSources = []string{"LeoMoeAPI", "IPInfo", "IP-API", "IP.SB", "Ip2region"}

type asset struct{ name, sha256 string }

var assets = map[string]asset{
	"darwin/arm64":  {"nexttrace-tiny_darwin_arm64", "e666b60fe8d2b0bf12555e4f463f2bba596fee9d03dadee35e3a2edf7e6c86fd"},
	"darwin/amd64":  {"nexttrace-tiny_darwin_amd64", "3ed6893cd438d0dfb8fe2d4c0060ad7fa9becc7968a8016a40da23599174e59f"},
	"windows/amd64": {"nexttrace-tiny_windows_amd64.exe", "808d7c5eab3569e7009d4698872bc1aecba34804019ce8df738abfd2e13d7e4d"},
	"windows/arm64": {"nexttrace-tiny_windows_arm64.exe", "a31915cdaf387be05158ce680466104b1b76c13fdf0ceedd1fa9c65c7de05998"},
	"linux/amd64":   {"nexttrace-tiny_linux_amd64", "03d514c7de478c4bb1ea8a43e771d6e8eb0a4f8a7347a36cf2b48c691f067e03"},
	"linux/arm64":   {"nexttrace-tiny_linux_arm64", "9670182456da65dd6a05a40cdca37f17ccf81581ce78d1132d48072c9b6d68a9"},
}

func platformAsset() (asset, bool) {
	a, ok := assets[runtime.GOOS+"/"+runtime.GOARCH]
	return a, ok
}

// DownloadURL is the release asset URL for this platform ("" if none).
func DownloadURL() string {
	a, ok := platformAsset()
	if !ok {
		return ""
	}
	return "https://github.com/nxtrace/NTrace-core/releases/download/" + Version + "/" + a.name
}

// InstallPath is where downloads land: <app data>/nexttrace/nexttrace[.exe].
func InstallPath() (string, error) {
	dir, err := store.DataDir()
	if err != nil {
		return "", err
	}
	name := "nexttrace"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, installDir, name), nil
}

// Find locates an existing binary: $NETFERRY_NEXTTRACE_BIN, then the install
// path. Returns "" when neither exists.
func Find() string {
	if p := strings.TrimSpace(os.Getenv("NETFERRY_NEXTTRACE_BIN")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := InstallPath(); err == nil {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// httpGet is swappable in tests.
var httpGet = func(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "NetFerry-TUI")
	return http.DefaultClient.Do(req)
}

// Ensure returns an installed binary, downloading and verifying it first
// when missing. progress (may be nil) receives bytes so far and the total
// (0 if unknown).
func Ensure(ctx context.Context, progress func(n, total int64)) (string, error) {
	if p := Find(); p != "" {
		return p, nil
	}
	a, ok := platformAsset()
	if !ok {
		return "", fmt.Errorf("no prebuilt NextTrace binary for %s/%s; install it manually and set NETFERRY_NEXTTRACE_BIN", runtime.GOOS, runtime.GOARCH)
	}
	target, err := InstallPath()
	if err != nil {
		return "", err
	}
	if err := download(ctx, DownloadURL(), target, a.sha256, progress); err != nil {
		_ = os.Remove(target)
		return "", err
	}
	return target, nil
}

func download(ctx context.Context, url, target, wantSHA string, progress func(n, total int64)) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}
	resp, err := httpGet(ctx, url)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GitHub returned status %s", resp.Status)
	}
	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	// A sibling .part file, renamed only once verified, so a partial or
	// tampered download is never picked up as a usable binary.
	part := strings.TrimSuffix(target, filepath.Ext(target)) + ".part"
	f, err := os.Create(part)
	if err != nil {
		return fmt.Errorf("open download file: %w", err)
	}
	h := sha256.New()
	var n int64
	buf := make([]byte, 64<<10)
	for {
		m, rerr := resp.Body.Read(buf)
		if m > 0 {
			if _, werr := f.Write(buf[:m]); werr != nil {
				f.Close()
				os.Remove(part)
				return fmt.Errorf("disk write: %w", werr)
			}
			h.Write(buf[:m])
			n += int64(m)
			if progress != nil {
				progress(n, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(part)
			return fmt.Errorf("network read: %w", rerr)
		}
	}
	f.Close()
	if total > 0 && n != total {
		os.Remove(part)
		return fmt.Errorf("truncated download: got %d of %d bytes", n, total)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantSHA) {
		os.Remove(part)
		return fmt.Errorf("SHA-256 mismatch: expected %s, got %s", wantSHA, got)
	}
	if err := os.Chmod(part, 0o755); err != nil {
		os.Remove(part)
		return err
	}
	if err := os.Rename(part, target); err != nil {
		os.Remove(part)
		return fmt.Errorf("rename to final path: %w", err)
	}
	return nil
}

// ParseTarget extracts the host from a profile.remote-style string:
// host, host:port, user@host[:port], [ipv6]:port, raw IPv6.
func ParseTarget(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("target host is empty")
	}
	host := s
	if i := strings.LastIndex(s, "@"); i >= 0 {
		host = s[i+1:]
	}
	switch {
	case strings.HasPrefix(host, "["):
		if end := strings.Index(host, "]"); end >= 0 {
			host = host[1:end]
		}
	case strings.Count(host, ":") == 1:
		host = host[:strings.Index(host, ":")]
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", errors.New("target host is empty after parsing")
	}
	return host, nil
}

// Hop is one parsed `--raw` output row.
type Hop struct {
	TTL      int
	IP       string
	Hostname string
	RTTMs    float64
	HasRTT   bool
	ASN      string
	Owner    string
	Country  string
	Province string
	City     string
	ISP      string
	Timeout  bool // "*": no reply for this TTL
}

// ParseRawLine parses `ttl|ip|host|rtt|asn|owner|country|province|city|isp|…`;
// headers and banners return ok=false.
func ParseRawLine(line string) (Hop, bool) {
	line = strings.TrimSpace(strings.TrimRight(line, "\r"))
	if line == "" {
		return Hop{}, false
	}
	parts := strings.Split(line, "|")
	if len(parts) < 2 {
		return Hop{}, false
	}
	ttl, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || ttl < 0 || ttl > 255 {
		return Hop{}, false
	}
	get := func(i int) string {
		if i < len(parts) {
			return strings.TrimSpace(parts[i])
		}
		return ""
	}
	h := Hop{TTL: ttl, IP: get(1), Hostname: get(2), ASN: get(4), Owner: get(5),
		Country: get(6), Province: get(7), City: get(8), ISP: get(9)}
	if h.IP == "*" {
		h.IP, h.Timeout = "", true
	}
	if v, err := strconv.ParseFloat(get(3), 64); err == nil {
		h.RTTMs, h.HasRTT = v, true
	}
	return h, true
}

// Options are the trace knobs the desktop exposes.
type Options struct {
	MaxHops int    // clamped to 1..64, default 30
	Queries int    // clamped to 1..5, default 1
	Geo     string // -d data source; empty = nexttrace default
}

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		v = def
	}
	return max(lo, min(hi, v))
}

// Args builds the nexttrace command line.
func Args(host string, o Options) []string {
	args := []string{"--raw", "--no-color",
		"-m", strconv.Itoa(clamp(o.MaxHops, 1, 64, 30)),
		"-q", strconv.Itoa(clamp(o.Queries, 1, 5, 1))}
	if g := strings.TrimSpace(o.Geo); g != "" {
		args = append(args, "-d", g)
	}
	return append(args, host)
}

// Run traces host with exe, calling onHop for every parsed row, until the
// process exits or ctx is cancelled. Returns the exit code.
func Run(ctx context.Context, exe, host string, o Options, onHop func(Hop)) (int, error) {
	cmd := exec.CommandContext(ctx, exe, Args(host, o)...)
	cmd.Stdin = nil
	cmd.Stderr = io.Discard
	hideWindow(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("failed to start nexttrace (%s): %w", exe, err)
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if h, ok := ParseRawLine(sc.Text()); ok {
			onHop(h)
		}
	}
	err = cmd.Wait()
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode(), nil
	}
	return -1, err
}
