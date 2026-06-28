// Package web serves the hiddify-health web UI and REST/SSE API.
package web

import (
	"crypto/subtle"
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
	SubOnly     bool   // hide config/deploy mode — subscription-only UI
	BasePath    string // serve the whole UI under this secret prefix, e.g. "/DSJKNLIWPFKLKS/health"
	AdminToken  string // when set, gates the whole UI + API behind a login cookie

	mu      sync.Mutex
	running map[string]bool // exampleDir → in-progress
}

const adminCookie = "hch_admin"

// requireAdmin gates h behind AdminToken. No token configured → open (dev/
// local default, matches the existing /api/health-server admin behaviour).
func (s *Server) requireAdmin(h http.Handler) http.Handler {
	if s.AdminToken == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			s.handleLogin(w, r)
			return
		}
		if c, err := r.Cookie(adminCookie); err == nil && tokenEqual(c.Value, s.AdminToken) {
			h.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Accept") == "application/json" || strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "login", http.StatusSeeOther)
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost || r.URL.Query().Get("token") != "" {
		token := r.URL.Query().Get("token")
		if token == "" {
			token = r.FormValue("token")
		}
		if tokenEqual(token, s.AdminToken) {
			http.SetCookie(w, &http.Cookie{
				Name: adminCookie, Value: token, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
				MaxAge: 30 * 24 * 3600,
			})
			http.Redirect(w, r, "./", http.StatusSeeOther)
			return
		}
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(loginHTML))
}

func tokenEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

const loginHTML = `<!doctype html><html><head><meta charset="utf-8">
<title>Hiddify Health — Admin</title><meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font-family:system-ui;background:#0f1117;color:#e2e8f0;display:flex;height:100vh;align-items:center;justify-content:center;margin:0}
form{background:#161b26;padding:32px;border-radius:12px;border:1px solid #2d3748;width:300px}
h1{font-size:1.1rem;color:#a78bfa;margin:0 0 16px}
input{width:100%;box-sizing:border-box;padding:10px;border-radius:8px;border:1px solid #2d3748;background:#0d1117;color:#e2e8f0;margin-bottom:12px}
button{width:100%;padding:10px;border:0;border-radius:8px;background:#6d28d9;color:#fff;font-weight:600;cursor:pointer}</style></head>
<body><form method="post" action="login"><h1>🛡 Admin access</h1>
<input name="token" type="password" placeholder="Admin token" autofocus>
<button>Enter</button></form></body></html>`

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static files. The index page is served through a wrapper that injects
	// the SubOnly build-time default; everything else is plain static.
	sub, _ := fs.Sub(staticFS, "static")
	fileSrv := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			s.serveIndex(w, sub)
			return
		}
		fileSrv.ServeHTTP(w, r)
	})

	// API.
	mux.HandleFunc("/api/examples", s.handleExamples)
	mux.HandleFunc("/api/run", s.handleRun)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/sub", s.handleSub)

	gated := s.requireAdmin(mux)

	if bp := s.basePrefix(); bp != "" {
		// Mount the entire UI under the secret prefix. Anything outside it
		// 404s (obscurity gate). A bare prefix without trailing slash
		// redirects so relative URLs + <base href> resolve correctly.
		outer := http.NewServeMux()
		outer.Handle(bp+"/", http.StripPrefix(bp, gated))
		outer.HandleFunc(bp, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, bp+"/", http.StatusMovedPermanently)
		})
		return outer
	}
	return gated
}

// basePrefix returns the secret base path normalised to "/foo/bar" (leading
// slash, no trailing slash), or "" when unset.
func (s *Server) basePrefix() string {
	bp := strings.Trim(s.BasePath, "/")
	if bp == "" {
		return ""
	}
	return "/" + bp
}

// serveIndex serves index.html, injecting window.SUB_ONLY when SubOnly is set
// so the page boots in subscription-only mode without a client query param.
func (s *Server) serveIndex(w http.ResponseWriter, sub fs.FS) {
	b, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var head string
	if bp := s.basePrefix(); bp != "" {
		// All API/EventSource calls in the page are relative; <base> makes
		// them resolve under the secret prefix.
		head += `<base href="` + bp + `/">`
	}
	if s.SubOnly {
		head += "<script>window.SUB_ONLY=true;</script>"
	}
	if head != "" {
		b = []byte(strings.Replace(string(b), "</head>", head+"</head>", 1))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
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
	// Note: when deploying without an explicit server, the runner derives the
	// client's SERVER from the SSH host automatically.
	if server := strings.TrimSpace(r.URL.Query().Get("server")); server != "" {
		ov.Vars = map[string]string{"SERVER": server}
	}
	if port := strings.TrimSpace(r.URL.Query().Get("port")); port != "" {
		if ov.Vars == nil {
			ov.Vars = map[string]string{}
		}
		ov.Vars["PORT"] = port
	}

	results, runErr := runner.RunWithOverrides(r.Context(), dir, pw, ov)
	_ = pw.Close()
	drainWG.Wait() // ensure all log lines are flushed before sending result/done

	if runErr != nil {
		writeSSE(w, flusher, "error", runErr.Error())
	}

	for _, res := range results {
		if s.DB != nil {
			if reg, ok := s.DB.RegressionCheck(dir, res.Variant, res.Checks); ok {
				res.Checks = append(res.Checks, reg)
			}
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
