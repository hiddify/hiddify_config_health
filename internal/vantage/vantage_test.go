package vantage

import (
	"context"
	"net"
	"testing"
	"time"
)

// mockVantage returns a fixed verdict for tests.
type mockVantage struct {
	name, region string
	reachable    bool
	lat          time.Duration
}

func (m mockVantage) Name() string   { return m.name }
func (m mockVantage) Region() string { return m.region }
func (m mockVantage) Probe(_ context.Context, _ string, _ int) (bool, time.Duration, error) {
	return m.reachable, m.lat, nil
}

func TestMatrixMixed(t *testing.T) {
	vs := []Vantage{
		mockVantage{"de", "DE", true, 40 * time.Millisecond},
		mockVantage{"ir", "IR", false, 0},
	}
	res := Matrix(context.Background(), vs, "1.2.3.4", 443)
	if len(res) != 2 {
		t.Fatalf("want 2, got %d", len(res))
	}
	// Sorted by name: de, ir.
	if res[0].Name != "de" || !res[0].Reachable || res[0].Verdict != "ok" {
		t.Errorf("de: %+v", res[0])
	}
	if res[1].Name != "ir" || res[1].Reachable || res[1].Verdict != "blocked" {
		t.Errorf("ir: %+v", res[1])
	}
	if Summary(res) != "de:ok · ir:blocked" {
		t.Errorf("summary: %q", Summary(res))
	}
}

func TestMatrixEmpty(t *testing.T) {
	if Matrix(context.Background(), nil, "x", 1) != nil {
		t.Error("no vantages should return nil (not measured)")
	}
}

func TestLocalVantageReachable(t *testing.T) {
	// Listen on a local port; LocalVantage must reach it.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	v := LocalVantage{Label: "local", Reg: "local"}
	reach, lat, err := v.Probe(context.Background(), "127.0.0.1", port)
	if err != nil || !reach || lat <= 0 {
		t.Errorf("local listener should be reachable: reach=%v lat=%v err=%v", reach, lat, err)
	}

	// Unreachable port → blocked, no error.
	reach2, _, err2 := v.Probe(context.Background(), "127.0.0.1", 1)
	if err2 != nil || reach2 {
		t.Errorf("port 1 should be blocked: reach=%v err=%v", reach2, err2)
	}
}
