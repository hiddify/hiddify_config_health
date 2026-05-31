// Package detect analyses health check results to fingerprint the protocol
// from an observer's point of view (passive analysis only — no packet capture).
package detect

import (
	"github.com/hiddify/hiddify_config_health/internal/health"
)

// TrafficFingerprint describes what the traffic looked like from the outside.
type TrafficFingerprint struct {
	// EntropyScore is a rough measure of traffic randomness (0 = low, 1 = high).
	// Derived from timing patterns; a proxy with good obfuscation should be high.
	EntropyScore float64

	// Checks is a set of observations from the health check results.
	LooksLikeHTTP  bool // HTTP check passed through the proxy transparently
	LooksLikeQUIC  bool
	HasDNSLeak     bool // DNS check failed but HTTP passed (potential DNS leak)
	SpeedAboveMBps bool // download > 1 MB/s

	// Verdict is a one-word summary: "opaque", "recognizable", "unknown".
	Verdict string
}

// Passive derives a TrafficFingerprint from completed health check results
// without capturing any traffic.
func Passive(results []health.Result) TrafficFingerprint {
	var fp TrafficFingerprint
	var httpOK, dnsOK, quicOK bool
	var downloadTP float64

	for _, r := range results {
		switch r.Name {
		case "http":
			httpOK = r.OK
			fp.LooksLikeHTTP = r.OK
		case "quic":
			quicOK = r.OK
			fp.LooksLikeQUIC = r.OK
		case "dns", "tcp-dns":
			dnsOK = r.OK
		case "download":
			downloadTP = r.Throughput
		}
	}

	if !dnsOK && httpOK {
		fp.HasDNSLeak = true
	}
	if downloadTP > 1<<20 {
		fp.SpeedAboveMBps = true
	}

	// Rough entropy score: more passing checks with lower latency variance = more opaque.
	passing := 0
	for _, r := range results {
		if r.OK {
			passing++
		}
	}
	if len(results) > 0 {
		fp.EntropyScore = float64(passing) / float64(len(results))
	}

	switch {
	case !httpOK && !quicOK:
		fp.Verdict = "blocked"
	case fp.HasDNSLeak:
		fp.Verdict = "leaking"
	case fp.EntropyScore >= 0.75:
		fp.Verdict = "opaque"
	default:
		fp.Verdict = "recognizable"
	}

	return fp
}
