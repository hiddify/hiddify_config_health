// Package web serves the hiddify-health web UI and REST/SSE API.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hiddify/hiddify_config_health/internal/runner"
	"github.com/hiddify/hiddify_config_health/internal/store"
)

//go:embed static
var staticFS embed.FS

// Server is the web UI HTTP server.
type Server struct {
	ExamplesDir string
	DB          *store.DB

	mu      sync.Mutex
	running map[string]bool // exampleDir → in-progress
}

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static files.
	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// API.
	mux.HandleFunc("/api/examples", s.handleExamples)
	mux.HandleFunc("/api/run", s.handleRun)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/history", s.handleHistory)

	return mux
}

// --- /api/examples ---

func (s *Server) handleExamples(w http.ResponseWriter, r *http.Request) {
	examples, err := scanExamples(s.ExamplesDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type exampleInfo struct {
		Dir            string `json:"dir"`
		Name           string `json:"name"`
		Core           string `json:"core"`
		DeployToServer string `json:"deploy_to_server,omitempty"`
		Server         string `json:"server,omitempty"`
		LastRun        *store.Record `json:"last_run,omitempty"`
	}
	var out []exampleInfo
	var latest []store.Record
	if s.DB != nil {
		latest, _ = s.DB.AllLatest()
	}
	latestByDir := map[string]store.Record{}
	for _, r := range latest {
		latestByDir[r.ExampleDir] = r
	}
	for _, e := range examples {
		info := exampleInfo{Dir: e.dir, Name: e.name, Core: e.core, DeployToServer: e.deployToServer, Server: e.server}
		if rec, ok := latestByDir[e.dir]; ok {
			info.LastRun = &rec
		}
		out = append(out, info)
	}
	writeJSON(w, out)
}

// --- /api/run  (SSE) ---

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "missing dir param", http.StatusBadRequest)
		return
	}

	// SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	if s.running == nil {
		s.running = map[string]bool{}
	}
	if s.running[dir] {
		s.mu.Unlock()
		writeSSE(w, flusher, "error", "already running")
		return
	}
	s.running[dir] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.running, dir)
		s.mu.Unlock()
	}()

	pr, pw, _ := os.Pipe()
	var drainWG sync.WaitGroup
	drainWG.Add(1)
	go func() {
		defer drainWG.Done()
		defer pr.Close()
		buf := make([]byte, 512)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				line := strings.TrimRight(string(buf[:n]), "\n")
				writeSSE(w, flusher, "log", line)
			}
			if err != nil {
				return
			}
		}
	}()

	ov := runner.Overrides{
		DeployToServer: strings.TrimSpace(r.URL.Query().Get("deploy")),
	}
	if server := strings.TrimSpace(r.URL.Query().Get("server")); server != "" {
		ov.Vars = map[string]string{"SERVER": server}
	}

	results, runErr := runner.RunWithOverrides(r.Context(), dir, pw, ov)
	_ = pw.Close()
	drainWG.Wait() // ensure all log lines are flushed before sending result/done

	if runErr != nil {
		writeSSE(w, flusher, "error", runErr.Error())
	}

	for _, res := range results {
		if s.DB != nil {
			rec := store.Record{
				ExampleDir:  dir,
				Name:        res.Name,
				Variant:     res.Variant,
				CoreVersion: res.CoreVersion,
				Pass:        res.Pass,
				Checks:      res.Checks,
				Fingerprint: res.Fingerprint,
				Log:         res.Log,
				StartedAt:   res.StartedAt,
				DurationMs:  res.Duration.Milliseconds(),
			}
			_, _ = s.DB.Save(rec)
		}
		b, _ := json.Marshal(res)
		writeSSE(w, flusher, "result", string(b))
	}

	writeSSE(w, flusher, "done", "")
}

// --- /api/status ---

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if s.DB == nil {
		writeJSON(w, nil)
		return
	}
	recs, err := s.DB.History(dir, 1)
	if err != nil || len(recs) == 0 {
		writeJSON(w, nil)
		return
	}
	writeJSON(w, recs[0])
}

// --- /api/history ---

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if s.DB == nil {
		writeJSON(w, []store.Record{})
		return
	}
	recs, err := s.DB.History(dir, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, recs)
}

// --- helpers ---

type exampleEntry struct {
	dir            string
	name           string
	core           string
	deployToServer string
	server         string
}

func scanExamples(root string) ([]exampleEntry, error) {
	var out []exampleEntry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() != "run.json" && d.Name() != "run.json.j2" {
			return nil
		}
		dir := filepath.Dir(path)
		// A dir with both run.json and run.json.j2 is visited twice; the .j2
		// is the source of truth, so skip the plain run.json in that case.
		if d.Name() == "run.json" {
			if _, err := os.Stat(filepath.Join(dir, "run.json.j2")); err == nil {
				return nil
			}
		}
		// Skip inheritance-only run.json files (no sibling config files).
		if !hasConfigFiles(dir) {
			return nil
		}
		cfg, err := runner.LoadRunConfig(dir)
		if err != nil {
			return nil
		}
		name := cfg.Name
		if name == "" {
			name = filepath.Base(dir)
		}
		entry := exampleEntry{dir: dir, name: name, core: cfg.Core, deployToServer: cfg.DeployToServer}
		if variants := cfg.Variants(); len(variants) > 0 {
			entry.server = variants[0].Vars["SERVER"]
		}
		out = append(out, entry)
		return nil
	})
	return out, err
}

// hasConfigFiles reports whether dir has at least one config file
// (*.json, *.j2, *.tpl) other than run.json itself.
func hasConfigFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "run.json" {
			continue
		}
		switch filepath.Ext(name) {
		case ".json", ".j2", ".tpl":
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeSSE(w http.ResponseWriter, f http.Flusher, event, data string) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	f.Flush()
}

// ListenAndServe starts the server on addr.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // streaming
	}
	return srv.ListenAndServe()
}
