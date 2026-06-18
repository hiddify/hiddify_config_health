package telemetry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hiddify/hiddify_config_health/internal/proxyuri"
)

func TestScrubRemovesPII(t *testing.T) {
	p := proxyuri.Proxy{
		Protocol: "vless", Server: "185.112.32.52", Port: 443,
		UUID: "secret-uuid", Password: "pw", SNI: "microsoft.com",
		Host: "cdn.example.com", PublicKey: "PUBKEY", ShortID: "ab12",
		Path: "/secret-path", Tag: "my-real-node-name",
		Security: "reality", Transport: "ws", Flow: "xtls-rprx-vision",
		ALPN: []string{"h2"},
	}
	s := Scrub(p)

	// Structural fields preserved.
	if s.Protocol != "vless" || s.Transport != "ws" || s.Security != "reality" {
		t.Errorf("structure lost: %+v", s)
	}
	if !s.HasFlow || !s.HasReality || !s.HasALPN {
		t.Errorf("flags wrong: %+v", s)
	}

	// Serialise and assert NO identifying value leaks anywhere.
	b, _ := json.Marshal(s)
	js := string(b)
	for _, secret := range []string{
		"185.112.32.52", "443", "secret-uuid", "pw", "microsoft.com",
		"cdn.example.com", "PUBKEY", "ab12", "secret-path", "my-real-node-name",
	} {
		if strings.Contains(js, secret) {
			t.Errorf("PII leaked in scrubbed shape: %q in %s", secret, js)
		}
	}
}

func TestScrubShadowsocksKeepsCipherClass(t *testing.T) {
	p := proxyuri.Proxy{Protocol: "shadowsocks", Cipher: "aes-256-gcm", Password: "thekey", Transport: "tcp"}
	s := Scrub(p)
	if s.Cipher != "aes-256-gcm" {
		t.Errorf("cipher class should be kept: %q", s.Cipher)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "thekey") {
		t.Error("password leaked")
	}
}

func TestAnonIDStableAndRandom(t *testing.T) {
	id1 := AnonID()
	id2 := AnonID()
	if id1 != id2 {
		t.Error("anon id should be stable across calls")
	}
	if len(id1) < 16 {
		t.Errorf("anon id too short: %q", id1)
	}
}
