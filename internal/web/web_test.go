package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleExamples_Empty(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{ExamplesDir: dir}
	req := httptest.NewRequest(http.MethodGet, "/api/examples", nil)
	w := httptest.NewRecorder()
	srv.handleExamples(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var items []interface{}
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for empty dir, got %d", len(items))
	}
}

func TestHandleExamples_WithRunJSON(t *testing.T) {
	root := t.TempDir()
	exDir := filepath.Join(root, "sing-box", "shadowsocks")
	os.MkdirAll(exDir, 0o755)
	os.WriteFile(filepath.Join(exDir, "run.json"), []byte(`{"name":"Shadowsocks","core":"sing-box"}`), 0o644)
	// Add a sibling config file so hasConfigFiles returns true.
	os.WriteFile(filepath.Join(exDir, "server.json"), []byte(`{}`), 0o644)

	srv := &Server{ExamplesDir: root}
	req := httptest.NewRequest(http.MethodGet, "/api/examples", nil)
	w := httptest.NewRecorder()
	srv.handleExamples(w, req)

	var items []struct {
		Name string `json:"name"`
		Core string `json:"core"`
		Dir  string `json:"dir"`
	}
	json.NewDecoder(w.Body).Decode(&items)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Name != "Shadowsocks" {
		t.Errorf("Name = %q", items[0].Name)
	}
	if items[0].Core != "sing-box" {
		t.Errorf("Core = %q", items[0].Core)
	}
}

func TestHandleStatus_NoDB(t *testing.T) {
	srv := &Server{ExamplesDir: t.TempDir(), DB: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/status?dir=examples/x", nil)
	w := httptest.NewRecorder()
	srv.handleStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandleHistory_NoDB(t *testing.T) {
	srv := &Server{ExamplesDir: t.TempDir(), DB: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/history?dir=examples/x", nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var items []interface{}
	json.NewDecoder(w.Body).Decode(&items)
	if len(items) != 0 {
		t.Errorf("expected 0 items without DB, got %d", len(items))
	}
}

func TestStaticFiles_IndexServed(t *testing.T) {
	srv := &Server{ExamplesDir: t.TempDir()}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / status = %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct == "" {
		t.Error("Content-Type should not be empty for /")
	}
}
