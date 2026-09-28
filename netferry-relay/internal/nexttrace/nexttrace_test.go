package nexttrace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestParseTarget(t *testing.T) {
	cases := map[string]string{
		"user@host.example:22":   "host.example",
		"host.example":           "host.example",
		"host:2222":              "host",
		"user@1.2.3.4":           "1.2.3.4",
		"[::1]:22":               "::1",
		"user@[2001:db8::1]:22":  "2001:db8::1",
		"2001:db8::1":            "2001:db8::1",
		"  a@b@host.example:1  ": "host.example",
	}
	for in, want := range cases {
		if got, err := ParseTarget(in); err != nil || got != want {
			t.Errorf("ParseTarget(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", "user@"} {
		if _, err := ParseTarget(in); err == nil {
			t.Errorf("ParseTarget(%q) should fail", in)
		}
	}
}

func TestParseRawLine(t *testing.T) {
	h, ok := ParseRawLine("3|1.2.3.4|host.x|12.5|13335|Cloudflare|US|CA|San Jose|Cloudflare|37.0|-122.0")
	want := Hop{TTL: 3, IP: "1.2.3.4", Hostname: "host.x", RTTMs: 12.5, HasRTT: true, ASN: "13335", Owner: "Cloudflare", Country: "US", Province: "CA", City: "San Jose", ISP: "Cloudflare"}
	if !ok || !reflect.DeepEqual(h, want) {
		t.Fatalf("data row = %+v %v", h, ok)
	}
	h, ok = ParseRawLine("4|*||||||")
	if !ok || h.TTL != 4 || !h.Timeout || h.IP != "" || h.HasRTT {
		t.Fatalf("timeout row = %+v", h)
	}
	for _, l := range []string{"NextTrace v1.6.4 ...", "IP Geo Data Provider: LeoMoeAPI", "MapTrace URL: ...", "", "x|y"} {
		if _, ok := ParseRawLine(l); ok {
			t.Errorf("%q should be skipped", l)
		}
	}
}

func TestArgsClamp(t *testing.T) {
	if got := Args("h", Options{}); !reflect.DeepEqual(got, []string{"--raw", "--no-color", "-m", "30", "-q", "1", "h"}) {
		t.Fatalf("defaults = %v", got)
	}
	if got := Args("h", Options{MaxHops: 99, Queries: 9, Geo: " IPInfo "}); !reflect.DeepEqual(got, []string{"--raw", "--no-color", "-m", "64", "-q", "5", "-d", "IPInfo", "h"}) {
		t.Fatalf("clamped = %v", got)
	}
}

func TestAssetsPinned(t *testing.T) {
	for k, a := range assets {
		if len(a.sha256) != 64 || strings.ToLower(a.sha256) != a.sha256 {
			t.Errorf("%s: bad hash %q", k, a.sha256)
		}
	}
	if _, ok := platformAsset(); !ok && (runtime.GOOS == "linux" || runtime.GOOS == "darwin") {
		t.Fatal("no asset for the test platform")
	}
}

func fakeServer(t *testing.T, body string, contentLength int64) {
	old := httpGet
	httpGet = func(ctx context.Context, url string) (*http.Response, error) {
		if url != DownloadURL() {
			t.Errorf("url = %s", url)
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", ContentLength: contentLength, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	t.Cleanup(func() { httpGet = old })
}

func TestEnsureDownloadsAndVerifies(t *testing.T) {
	a, ok := platformAsset()
	if !ok {
		t.Skip("no asset for this platform")
	}
	t.Setenv("NETFERRY_DATA_DIR", t.TempDir())
	t.Setenv("NETFERRY_NEXTTRACE_BIN", "")
	body := "fake nexttrace binary"
	sum := sha256.Sum256([]byte(body))
	old := assets[runtime.GOOS+"/"+runtime.GOARCH]
	assets[runtime.GOOS+"/"+runtime.GOARCH] = asset{a.name, hex.EncodeToString(sum[:])}
	t.Cleanup(func() { assets[runtime.GOOS+"/"+runtime.GOARCH] = old })

	fakeServer(t, body, int64(len(body)))
	var last int64
	p, err := Ensure(context.Background(), func(n, total int64) { last = n })
	if err != nil {
		t.Fatal(err)
	}
	want, _ := InstallPath()
	if p != want || last != int64(len(body)) {
		t.Fatalf("path %s (want %s), progress %d", p, want, last)
	}
	st, err := os.Stat(p)
	if err != nil || (runtime.GOOS != "windows" && st.Mode().Perm()&0o100 == 0) {
		t.Fatalf("installed file: %v %v", st, err)
	}
	if _, err := os.Stat(strings.TrimSuffix(p, filepath.Ext(p)) + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part left behind")
	}
	// Found without the network the second time.
	httpGet = nil
	if p2, err := Ensure(context.Background(), nil); err != nil || p2 != p {
		t.Fatal(p2, err)
	}
}

func TestEnsureRejectsTamperedAndTruncated(t *testing.T) {
	if _, ok := platformAsset(); !ok {
		t.Skip("no asset for this platform")
	}
	t.Setenv("NETFERRY_DATA_DIR", t.TempDir())
	t.Setenv("NETFERRY_NEXTTRACE_BIN", "")
	fakeServer(t, "evil", 4)
	if _, err := Ensure(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("tampered: %v", err)
	}
	fakeServer(t, "short", 100)
	if _, err := Ensure(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("truncated: %v", err)
	}
	if Find() != "" {
		t.Fatal("a failed download must not install anything")
	}
}

func TestFindPrefersEnv(t *testing.T) {
	t.Setenv("NETFERRY_DATA_DIR", t.TempDir())
	bin := filepath.Join(t.TempDir(), "nt")
	os.WriteFile(bin, nil, 0o755)
	t.Setenv("NETFERRY_NEXTTRACE_BIN", bin)
	if Find() != bin {
		t.Fatal("env override ignored")
	}
	t.Setenv("NETFERRY_NEXTTRACE_BIN", bin+".missing")
	if Find() != "" {
		t.Fatal("a missing override must not be returned")
	}
}

func TestRunStreamsHops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script")
	}
	exe := filepath.Join(t.TempDir(), "nexttrace")
	script := "#!/bin/sh\necho 'NextTrace v1.6.4'\necho \"1|10.0.0.1||0.5|||||| $*\"\necho '2|*||||||'\nexit 3\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var hops []Hop
	code, err := Run(context.Background(), exe, "example.com", Options{MaxHops: 5}, func(h Hop) { hops = append(hops, h) })
	if err != nil || code != 3 {
		t.Fatalf("code %d err %v", code, err)
	}
	if len(hops) != 2 || hops[0].IP != "10.0.0.1" || !hops[1].Timeout {
		t.Fatalf("hops = %+v", hops)
	}
	if !strings.Contains(hops[0].ISP, "-m 5 -q 1 example.com") {
		t.Fatalf("args not passed: %q", hops[0].ISP)
	}
}
