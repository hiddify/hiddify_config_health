package score

import (
	"testing"
	"time"

	"github.com/hiddify/hiddify_config_health/internal/detect"
	"github.com/hiddify/hiddify_config_health/internal/health"
)

func TestScoreUnsupportedIsZero(t *testing.T) {
	if b := Score(Input{Supported: false}); b.Total != 0 {
		t.Errorf("unsupported should score 0, got %d", b.Total)
	}
}

func TestScoreGoodProxyHigh(t *testing.T) {
	in := Input{
		Pass: true, Supported: true,
		Fingerprint: detect.TrafficFingerprint{Verdict: "opaque"},
		Checks: []health.Result{
			{Name: "ping", OK: true, PingAvg: 30 * time.Millisecond},
			{Name: "download", OK: true, Throughput: 3 << 20},
			{Name: "active-probe", OK: true, ProbeVerdict: "resistant"},
			{Name: "tls-fingerprint", OK: true, TLSMatch: "chrome"},
		},
	}
	b := Score(in)
	if b.Total < 85 {
		t.Errorf("good proxy should score high, got %d (%+v)", b.Total, b)
	}
}

func TestScoreBlockedLow(t *testing.T) {
	in := Input{
		Pass: false, Supported: true,
		Fingerprint: detect.TrafficFingerprint{Verdict: "blocked"},
		Checks: []health.Result{
			{Name: "active-probe", OK: false, ProbeVerdict: "unreachable"},
		},
	}
	if b := Score(in); b.Total > 20 {
		t.Errorf("blocked proxy should score low, got %d", b.Total)
	}
}

func TestRankOrders(t *testing.T) {
	good := Input{Pass: true, Supported: true, Fingerprint: detect.TrafficFingerprint{Verdict: "opaque"},
		Checks: []health.Result{{Name: "active-probe", OK: true, ProbeVerdict: "resistant"}}}
	bad := Input{Pass: false, Supported: true, Fingerprint: detect.TrafficFingerprint{Verdict: "blocked"}}
	r := Rank([]string{"bad", "good"}, []Input{bad, good})
	if r[0].Key != "good" {
		t.Errorf("good should rank first, got %v", r)
	}
}
