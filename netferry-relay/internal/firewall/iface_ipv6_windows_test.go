//go:build windows

package firewall

import "testing"

func TestIsValidAdapterName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"ascii", "Ethernet", true},
		{"chinese", "以太网", true},
		{"japanese", "イーサネット", true},
		{"empty", "", true},
		// JSON marshalling on a string that originally held GBK bytes for
		// "以太网" produces U+FFFD replacement characters — this is the exact
		// payload we now want to skip rather than spawn PowerShell for.
		{"replacement_char", "��太��", false},
		{"trailing_replacement", "Ethernet�", false},
		// Raw non-UTF-8 bytes (defensive — shouldn't happen post-fix, but
		// guard the path).
		{"invalid_utf8", "\xff\xfe\xfd", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isValidAdapterName(c.in); got != c.want {
				t.Fatalf("isValidAdapterName(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
