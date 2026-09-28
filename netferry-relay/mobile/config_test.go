package mobile

import "testing"

func TestParseConfigFectun(t *testing.T) {
	// Shape emitted by Profile.toConfigJson (Android) / toConfigJSON (iOS).
	cfg, err := parseConfig(`{"remote":"u@h","fectun":{"port":55700,"k":20,"m":15,"rateMbps":25.0}}`)
	if err != nil {
		t.Fatal(err)
	}
	f := cfg.Fectun
	if !f.Enabled() || f.Port != 55700 || f.K != 20 || f.M != 15 || f.RateMbps != 25 {
		t.Fatalf("fectun = %+v", f)
	}

	cfg, err = parseConfig(`{"remote":"u@h"}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Fectun.Enabled() {
		t.Fatal("fectun should be off when absent")
	}
}
