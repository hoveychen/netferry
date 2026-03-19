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
