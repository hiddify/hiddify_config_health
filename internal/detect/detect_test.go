package detect

import (
	"testing"

	"github.com/hiddify/hiddify_config_health/internal/health"
)

func TestPassive_AllPass_Opaque(t *testing.T) {
	results := []health.Result{
		{Name: "dns", OK: true},
		{Name: "http", OK: true},
		{Name: "quic", OK: true},
	}
	fp := Passive(results)
	if fp.Verdict != "opaque" {
		t.Errorf("all-pass verdict = %q, want opaque", fp.Verdict)
	}
	if fp.EntropyScore < 0.75 {
		t.Errorf("entropy = %.2f, want >= 0.75", fp.EntropyScore)
	}
}

func TestPassive_AllFail_Blocked(t *testing.T) {
	results := []health.Result{
		{Name: "http", OK: false},
		{Name: "quic", OK: false},
	}
	fp := Passive(results)
	if fp.Verdict != "blocked" {
		t.Errorf("all-fail verdict = %q, want blocked", fp.Verdict)
	}
}

func TestPassive_DNSLeak(t *testing.T) {
	results := []health.Result{
		{Name: "dns", OK: false},  // DNS fails
		{Name: "http", OK: true},  // but HTTP works → leak
	}
	fp := Passive(results)
	if !fp.HasDNSLeak {
		t.Error("expected HasDNSLeak = true")
	}
	if fp.Verdict != "leaking" {
		t.Errorf("leaking verdict = %q, want leaking", fp.Verdict)
	}
}

func TestPassive_SpeedAboveMBps(t *testing.T) {
	results := []health.Result{
		{Name: "download", OK: true, Throughput: 2 << 20}, // 2 MB/s
	}
	fp := Passive(results)
	if !fp.SpeedAboveMBps {
		t.Error("expected SpeedAboveMBps = true for 2 MB/s")
	}
}

func TestPassive_SpeedBelowMBps(t *testing.T) {
	results := []health.Result{
		{Name: "download", OK: true, Throughput: 500 * 1024}, // 500 KB/s
	}
	fp := Passive(results)
	if fp.SpeedAboveMBps {
		t.Error("expected SpeedAboveMBps = false for 500 KB/s")
	}
}

func TestPassive_Empty(t *testing.T) {
	fp := Passive(nil)
	if fp.Verdict != "blocked" {
		t.Errorf("empty verdict = %q, want blocked", fp.Verdict)
	}
	if fp.EntropyScore != 0 {
		t.Errorf("empty entropy = %.2f, want 0", fp.EntropyScore)
	}
}

func TestPassive_QUICFlag(t *testing.T) {
	results := []health.Result{
		{Name: "http", OK: true},
		{Name: "quic", OK: true},
	}
	fp := Passive(results)
	if !fp.LooksLikeQUIC {
		t.Error("expected LooksLikeQUIC = true")
	}
}
