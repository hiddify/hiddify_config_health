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
		Dir     string `json:"dir"`
		Name    string `json:"name"`
		Core    string `json:"core"`
		LastRun *store.Record `json:"last_run,omitempty"`
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
		info := exampleInfo{Dir: e.dir, Name: e.name, Core: e.core}
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
	go func() {
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

	results, runErr := runner.Run(r.Context(), dir, pw)
	_ = pw.Close()

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
	dir  string
	name string
	core string
}

func scanExamples(root string) ([]exampleEntry, error) {
	var out []exampleEntry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() != "run.json" {
			return nil
		}
		dir := filepath.Dir(path)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var rc struct {
			Name string `json:"name"`
			Core string `json:"core"`
		}
		_ = json.Unmarshal(b, &rc)
		if rc.Name == "" {
			rc.Name = filepath.Base(dir)
		}
		out = append(out, exampleEntry{dir: dir, name: rc.Name, core: rc.Core})
		return nil
	})
	return out, err
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
