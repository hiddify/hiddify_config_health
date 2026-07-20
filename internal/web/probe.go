package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hiddify/hiddify_config_health/internal/probe"
	"github.com/hiddify/hiddify_config_health/internal/proxyuri"
	"github.com/hiddify/hiddify_config_health/internal/store"
)

// probeState tracks the single active probe session, if any. Only one probe
// may run at a time process-wide — probing more than one proxy concurrently
// would compete for the same upstream bandwidth and invalidate both sets of
// measurements (probe.Runner also enforces this at the package level; this
// mirrors it at the HTTP layer so /api/probe/start can return a clean 409).
type probeState struct {
	mu        sync.Mutex
	runner    *probe.Runner
	sessionID int64
	dir       string
	cancel    context.CancelFunc
}

func (s *Server) probe() *probeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeProbe == nil {
		s.activeProbe = &probeState{}
	}
	return s.activeProbe
}

type probeStartRequest struct {
	ProxyURI     string   `json:"proxy_uri"`
	Core         string   `json:"core"`
	IntervalSecs int      `json:"interval_seconds"`
	Actions      []string `json:"actions"`
	PageloadURL  string   `json:"pageload_url"`
	DownloadURL  string   `json:"download_url"`
	UploadURL    string   `json:"upload_url"`
	BlockAfterN  int      `json:"block_after_failures"`
}

// handleProbeStart begins a new probe session against a single proxy.
// Rejects with 409 if a session is already running (single-proxy constraint).
func (s *Server) handleProbeStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req probeStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.ProxyURI) == "" {
		http.Error(w, "proxy_uri is required", http.StatusBadRequest)
		return
	}
	if req.Core == "" {
		req.Core = "sing-box"
	}

	p, err := proxyuri.ParseURI(req.ProxyURI)
	if err != nil {
		http.Error(w, "bad proxy_uri: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := "probe:" + tagOr(p.Tag, p.Server)

	ps := s.probe()
	ps.mu.Lock()
	if ps.runner != nil {
		ps.mu.Unlock()
		http.Error(w, "a probe session is already running; stop it before starting another", http.StatusConflict)
		return
	}

	cfg := probe.Config{
		ExampleDir:         dir,
		ProxyURI:           req.ProxyURI,
		Core:               req.Core,
		Interval:           time.Duration(req.IntervalSecs) * time.Second,
		Actions:            req.Actions,
		PageloadURL:        req.PageloadURL,
		DownloadURL:        req.DownloadURL,
		UploadURL:          req.UploadURL,
		BlockAfterFailures: req.BlockAfterN,
		Seed:               time.Now().UnixNano(),
	}
	runner, err := probe.New(cfg)
	if err != nil {
		ps.mu.Unlock()
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var sessionID int64
	if s.DB != nil {
		sessionID, _ = s.DB.StartProbeSession(dir, "", cfg.Interval, cfg.Actions)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := runner.Start(ctx); err != nil {
		cancel()
		ps.mu.Unlock()
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	ps.runner = runner
	ps.sessionID = sessionID
	ps.dir = dir
	ps.cancel = cancel
	ps.mu.Unlock()

	go s.pumpProbeSession(runner, sessionID)

	writeJSON(w, map[string]any{"session_id": sessionID, "dir": dir})
}

// pumpProbeSession persists every sample/event from a running probe until its
// channels close (session stopped).
func (s *Server) pumpProbeSession(runner *probe.Runner, sessionID int64) {
	samples := runner.Samples()
	events := runner.Events()
	for samples != nil || events != nil {
		select {
		case sample, ok := <-samples:
			if !ok {
				samples = nil
				continue
			}
			if s.DB != nil {
				_ = s.DB.SaveProbeSample(sessionID, store.ProbeSample{
					Timestamp:           sample.Timestamp,
					OK:                  sample.OK,
					DownloadBPS:         sample.DownloadBPS,
					UploadBPS:           sample.UploadBPS,
					PageloadMs:          sample.PageloadMs,
					Err:                 sample.Err,
					ConsecutiveFailures: sample.ConsecutiveFailures,
					BaselineFailed:      sample.BaselineFailed,
					CoreRestarted:       sample.CoreRestarted,
				})
			}
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if s.DB != nil && ev.Blocked {
				_ = s.DB.MarkProbeBlocked(sessionID, ev.Timestamp)
			}
		}
	}
}

// handleProbeStop ends the active probe session.
func (s *Server) handleProbeStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	ps := s.probe()
	ps.mu.Lock()
	runner, sessionID, cancel := ps.runner, ps.sessionID, ps.cancel
	ps.runner, ps.sessionID, ps.cancel, ps.dir = nil, 0, nil, ""
	ps.mu.Unlock()

	if runner == nil {
		http.Error(w, "no probe session running", http.StatusNotFound)
		return
	}
	runner.Stop()
	if cancel != nil {
		cancel()
	}
	if s.DB != nil {
		_ = s.DB.StopProbeSession(sessionID)
	}
	writeJSON(w, map[string]any{"stopped": true, "session_id": sessionID})
}

// handleProbeStatus reports whether a probe is currently running, and which
// proxy/session — the UI uses this to show the "don't add other traffic"
// warning and to disable starting a second session.
func (s *Server) handleProbeStatus(w http.ResponseWriter, r *http.Request) {
	ps := s.probe()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.runner == nil {
		writeJSON(w, map[string]any{"running": false})
		return
	}
	writeJSON(w, map[string]any{"running": true, "session_id": ps.sessionID, "dir": ps.dir})
}

// handleProbeHistory returns all samples for a session, e.g. to redraw the
// chart after a page reload.
func (s *Server) handleProbeHistory(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeJSON(w, []store.ProbeSample{})
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("session_id"), 10, 64)
	samples, err := s.DB.ProbeSamples(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, samples)
}

// handleProbeSessions lists past probe sessions for a proxy dir key.
func (s *Server) handleProbeSessions(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeJSON(w, []store.ProbeSession{})
		return
	}
	dir := r.URL.Query().Get("dir")
	sessions, err := s.DB.ProbeSessions(dir, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, sessions)
}
