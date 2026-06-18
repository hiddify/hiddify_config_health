package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	rl := newRateLimiter(time.Hour)
	if ok, _ := rl.allow("1.2.3.4"); !ok {
		t.Fatal("first call should be allowed")
	}
	if ok, wait := rl.allow("1.2.3.4"); ok || wait <= 0 {
		t.Errorf("second call should be blocked with wait>0, got ok=%v wait=%v", ok, wait)
	}
	if ok, _ := rl.allow("5.6.7.8"); !ok {
		t.Error("different IP should be allowed")
	}
}

func TestRateLimiterWindowExpiry(t *testing.T) {
	rl := newRateLimiter(20 * time.Millisecond)
	rl.allow("ip")
	if ok, _ := rl.allow("ip"); ok {
		t.Fatal("should be blocked immediately")
	}
	time.Sleep(30 * time.Millisecond)
	if ok, _ := rl.allow("ip"); !ok {
		t.Error("should be allowed after window")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/ingest", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")

	if ip := clientIP(r, false); ip != "10.0.0.1" {
		t.Errorf("no-trust: want remote 10.0.0.1, got %s", ip)
	}
	if ip := clientIP(r, true); ip != "203.0.113.9" {
		t.Errorf("trust-proxy: want XFF client 203.0.113.9, got %s", ip)
	}
}

func TestTokenEqual(t *testing.T) {
	if !tokenEqual("abc", "abc") {
		t.Error("equal tokens should match")
	}
	if tokenEqual("abc", "abd") {
		t.Error("different tokens must not match")
	}
}
