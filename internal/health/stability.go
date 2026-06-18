package health

import (
	"fmt"
	"math"
	"time"
)

// stabilityResult captures connection stability over a hold window.
type stabilityResult struct {
	Samples      int
	Flaps        int           // failed RTT samples (drops)
	RTTStdDevMs  float64       // jitter of latency over time
	RTTMeanMs    float64
	Throttled    bool          // throughput/RTT degraded over the window
}

// testStability holds the proxy open for cfg.StabilityDur, sampling RTT every
// ~1s, and detects drops (flaps), RTT variance, and throttling onset (rising
// RTT trend = a censor shaping the connection). Heavy — only in --full.
func testStability(d dialer, cfg Config) (stabilityResult, error) {
	dur := cfg.StabilityDur
	if dur <= 0 {
		dur = 20 * time.Second
	}
	deadline := time.Now().Add(dur)

	var rtts []float64
	res := stabilityResult{}
	for time.Now().Before(deadline) {
		samples, err := testPing(d, cfg) // one ping burst
		if err != nil || len(samples) == 0 {
			res.Flaps++
		} else {
			avg, _, _, _ := summarizePing(samples)
			rtts = append(rtts, float64(avg.Microseconds())/1000.0)
		}
		res.Samples++
		time.Sleep(time.Second)
	}

	if len(rtts) > 0 {
		var sum float64
		for _, v := range rtts {
			sum += v
		}
		res.RTTMeanMs = sum / float64(len(rtts))
		var variance float64
		for _, v := range rtts {
			variance += (v - res.RTTMeanMs) * (v - res.RTTMeanMs)
		}
		res.RTTStdDevMs = math.Sqrt(variance / float64(len(rtts)))
		// Throttling heuristic: second half mean RTT >> first half mean RTT.
		res.Throttled = risingTrend(rtts)
	}
	return res, nil
}

// risingTrend reports whether the latter half's mean is >40% above the first.
func risingTrend(xs []float64) bool {
	if len(xs) < 4 {
		return false
	}
	mid := len(xs) / 2
	var a, b float64
	for _, v := range xs[:mid] {
		a += v
	}
	for _, v := range xs[mid:] {
		b += v
	}
	a /= float64(mid)
	b /= float64(len(xs) - mid)
	return a > 0 && b > a*1.4
}

func (r stabilityResult) ok() bool { return r.Flaps == 0 && !r.Throttled }

func (r stabilityResult) extra() string {
	return fmt.Sprintf("samples=%d flaps=%d rtt=%.1f±%.1fms throttled=%v",
		r.Samples, r.Flaps, r.RTTMeanMs, r.RTTStdDevMs, r.Throttled)
}
