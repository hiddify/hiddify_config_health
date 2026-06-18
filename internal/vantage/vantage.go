// Package vantage runs the same proxy reachability test from multiple network
// egress points (a fleet of SSH hosts in different regions), producing a
// reachability matrix — the single strongest censorship signal: "works from
// DE, blocked from IR". The real SSH-backed vantage reuses internal/runner's
// SSH helpers; tests use a mock vantage. No live fleet is wired by default —
// users configure endpoints via Config.
package vantage

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Result is one vantage's view of a proxy endpoint.
type Result struct {
	Name      string  `json:"name"`      // vantage label, e.g. "de-fsn", "ir-thr"
	Region    string  `json:"region"`    // coarse region/country
	Reachable bool    `json:"reachable"`
	LatencyMs float64 `json:"latency_ms,omitempty"`
	Verdict   string  `json:"verdict"`   // ok | blocked | error
	Err       string  `json:"error,omitempty"`
}

// Vantage tests reachability of host:port from one egress point.
type Vantage interface {
	Name() string
	Region() string
	// Probe reports whether host:port is reachable from this vantage, with RTT.
	Probe(ctx context.Context, host string, port int) (reachable bool, latency time.Duration, err error)
}

// Matrix runs every vantage against host:port concurrently and returns the
// per-vantage results sorted by name. When no vantages are configured it
// returns nil (the caller treats vantage as "not measured", never a failure).
func Matrix(ctx context.Context, vs []Vantage, host string, port int) []Result {
	if len(vs) == 0 {
		return nil
	}
	out := make([]Result, len(vs))
	var wg sync.WaitGroup
	for i, v := range vs {
		wg.Add(1)
		go func(i int, v Vantage) {
			defer wg.Done()
			r := Result{Name: v.Name(), Region: v.Region()}
			reach, lat, err := v.Probe(ctx, host, port)
			switch {
			case err != nil:
				r.Verdict, r.Err = "error", err.Error()
			case reach:
				r.Reachable, r.Verdict = true, "ok"
				r.LatencyMs = float64(lat.Microseconds()) / 1000.0
			default:
				r.Verdict = "blocked"
			}
			out[i] = r
		}(i, v)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Summary renders a compact one-line matrix cell, e.g. "de:ok 40ms · ir:blocked".
func Summary(rs []Result) string {
	if len(rs) == 0 {
		return ""
	}
	s := ""
	for i, r := range rs {
		if i > 0 {
			s += " · "
		}
		s += r.Name + ":" + r.Verdict
	}
	return s
}
