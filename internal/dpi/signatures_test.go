package dpi

import "testing"

func TestClassifyTLS(t *testing.T) {
	tls := []byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01, 0x02, 0x03}
	v := Classify(tls)
	if v.WouldFlag {
		t.Errorf("normal TLS should not be flagged: %+v", v)
	}
	if v.ClassifiedAs != "tls-clienthello" {
		t.Errorf("class: %s", v.ClassifiedAs)
	}
}

func TestClassifyPlaintextHTTP(t *testing.T) {
	v := Classify([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	if !v.WouldFlag {
		t.Errorf("plaintext HTTP should be flagged: %+v", v)
	}
}

func TestClassifyEmpty(t *testing.T) {
	v := Classify(nil)
	if v.ClassifiedAs != "unknown" || v.WouldFlag {
		t.Errorf("empty: %+v", v)
	}
}

func TestClassifyOpaque(t *testing.T) {
	// High-entropy random-looking bytes that aren't TLS/HTTP → opaque, not flagged.
	b := make([]byte, 64)
	for i := range b {
		b[i] = byte(i*7 + 3)
	}
	v := Classify(b)
	if v.WouldFlag {
		t.Errorf("high-entropy opaque flow should not be flagged: %+v", v)
	}
}
