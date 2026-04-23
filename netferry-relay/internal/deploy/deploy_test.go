package deploy

import (
	"testing"
)

// ---- parseUname tests -------------------------------------------------------

func TestParseUname(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Linux x86_64", "linux-amd64"},
		{"Linux amd64", "linux-amd64"},
		{"Darwin arm64", "darwin-arm64"},
		{"Linux aarch64", "linux-arm64"},
		{"Linux mipsel", "linux-mipsle"},
		{"Linux mipsle", "linux-mipsle"},
		{"Linux mips", "linux-mips"},
		// Default fallback: less than 2 fields → "linux-amd64".
		{"Linux", "linux-amd64"},
		{"", "linux-amd64"},
		// Unknown arch is lowercased and passed through.
		{"Darwin powerpc", "darwin-powerpc"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := parseUname(tc.input)
			if got != tc.want {
				t.Errorf("parseUname(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ---- shouldReuseRemote tests ------------------------------------------------

func TestShouldReuseRemote(t *testing.T) {
	cases := []struct {
		name       string
		remoteSize int64
		localSize  int64
		want       bool
	}{
		{"missing remote", -1, 100, false},
		{"size match", 100, 100, true},
		{"size mismatch smaller", 50, 100, false},
		{"size mismatch larger", 150, 100, false},
		{"zero-byte remote", 0, 100, false},
		{"both zero", 0, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldReuseRemote(tc.remoteSize, tc.localSize)
			if got != tc.want {
				t.Errorf("shouldReuseRemote(remote=%d, local=%d) = %v, want %v",
					tc.remoteSize, tc.localSize, got, tc.want)
			}
		})
	}
}

// ---- shellQuote tests -------------------------------------------------------

func TestShellQuote(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// Plain string: wrap in single quotes.
		{"hello", "'hello'"},
		// String containing a single quote: escape it.
		{"it's", "'it'\"'\"'s'"},
		// Empty string.
		{"", "''"},
		// Path with spaces.
		{"/home/user/my path/bin", "'/home/user/my path/bin'"},
		// Multiple single quotes.
		{"a'b'c", "'a'\"'\"'b'\"'\"'c'"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := shellQuote(tc.input)
			if got != tc.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
