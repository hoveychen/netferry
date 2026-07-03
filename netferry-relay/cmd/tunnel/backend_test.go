package main

import "testing"

func TestSSHHostPart(t *testing.T) {
	cases := []struct{ in, want string }{
		{"tom@13.229.51.179", "13.229.51.179"},
		{"tom@13.229.51.179:2222", "13.229.51.179"},
		{"example.com", "example.com"},
		{"tom@example.com:22", "example.com"},
		{"2001:db8::1", "2001:db8::1"}, // bare IPv6 literal: colons are not a port
	}
	for _, c := range cases {
		if got := sshHostPart(c.in); got != c.want {
			t.Errorf("sshHostPart(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// When every SSH endpoint is an IP literal, re-dialing needs no DNS at all —
// the reconnect exit path must keep the DNS redirect in place so applications
// cannot pick up region-local geo-DNS answers during the reconnect window.
func TestReconnectNeedsDNS_AllIPLiterals(t *testing.T) {
	if reconnectNeedsDNS("13.229.51.179", "", nil) {
		t.Fatalf("IP-literal endpoint reported as needing DNS")
	}
	if reconnectNeedsDNS("13.229.51.179", "", []string{"root@10.8.0.1:22"}) {
		t.Fatalf("IP-literal endpoint + IP jump host reported as needing DNS")
	}
}

func TestReconnectNeedsDNS_HostnameCases(t *testing.T) {
	if !reconnectNeedsDNS("ssh.example.com", "", nil) {
		t.Fatalf("hostname endpoint not reported as needing DNS")
	}
	if !reconnectNeedsDNS("13.229.51.179", "jump.example.com", nil) {
		t.Fatalf("hostname ProxyJump not reported as needing DNS")
	}
	if !reconnectNeedsDNS("13.229.51.179", "", []string{"root@jump.example.com"}) {
		t.Fatalf("hostname jump host not reported as needing DNS")
	}
	// Mixed: one IP jump, one hostname jump → still needs DNS.
	if !reconnectNeedsDNS("13.229.51.179", "", []string{"root@10.8.0.1", "root@jump.example.com:2222"}) {
		t.Fatalf("mixed jump hosts not reported as needing DNS")
	}
	// IP-literal ProxyJump needs no DNS.
	if reconnectNeedsDNS("13.229.51.179", "10.8.0.1", nil) {
		t.Fatalf("IP-literal ProxyJump reported as needing DNS")
	}
}
