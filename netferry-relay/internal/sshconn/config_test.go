package sshconn

import (
	"testing"
)

// TestSplitUserHost covers the key behaviours of splitUserHost.
func TestSplitUserHost(t *testing.T) {
	cases := []struct {
		input    string
		wantUser string
		wantHost string
	}{
		// Standard "user@host" form.
		{"user@host", "user", "host"},
		// No "@" → empty user, full string is host.
		{"host", "", "host"},
		// User is empty but "@" is present.
		{"@host", "", "host"},
		// Host with port — splitUserHost does NOT strip port; host includes ":port".
		{"user@host:2222", "user", "host:2222"},
		// No port, no user.
		{"example.com", "", "example.com"},
		// Multiple "@" signs — split on the LAST one (strings.LastIndex).
		{"user@name@host.example.com", "user@name", "host.example.com"},
		// IPv6 address without user.
		{"::1", "", "::1"},
		// IPv6 with user.
		{"admin@::1", "admin", "::1"},
		// Empty string.
		{"", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			gotUser, gotHost := splitUserHost(tc.input)
			if gotUser != tc.wantUser || gotHost != tc.wantHost {
				t.Errorf("splitUserHost(%q) = (%q, %q), want (%q, %q)",
					tc.input, gotUser, gotHost, tc.wantUser, tc.wantHost)
			}
		})
	}
}
