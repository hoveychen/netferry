//go:build darwin

package firewall

import (
	"testing"
)

// TestListDarwinIPv6Services_ReadOnly exercises the scutil parsing path used by
// DisableSystemIPv6 without actually modifying any network setting. Verifies
// that enumeration and ConfigMethod/UserDefinedName parsing work against the
// live system. Must pass before it is safe to touch the write path.
func TestListDarwinIPv6Services_ReadOnly(t *testing.T) {
	services, err := listDarwinIPv6Services()
	if err != nil {
		t.Fatalf("listDarwinIPv6Services: %v", err)
	}
	if len(services) == 0 {
		t.Fatal("expected at least one network service with an IPv6 Setup entry")
	}
	for name, method := range services {
		if name == "" {
			t.Errorf("got empty service name (method=%q)", method)
		}
		switch method {
		case "Automatic", "LinkLocal", "Manual", "Off", "6to4":
			// Known values; Restore handles each.
		default:
			// Unknown is not a failure — our Restore logs a warning and falls
			// back to Automatic — but surface it so we notice new enum values.
			t.Logf("service %q has ConfigMethod %q (unknown; will fall back to Automatic on restore)", name, method)
		}
		t.Logf("service=%q ConfigMethod=%q", name, method)
	}
}
