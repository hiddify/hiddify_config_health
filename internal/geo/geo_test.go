package geo

import "testing"

func TestLookupKnownRange(t *testing.T) {
	info := Lookup("104.16.5.5") // Cloudflare
	if info.ASN != "AS13335" || info.Country != "US" {
		t.Errorf("cloudflare lookup wrong: %+v", info)
	}
	if info.Flagged {
		t.Error("cloudflare should not be flagged")
	}
}

func TestLookupFlagged(t *testing.T) {
	info := Lookup("5.61.18.1")
	if !info.Flagged || info.Country != "IR" {
		t.Errorf("expected flagged IR: %+v", info)
	}
}

func TestLookupServerExample(t *testing.T) {
	info := Lookup("185.112.32.52") // gorbani-dd range
	if info.Country != "NL" {
		t.Errorf("expected NL: %+v", info)
	}
}

func TestLookupUnknownIP(t *testing.T) {
	info := Lookup("198.51.100.7") // TEST-NET, not in table
	if info.ASN != "" || info.Flagged {
		t.Errorf("unknown IP should be empty/unflagged: %+v", info)
	}
}

func TestLookupHostnameNoNetwork(t *testing.T) {
	info := Lookup("example.com") // not an IP literal — must not resolve
	if info.ASN != "" {
		t.Errorf("hostname should not be resolved: %+v", info)
	}
}
