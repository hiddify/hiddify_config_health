package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hiddify/hiddify_config_health/internal/detect"
	"github.com/hiddify/hiddify_config_health/internal/health"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSave_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	rec := Record{
		ExampleDir:  "examples/sing-box/shadowsocks",
		Name:        "Shadowsocks",
		Variant:     "plain",
		CoreVersion: "sing-box 1.11.0",
		Pass:        true,
		Checks: []health.Result{
			{Name: "dns", OK: true, Duration: 38 * time.Millisecond},
			{Name: "http", OK: true, Duration: 210 * time.Millisecond},
		},
		Fingerprint: detect.TrafficFingerprint{Verdict: "opaque", EntropyScore: 1.0},
		Log:         "all checks passed",
		StartedAt:   now,
		DurationMs:  450,
	}

	id, err := db.Save(rec)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id <= 0 {
		t.Errorf("ID = %d, want > 0", id)
	}

	rows, err := db.History("examples/sing-box/shadowsocks", 1)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	got := rows[0]
	if got.Name != "Shadowsocks" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Variant != "plain" {
		t.Errorf("Variant = %q, want plain", got.Variant)
	}
	if !got.Pass {
		t.Error("Pass should be true")
	}
	if got.CoreVersion != "sing-box 1.11.0" {
		t.Errorf("CoreVersion = %q", got.CoreVersion)
	}
	if got.Fingerprint.Verdict != "opaque" {
		t.Errorf("Fingerprint.Verdict = %q", got.Fingerprint.Verdict)
	}
	if len(got.Checks) != 2 {
		t.Errorf("Checks len = %d, want 2", len(got.Checks))
	}
	if got.DurationMs != 450 {
		t.Errorf("DurationMs = %d, want 450", got.DurationMs)
	}
}

func TestSave_FailRecord(t *testing.T) {
	db := openTestDB(t)
	rec := Record{
		ExampleDir: "examples/xray/vless-xhttp",
		Name:       "VLESS",
		Pass:       false,
		StartedAt:  time.Now(),
	}
	_, err := db.Save(rec)
	if err != nil {
		t.Fatalf("Save fail record: %v", err)
	}
}

func TestHistory_Limit(t *testing.T) {
	db := openTestDB(t)
	dir := "examples/test"
	for i := 0; i < 5; i++ {
		db.Save(Record{ExampleDir: dir, Name: "T", StartedAt: time.Now()})
	}
	rows, err := db.History(dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Errorf("got %d rows with limit 3, want 3", len(rows))
	}
}

func TestAllLatest_OnePerDir(t *testing.T) {
	db := openTestDB(t)
	dirs := []string{"examples/a", "examples/b", "examples/c"}
	for _, dir := range dirs {
		// Save two records per dir; AllLatest should return only the last.
		db.Save(Record{ExampleDir: dir, Name: "first", Pass: false, StartedAt: time.Now()})
		db.Save(Record{ExampleDir: dir, Name: "second", Pass: true, StartedAt: time.Now()})
	}
	rows, err := db.AllLatest()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Errorf("got %d rows, want 3 (one per dir)", len(rows))
	}
	// Each should be the "second" record.
	for _, r := range rows {
		if r.Name != "second" {
			t.Errorf("dir %q: Name = %q, want second", r.ExampleDir, r.Name)
		}
	}
}

func TestHistory_Empty(t *testing.T) {
	db := openTestDB(t)
	rows, err := db.History("nonexistent/dir", 10)
	if err != nil {
		t.Fatalf("History nonexistent: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}
