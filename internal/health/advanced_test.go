package health

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestShannonNorm(t *testing.T) {
	zeros := make([]byte, 4096) // all same byte → entropy 0
	if h := shannonNorm(zeros); h > 0.01 {
		t.Errorf("all-zero entropy = %f, want ~0", h)
	}
	rnd := make([]byte, 4096)
	_, _ = rand.Read(rnd)
	if h := shannonNorm(rnd); h < 0.95 {
		t.Errorf("random entropy = %f, want ~1", h)
	}
}

func TestSizeDistEntropy(t *testing.T) {
	// All identical sizes → 0 entropy (regular framing).
	same := make([]int, 100)
	for i := range same {
		same[i] = 1400
	}
	if h := sizeDistEntropy(same); h > 0.01 {
		t.Errorf("uniform-size entropy = %f, want ~0", h)
	}
	// Varied sizes across many buckets → high entropy.
	varied := make([]int, 100)
	for i := range varied {
		varied[i] = i * 600
	}
	if h := sizeDistEntropy(varied); h < 0.5 {
		t.Errorf("varied-size entropy = %f, want high", h)
	}
}

func TestBuildClientHelloWellFormed(t *testing.T) {
	rec := buildClientHello("example.com")
	if len(rec) < 5 || rec[0] != 0x16 || rec[1] != 0x03 {
		t.Fatalf("not a TLS handshake record: % x", rec[:5])
	}
	if !bytes.Contains(rec, []byte("example.com")) {
		t.Error("SNI not embedded in ClientHello")
	}
	if rec[5] != 0x01 { // handshake type ClientHello
		t.Errorf("handshake type = %d, want 1 (ClientHello)", rec[5])
	}
}

func TestJA3FromRawHello(t *testing.T) {
	// Build a Chrome hello via uTLS and ensure JA3/JA4 are computed.
	raw, err := buildChromeHello("127.0.0.1:1", "example.com", 0) // unreachable → pipe fallback
	if err != nil {
		t.Fatalf("buildChromeHello: %v", err)
	}
	res, err := ja3FromRawHello(raw)
	if err != nil {
		t.Fatalf("ja3FromRawHello: %v", err)
	}
	if len(res.JA3Sum) != 32 {
		t.Errorf("JA3 md5 len = %d, want 32 (%q)", len(res.JA3Sum), res.JA3Sum)
	}
	if res.JA4 == "" {
		t.Error("empty JA4")
	}
	if res.Match != "chrome" {
		t.Errorf("match = %q, want chrome", res.Match)
	}
}

func TestIsGREASE(t *testing.T) {
	if !isGREASE(0x0a0a) || !isGREASE(0x1a1a) {
		t.Error("GREASE values not detected")
	}
	if isGREASE(0x1301) {
		t.Error("real cipher flagged as GREASE")
	}
}
