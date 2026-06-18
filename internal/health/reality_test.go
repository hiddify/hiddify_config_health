package health

import "testing"

func TestMatchHost(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		{"example.com", "example.com", true},
		{"example.com", "other.com", false},
		{"*.example.com", "www.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "a.b.example.com", false},
		{"WWW.Example.COM", "www.example.com", true},
	}
	for _, c := range cases {
		if got := matchHost(c.pattern, c.host); got != c.want {
			t.Errorf("matchHost(%q,%q)=%v want %v", c.pattern, c.host, got, c.want)
		}
	}
}

func TestCertCoversHost(t *testing.T) {
	if !certCoversHost([]string{"*.cloudflare.com"}, "cloudflare.com", "www.cloudflare.com") {
		t.Error("wildcard SAN should cover www")
	}
	if certCoversHost([]string{"a.com"}, "a.com", "evil.com") {
		t.Error("should not cover unrelated host")
	}
}
