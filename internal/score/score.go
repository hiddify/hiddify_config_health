// Package score combines measured signals into a single explainable 0–100
// "censorship-survival + quality" score per proxy×core. The weights are plain
// constants (no ML / no external service) so the result is fully transparent.
package score

import (
	"sort"

	"github.com/hiddify/hiddify_config_health/internal/detect"
	"github.com/hiddify/hiddify_config_health/internal/health"
)

// Input is the minimal set of signals the scorer reads. Callers map their
// result type (e.g. proxytest.CoreResult) onto this.
type Input struct {
	Pass        bool
	Supported   bool
	Fingerprint detect.TrafficFingerprint
	Checks      []health.Result
	GeoFlagged  bool // server IP on a known-blocked/burned ASN range
}

// Breakdown is the explainable component contribution to the final score.
type Breakdown struct {
	Connectivity float64 `json:"connectivity"` // 0..40
	Survival     float64 `json:"survival"`     // 0..35 (censor/probe/reality/dpi)
	Quality      float64 `json:"quality"`      // 0..25 (latency/jitter/throughput/stability)
	Total        int     `json:"total"`        // 0..100
}

// Score returns the 0–100 score and its breakdown.
func Score(in Input) Breakdown {
	var b Breakdown
	if !in.Supported {
		return b // 0 — core can't run this protocol
	}

	// Connectivity (0..40): did the core checks pass at all.
	if in.Pass {
		b.Connectivity = 40
	} else {
		// Partial credit if some non-optional checks passed.
		var total, ok int
		for _, c := range in.Checks {
			if c.Optional {
				continue
			}
			total++
			if c.OK {
				ok++
			}
		}
		if total > 0 {
			b.Connectivity = 40 * float64(ok) / float64(total)
		}
	}

	// Survival (0..35): censorship resistance.
	b.Survival = survivalScore(in)

	// Quality (0..25): latency/jitter/throughput/stability.
	b.Quality = qualityScore(in.Checks)

	total := b.Connectivity + b.Survival + b.Quality
	// Geo penalty: server IP on a known-blocked/burned ASN range.
	if in.GeoFlagged {
		total *= 0.7
	}
	b.Total = int(total + 0.5)
	if b.Total > 100 {
		b.Total = 100
	}
	return b
}

func survivalScore(in Input) float64 {
	s := 0.0
	// Censor verdict (0..15).
	switch in.Fingerprint.Verdict {
	case "opaque":
		s += 15
	case "recognizable":
		s += 9
	case "probeable", "leaking":
		s += 3
	case "blocked":
		s += 0
	default:
		s += 6
	}
	// Active-probe verdict (0..12) and reality/dpi/tls (0..8) from checks.
	probe := 6.0
	extra := 0.0
	for _, c := range in.Checks {
		switch c.Name {
		case "active-probe":
			switch c.ProbeVerdict {
			case "resistant":
				probe = 12
			case "greylisted":
				probe = 7
			case "timing-leak":
				probe = 4
			case "fingerprintable":
				probe = 2
			case "unreachable":
				probe = 0
			}
		case "tls-fingerprint":
			if c.TLSMatch != "" && c.TLSMatch != "none" {
				extra += 4
			}
		case "reality-verify":
			if c.OK {
				extra += 4
			}
		case "dpi-classify":
			if c.OK { // OK == not flagged
				extra += 2
			}
		}
	}
	if extra > 8 {
		extra = 8
	}
	return s + probe + extra
}

func qualityScore(checks []health.Result) float64 {
	q := 0.0
	for _, c := range checks {
		switch c.Name {
		case "ping":
			// latency: <50ms great, scale down to 0 at 400ms (0..10).
			ms := float64(c.PingAvg.Microseconds()) / 1000.0
			q += clamp(10*(1-(ms-50)/350), 0, 10)
		case "download", "load", "speedtest":
			// throughput: 0 at 0, 8 at >=2 MB/s (0..8).
			mbps := c.Throughput / (1 << 20)
			q += clamp(8*mbps/2, 0, 8)
		case "stability":
			if c.OK {
				q += 4
			}
		case "pageload":
			if c.OK {
				q += 3
			}
		}
	}
	return clamp(q, 0, 25)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Ranked pairs an arbitrary key with its score, sorted best-first.
type Ranked struct {
	Key   string
	Score int
	B     Breakdown
}

// Rank sorts inputs by score descending; keys identify each input.
func Rank(keys []string, ins []Input) []Ranked {
	out := make([]Ranked, 0, len(ins))
	for i := range ins {
		b := Score(ins[i])
		k := ""
		if i < len(keys) {
			k = keys[i]
		}
		out = append(out, Ranked{Key: k, Score: b.Total, B: b})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}
