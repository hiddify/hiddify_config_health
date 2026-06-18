package main

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// --- IP rate limiter (ingest abuse protection) ---

// rateLimiter allows one event per key per window. In-memory; for a single
// instance this is sufficient. Behind multiple replicas, move to Redis.
type rateLimiter struct {
	mu     sync.Mutex
	last   map[string]time.Time
	window time.Duration
}

func newRateLimiter(window time.Duration) *rateLimiter {
	rl := &rateLimiter{last: map[string]time.Time{}, window: window}
	go rl.gc()
	return rl
}

// allow reports whether key may act now; if so it records the action.
func (rl *rateLimiter) allow(key string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if t, ok := rl.last[key]; ok {
		if wait := rl.window - now.Sub(t); wait > 0 {
			return false, wait
		}
	}
	rl.last[key] = now
	return true, 0
}

func (rl *rateLimiter) gc() {
	t := time.NewTicker(rl.window)
	for range t.C {
		cutoff := time.Now().Add(-rl.window)
		rl.mu.Lock()
		for k, v := range rl.last {
			if v.Before(cutoff) {
				delete(rl.last, k)
			}
		}
		rl.mu.Unlock()
	}
}

// clientIP extracts the real client IP, honouring X-Forwarded-For /
// X-Real-IP set by a trusted reverse proxy (set TRUST_PROXY=1 to use them).
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// First hop is the original client.
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return strings.TrimSpace(xr)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- admin auth (dashboard is private to the operator) ---

const adminCookie = "hc_admin"

// adminAuth gates a handler behind the admin token. The token is supplied once
// at /login (form or ?token=) and stored in a cookie; requests without a valid
// cookie are redirected to /login.
func (s *server) adminAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.adminToken == "" {
			// No token configured → dashboard open (dev/local). Warn once at boot.
			h(w, r)
			return
		}
		if c, err := r.Cookie(adminCookie); err == nil && tokenEqual(c.Value, s.adminToken) {
			h(w, r)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost || r.URL.Query().Get("token") != "" {
		token := r.URL.Query().Get("token")
		if token == "" {
			token = r.FormValue("token")
		}
		if tokenEqual(token, s.adminToken) {
			http.SetCookie(w, &http.Cookie{
				Name: adminCookie, Value: token, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
				MaxAge: 30 * 24 * 3600,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
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
<body><form method="post" action="/login"><h1>🛡 Admin access</h1>
<input name="token" type="password" placeholder="Admin token" autofocus>
<button>Enter</button></form></body></html>`
