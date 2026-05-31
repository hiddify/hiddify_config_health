// Package store persists run results in a SQLite database.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hiddify/hiddify_config_health/internal/detect"
	"github.com/hiddify/hiddify_config_health/internal/health"
)

// DB wraps a SQLite connection.
type DB struct{ db *sql.DB }

// Record is one persisted run.
type Record struct {
	ID          int64
	ExampleDir  string
	Name        string
	CoreVersion string
	Pass        bool
	Checks      []health.Result
	Fingerprint detect.TrafficFingerprint
	Log         string
	StartedAt   time.Time
	DurationMs  int64
}

// DefaultPath returns ~/.hiddify-health/results.db.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hiddify-health", "results.db")
}

// Open opens (or creates) the database at path.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// Save persists a run record and returns its row ID.
func (d *DB) Save(r Record) (int64, error) {
	checksJSON, _ := json.Marshal(r.Checks)
	fpJSON, _ := json.Marshal(r.Fingerprint)
	res, err := d.db.Exec(`
		INSERT INTO runs(example_dir, name, core_version, pass, checks_json, fingerprint_json, log, started_at, duration_ms)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		r.ExampleDir, r.Name, r.CoreVersion,
		boolInt(r.Pass), string(checksJSON), string(fpJSON),
		r.Log, r.StartedAt.UTC().Unix(), r.DurationMs,
	)
	if err != nil {
		return 0, fmt.Errorf("store: save: %w", err)
	}
	return res.LastInsertId()
}

// History returns the last n records for the given example directory.
// n <= 0 returns all.
func (d *DB) History(exampleDir string, n int) ([]Record, error) {
	limit := -1
	if n > 0 {
		limit = n
	}
	rows, err := d.db.Query(`
		SELECT id, example_dir, name, core_version, pass, checks_json, fingerprint_json, log, started_at, duration_ms
		FROM runs WHERE example_dir = ?
		ORDER BY started_at DESC LIMIT ?`, exampleDir, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecords(rows)
}

// AllLatest returns the most recent record for every distinct example_dir.
func (d *DB) AllLatest() ([]Record, error) {
	rows, err := d.db.Query(`
		SELECT id, example_dir, name, core_version, pass, checks_json, fingerprint_json, log, started_at, duration_ms
		FROM runs
		WHERE id IN (SELECT MAX(id) FROM runs GROUP BY example_dir)
		ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecords(rows)
}

func scanRecords(rows *sql.Rows) ([]Record, error) {
	var out []Record
	for rows.Next() {
		var r Record
		var passInt int
		var checksJSON, fpJSON string
		var startedAt int64
		if err := rows.Scan(&r.ID, &r.ExampleDir, &r.Name, &r.CoreVersion,
			&passInt, &checksJSON, &fpJSON, &r.Log, &startedAt, &r.DurationMs); err != nil {
			return nil, err
		}
		r.Pass = passInt == 1
		r.StartedAt = time.Unix(startedAt, 0).UTC()
		_ = json.Unmarshal([]byte(checksJSON), &r.Checks)
		_ = json.Unmarshal([]byte(fpJSON), &r.Fingerprint)
		out = append(out, r)
	}
	return out, rows.Err()
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS runs (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			example_dir      TEXT NOT NULL,
			name             TEXT NOT NULL DEFAULT '',
			core_version     TEXT NOT NULL DEFAULT '',
			pass             INTEGER NOT NULL DEFAULT 0,
			checks_json      TEXT NOT NULL DEFAULT '[]',
			fingerprint_json TEXT NOT NULL DEFAULT '{}',
			log              TEXT NOT NULL DEFAULT '',
			started_at       INTEGER NOT NULL,
			duration_ms      INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_runs_dir ON runs(example_dir);
	`)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
