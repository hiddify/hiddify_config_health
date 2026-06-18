package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/hiddify/hiddify_config_health/internal/score"
	"github.com/hiddify/hiddify_config_health/internal/store"
	"github.com/hiddify/hiddify_config_health/internal/telemetry"
	"github.com/hiddify/hiddify_config_health/pkg/proxytest"
)

// subRequest is the POST body for /api/sub.
type subRequest struct {
	SubURL string `json:"sub_url"` // subscription link, OR
	Text   string `json:"text"`    // pasted proxy URIs (one per line)
	Full   bool   `json:"full"`    // run the heavy advanced suite
	Core   string `json:"core"`    // optional: restrict to one core
	Submit bool   `json:"submit"`  // opt-in: anonymous submission to central server
}

// subRow is one streamed (proxy,core) result.
type subRow struct {
	Tag       string  `json:"tag"`
	Protocol  string  `json:"protocol"`
	Server    string  `json:"server"`
	Port      int     `json:"port"`
	Core      string  `json:"core"`
	Supported bool    `json:"supported"`
	Pass      bool    `json:"pass"`
	Censor    string  `json:"censor"`
	LatencyMs float64 `json:"latency_ms"`
	Score     int     `json:"score"`
	Country   string  `json:"country,omitempty"`
	ASN       string  `json:"asn,omitempty"`
	Flagged   bool    `json:"flagged,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// handleSub parses a subscription link or pasted proxy list, tests every proxy
// on each core, and streams scored results over SSE. Privacy: raw URIs and
// credentials are never persisted — only tag/server/port/metrics.
func (s *Server) handleSub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req subRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no streaming", http.StatusInternalServerError)
		return
	}

	opts := proxytest.Options{Full: req.Full, Timeout: 10 * time.Second}
	if req.Core != "" {
		opts.Cores = []string{req.Core}
	}

	// Gather proxy results: subscription URL (fetch+parse+test) or pasted text.
	ctx := r.Context()
	var results []proxytest.ProxyResult
	if strings.TrimSpace(req.SubURL) != "" {
		rs, errs := proxytest.TestSubscription(ctx, req.SubURL, opts)
		for _, e := range errs {
			writeSSE(w, flusher, "warn", e.Error())
		}
		results = rs
	} else {
		var uris []string
		for _, ln := range strings.Split(req.Text, "\n") {
			if ln = strings.TrimSpace(ln); ln != "" && !strings.HasPrefix(ln, "#") {
				uris = append(uris, ln)
			}
		}
		results = proxytest.TestProxies(ctx, uris, opts)
	}
	if len(results) == 0 {
		writeSSE(w, flusher, "error", "no proxies to test")
		writeSSE(w, flusher, "done", "")
		return
	}

	// Baseline (no-proxy) line speed — emitted first so the UI can show proxy
	// overhead relative to the user's raw connection.
	if bl, err := json.Marshal(proxytest.MeasureBaseline(ctx, 15*time.Second)); err == nil {
		writeSSE(w, flusher, "baseline", string(bl))
	}

	for _, res := range results {
		for _, cr := range res.PerCore {
			sc := score.Score(score.Input{
				Pass: cr.Pass, Supported: cr.Supported,
				Fingerprint: cr.Fingerprint, Checks: cr.Checks,
				GeoFlagged: res.Geo.Flagged,
			})
			row := subRow{
				Tag: res.Tag, Protocol: res.Protocol, Server: res.Server, Port: res.Port,
				Core: cr.Core, Supported: cr.Supported, Pass: cr.Pass,
				Censor: cr.Fingerprint.Verdict, LatencyMs: subLatency(cr), Score: sc.Total,
				Country: res.Geo.Country, ASN: res.Geo.ASN, Flagged: res.Geo.Flagged, Error: cr.Err,
			}
			b, _ := json.Marshal(row)
			writeSSE(w, flusher, "row", string(b))

			if s.DB != nil && cr.Supported {
				_, _ = s.DB.Save(store.Record{
					ExampleDir:  "sub:" + tagOr(res.Tag, res.Server),
					Name:        res.Protocol + " " + tagOr(res.Tag, res.Server),
					Variant:     cr.Core,
					Pass:        cr.Pass,
					Checks:      cr.Checks,
					Fingerprint: cr.Fingerprint,
					StartedAt:   time.Now(),
				})
			}
		}
	}

	// Opt-in anonymous submission to the central server.
	if req.Submit {
		if link, err := submitBatch(ctx, results); err != nil {
			writeSSE(w, flusher, "warn", "submit failed: "+err.Error())
		} else {
			writeSSE(w, flusher, "link", link)
		}
	}

	writeSSE(w, flusher, "done", "")
}

// submitBatch scrubs results to anonymous shapes and submits (opt-in).
func submitBatch(ctx context.Context, results []proxytest.ProxyResult) (string, error) {
	anon := telemetry.AnonID()
	batch := telemetry.Batch{AnonID: anon}
	for _, pr := range results {
		shape := telemetry.Scrub(pr.Proxy())
		for _, cr := range pr.PerCore {
			if !cr.Supported {
				continue
			}
			sc := score.Score(score.Input{Pass: cr.Pass, Supported: cr.Supported,
				Fingerprint: cr.Fingerprint, Checks: cr.Checks, GeoFlagged: pr.Geo.Flagged})
			batch.Reports = append(batch.Reports, telemetry.Report{
				AnonID: anon, Shape: shape, Core: cr.Core, Pass: cr.Pass,
				Censor: cr.Fingerprint.Verdict, Score: sc.Total, LatencyMs: subLatency(cr),
				Country: pr.Geo.Country, ASN: pr.Geo.ASN,
				Ts: time.Now().Unix(),
			})
		}
	}
	if len(batch.Reports) == 0 {
		return "", errNothingToSubmit
	}
	resp, err := telemetry.Submit(ctx, telemetry.DefaultEndpoint, batch)
	if err != nil {
		return "", err
	}
	return resp.Link, nil
}

var errNothingToSubmit = errorString("nothing to submit")

type errorString string

func (e errorString) Error() string { return string(e) }

func subLatency(cr proxytest.CoreResult) float64 {
	for _, c := range cr.Checks {
		if c.Name == "ping" && c.PingAvg > 0 {
			return float64(c.PingAvg.Microseconds()) / 1000.0
		}
	}
	return 0
}

func tagOr(tag, server string) string {
	if tag = strings.TrimSpace(tag); tag != "" {
		return tag
	}
	return server
}
