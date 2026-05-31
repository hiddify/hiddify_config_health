package cert

import (
	"encoding/pem"
	"os"
	"testing"
)

func TestGenerate_ValidBundle(t *testing.T) {
	b, err := Generate([]string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.CACertPEM) == 0 || len(b.CertPEM) == 0 || len(b.KeyPEM) == 0 {
		t.Fatal("empty PEM fields")
	}
	if len(b.CAFingerprint) != 64 {
		t.Errorf("CAFingerprint len = %d, want 64", len(b.CAFingerprint))
	}
	block, _ := pem.Decode(b.CACertPEM)
	if block == nil {
		t.Fatal("CACertPEM did not decode as PEM")
	}
}

func TestGenerate_TLSConfig(t *testing.T) {
	b, err := Generate([]string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := b.TLSConfig()
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("expected 1 certificate, got %d", len(cfg.Certificates))
	}
}

func TestWriteToDir(t *testing.T) {
	b, _ := Generate([]string{"localhost"})
	dir := t.TempDir()
	ca, cert, key, err := WriteToDir(b, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ca, cert, key} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file %s not created: %v", path, err)
		}
	}
}
