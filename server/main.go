// Command health-check-server is the central ingest + dashboard service for
// hiddify health-check. It accepts ANONYMOUS, PII-stripped result batches from
// clients (opt-in), stores them in Postgres, shows an aggregate dashboard, and
// gives each anonymous user a private link (/r/<anon_id>) to view only their
// own results.
//
// Deploy behind health-check.hiddify.com. Config via env:
//
//	DATABASE_URL  postgres://user:pass@host:5432/db   (required)
//	ADDR          listen address (default :8080)
//	PUBLIC_URL    base URL for private links (default https://health-check.hiddify.com)
package main

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed static
var staticFS embed.FS

type server struct {
	store      *Store
	publicURL  string
	adminToken string
	trustProxy bool
	ingestRL   *rateLimiter
}

func main() {
	ctx := context.Background()
	addr := envOr("ADDR", ":8080")
	publicURL := envOr("PUBLIC_URL", "https://health-check.hiddify.com")

	// Ingest rate limit: one upload per client IP per window (default 10m).
	window := 10 * time.Minute
	if v := os.Getenv("INGEST_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			window = d
		}
	}

	srv := &server{
		publicURL:  publicURL,
		adminToken: os.Getenv("ADMIN_TOKEN"),
		trustProxy: os.Getenv("TRUST_PROXY") == "1",
		ingestRL:   newRateLimiter(window),
	}
	if srv.adminToken == "" {
		log.Println("WARNING: ADMIN_TOKEN not set — dashboard is PUBLIC (set ADMIN_TOKEN to protect it)")
	}
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		st, err := openStore(ctx, dsn)
		if err != nil {
			log.Fatalf("database: %v", err)
		}
		srv.store = st
		log.Println("connected to Postgres")
	} else {
		log.Println("WARNING: DATABASE_URL not set — ingest/dashboard disabled (health only)")
	}

	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	dashboard := http.FileServer(http.FS(sub))

	// Public, rate-limited: anyone may upload anonymous results.
	mux.HandleFunc("/ingest", srv.handleIngest)
	// Public: a user's own results page (guarded only by the secret token in URL).
	mux.HandleFunc("/api/r/", srv.handleUserAPI)
	mux.HandleFunc("/r/", srv.handleUserPage)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/login", srv.handleLogin)

	// Admin-only: the aggregate dashboard + its data.
	mux.HandleFunc("/api/aggregates", srv.adminAuth(srv.handleAggregates))
	mux.HandleFunc("/", srv.adminAuth(func(w http.ResponseWriter, r *http.Request) {
		dashboard.ServeHTTP(w, r)
	}))

	httpSrv := &http.Server{Addr: addr, Handler: logMW(mux), ReadTimeout: 20 * time.Second}
	log.Printf("health-check-server listening on %s", addr)
	log.Fatal(httpSrv.ListenAndServe())
}

// handleIngest accepts an anonymous result batch.
func (s *server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.store == nil {
		http.Error(w, "ingest disabled (no database)", http.StatusServiceUnavailable)
		return
	}
	// Rate limit: one upload per client IP per window.
	ip := clientIP(r, s.trustProxy)
	if ok, wait := s.ingestRL.allow(ip); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		http.Error(w, "rate limited: try again in "+wait.Round(time.Second).String(), http.StatusTooManyRequests)
		return
	}
	var batch struct {
		AnonID  string   `json:"anon_id"`
		Reports []Report `json:"reports"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&batch); err != nil {
		http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if batch.AnonID == "" || len(batch.Reports) == 0 {
		http.Error(w, "anon_id and reports required", http.StatusBadRequest)
		return
	}
	if err := s.store.insertReports(r.Context(), batch.AnonID, batch.Reports); err != nil {
		log.Printf("insert: %v", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"ok":   true,
		"link": s.publicURL + "/r/" + batch.AnonID,
	})
}

func (s *server) handleAggregates(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, []Aggregate{})
		return
	}
	aggs, err := s.store.aggregates(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, aggs)
}

func (s *server) handleUserAPI(w http.ResponseWriter, r *http.Request) {
	anon := strings.TrimPrefix(r.URL.Path, "/api/r/")
	if anon == "" || s.store == nil {
		writeJSON(w, []Report{})
		return
	}
	rows, err := s.store.userRows(r.Context(), anon, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, rows)
}

// handleUserPage serves the private results page; the JS reads /api/r/<id>.
func (s *server) handleUserPage(w http.ResponseWriter, r *http.Request) {
	b, err := staticFS.ReadFile("static/user.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func logMW(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
