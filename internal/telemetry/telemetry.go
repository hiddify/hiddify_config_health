package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// DefaultEndpoint is the central health-check ingest server.
const DefaultEndpoint = "https://health-check.hiddify.com"

// Report is the anonymous payload submitted for one tested proxy×core. It
// carries NO credentials, NO server IP/host, NO SNI — only the structural
// Shape plus measured metrics. The AnonID ties a user's own submissions
// together for their private results link; it is a random local token, not
// derived from any PII.
type Report struct {
	AnonID    string  `json:"anon_id"`
	Shape     Shape   `json:"shape"`
	Core      string  `json:"core"`
	Pass      bool    `json:"pass"`
	Censor    string  `json:"censor,omitempty"`
	Score     int     `json:"score"`
	LatencyMs float64 `json:"latency_ms,omitempty"`
	JitterMs  float64 `json:"jitter_ms,omitempty"`
	DownBPS   float64 `json:"down_bps,omitempty"`
	UpBPS     float64 `json:"up_bps,omitempty"`
	Country   string  `json:"country,omitempty"` // coarse, from offline geo (no exact IP)
	ASN       string  `json:"asn,omitempty"`
	ClientVer string  `json:"client_version,omitempty"`
	Ts        int64   `json:"ts"`
}

// Batch is what gets POSTed: a baseline (no-proxy) line speed plus the per
// proxy×core reports.
type Batch struct {
	AnonID   string    `json:"anon_id"`
	Baseline *Baseline `json:"baseline,omitempty"`
	Reports  []Report  `json:"reports"`
}

// Baseline mirrors the no-proxy line measurement (kept here to avoid importing
// pkg/proxytest into this internal package).
type Baseline struct {
	LatencyMs   float64 `json:"latency_ms"`
	DownloadBPS float64 `json:"download_bps"`
	UploadBPS   float64 `json:"upload_bps"`
}

// SubmitResponse is the server's reply.
type SubmitResponse struct {
	OK      bool   `json:"ok"`
	Link    string `json:"link"`    // private results URL: /r/<anon_id>
	Message string `json:"message,omitempty"`
}

// AnonID returns the persistent random anonymous id, creating it on first use.
// Stored at ~/.hiddify-health/anon_id. No PII; rotate by deleting the file.
func AnonID() string {
	path := anonIDPath()
	if b, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(b)) >= 16 {
		return string(bytes.TrimSpace(b))
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	id := hex.EncodeToString(buf)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(id), 0o600)
	return id
}

func anonIDPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hiddify-health", "anon_id")
}

// Submit posts the batch to the ingest endpoint and returns the private link.
// Submission is strictly opt-in — callers only invoke this when the user agreed.
func Submit(ctx context.Context, endpoint string, b Batch) (*SubmitResponse, error) {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	body, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/ingest", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("submit: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("submit: server returned HTTP %d", resp.StatusCode)
	}
	var sr SubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, err
	}
	return &sr, nil
}
