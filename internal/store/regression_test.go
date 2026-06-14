package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hiddify/hiddify_config_health/internal/health"
)

func TestRegressionCheck(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	dir, variant := "examples/x", "v1"

	baseChecks := []health.Result{
		{Name: "download", OK: true, Throughput: 1_000_000},
		{Name: "ping", OK: true, PingAvg: 1 * time.Millisecond},
		{Name: "active-probe", OK: true, ProbeVerdict: "resistant"},
	}

	// No baseline yet → ok=false.
	if _, ok := db.RegressionCheck(dir, variant, nil); ok {
		t.Fatal("expected no baseline on first run")
	}

	// Save several strong baseline PASS runs (median needs >= minBaselines).
	for i := 0; i < minBaselines; i++ {
		base := Record{
			ExampleDir: dir, Variant: variant, Pass: true, StartedAt: time.Now(),
			Checks: baseChecks,
		}
		if _, err := db.Save(base); err != nil {
			t.Fatal(err)
		}
	}

	// A degraded current run: throughput far below median, probe regressed.
	cur := []health.Result{
		{Name: "download", OK: true, Throughput: 200_000}, // -80% vs median 1M
		{Name: "ping", OK: true, PingAvg: 1 * time.Millisecond},
		{Name: "active-probe", OK: false, ProbeVerdict: "fingerprintable"},
	}
	reg, ok := db.RegressionCheck(dir, variant, cur)
	if !ok {
		t.Fatal("expected baseline to exist")
	}
	if !reg.Regressed {
		t.Errorf("expected regression flagged, extra=%q", reg.Extra)
	}
	if !reg.Optional {
		t.Error("regression result must be Optional (warn-only)")
	}

	// A stable current run: no regression.
	reg2, _ := db.RegressionCheck(dir, variant, baseChecks)
	if reg2.Regressed {
		t.Errorf("stable run flagged as regressed: %q", reg2.Extra)
	}
}
