package store

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hiddify/hiddify_config_health/internal/health"
)

// medianOf returns the median of get(m) over all baselines with a positive
// value (zero/missing metrics are ignored so they don't skew the median).
func medianOf(ms []metrics, get func(metrics) float64) float64 {
	var vs []float64
	for _, m := range ms {
		if v := get(m); v > 0 {
			vs = append(vs, v)
		}
	}
	if len(vs) == 0 {
		return 0
	}
	sort.Float64s(vs)
	n := len(vs)
	if n%2 == 1 {
		return vs[n/2]
	}
	return (vs[n/2-1] + vs[n/2]) / 2
}

// Regression thresholds (fractional / absolute) beyond which a metric counts
// as degraded versus the baseline.
// Thresholds are deliberately wide: throughput and latency on a local
// loopback test are noisy run-to-run (cache, scheduler, background load), so a
// regression only fires on a sustained, large change versus the MEDIAN of
// recent baselines — not a single prior run.
const (
	tputDropFrac  = 0.60 // throughput dropped >60% vs median
	latencyUpFrac = 1.50 // latency rose >150% vs median
	entropyDrop   = 0.10 // entropy fell >0.10 absolute
	minBaselines  = 3    // need at least this many prior PASS runs to judge noise
)

// RegressionCheck compares cur's checks against the most recent prior PASS run
// of the same example dir + variant and returns a synthetic "regression"
// health.Result describing any degradation. It returns (Result, true) when a
// baseline existed (so the caller can append the row), or ok=false when there
// is no baseline to compare against yet.
//
// The result is marked Optional (warn-only) — a regression never fails the run
// by itself; it surfaces the deltas for human/CI review.
func (d *DB) RegressionCheck(exampleDir, variant string, cur []health.Result) (health.Result, bool) {
	hist, err := d.History(exampleDir, 50)
	if err != nil {
		return health.Result{}, false
	}
	// Collect metrics from prior PASS runs of this variant (excluding the
	// current run, which has not been saved yet).
	var baseM []metrics
	var lastProbe string
	for i := range hist {
		r := &hist[i]
		if r.Variant == variant && r.Pass && len(r.Checks) > 0 {
			m := metricsOf(r.Checks)
			baseM = append(baseM, m)
			if lastProbe == "" && m.probe != "" {
				lastProbe = m.probe
			}
		}
	}
	if len(baseM) < minBaselines {
		// Not enough history to distinguish a regression from normal noise.
		return health.Result{}, false
	}

	curM := metricsOf(cur)
	medTput := medianOf(baseM, func(m metrics) float64 { return m.tput })
	medLat := medianOf(baseM, func(m metrics) float64 { return m.latency })
	medEnt := medianOf(baseM, func(m metrics) float64 { return m.entropy })

	var deltas []string
	regressed := false

	if medTput > 0 && curM.tput > 0 {
		frac := (medTput - curM.tput) / medTput
		if frac > tputDropFrac {
			regressed = true
			deltas = append(deltas, fmt.Sprintf("throughput -%.0f%% (vs median)", frac*100))
		}
	}
	if medLat > 0 && curM.latency > 0 {
		frac := (curM.latency - medLat) / medLat
		if frac > latencyUpFrac {
			regressed = true
			deltas = append(deltas, fmt.Sprintf("latency +%.0f%% (vs median)", frac*100))
		}
	}
	if medEnt > 0 && curM.entropy > 0 {
		if medEnt-curM.entropy > entropyDrop {
			regressed = true
			deltas = append(deltas, fmt.Sprintf("entropy %.3f→%.3f", medEnt, curM.entropy))
		}
	}
	if lastProbe == "resistant" && curM.probe != "" && curM.probe != "resistant" {
		regressed = true
		deltas = append(deltas, "probe resistant→"+curM.probe)
	}

	extra := "no regression vs baseline"
	if regressed {
		extra = "REGRESSED: " + strings.Join(deltas, ", ")
	}
	return health.Result{
		Name:      "regression",
		OK:        !regressed,
		Optional:  true,
		Regressed: regressed,
		Extra:     extra,
	}, true
}

type metrics struct {
	tput    float64
	latency float64 // nanoseconds (PingAvg)
	entropy float64
	probe   string
}

func metricsOf(checks []health.Result) metrics {
	var m metrics
	for _, c := range checks {
		switch c.Name {
		case "download", "speedtest", "load":
			if c.Throughput > m.tput {
				m.tput = c.Throughput
			}
		case "ping":
			m.latency = float64(c.PingAvg)
		case "entropy":
			m.entropy = c.EntropyScore
		case "active-probe":
			m.probe = c.ProbeVerdict
		}
	}
	return m
}
