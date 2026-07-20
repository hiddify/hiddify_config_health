package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Probe session status values.
const (
	ProbeStatusRunning     = "running"
	ProbeStatusStopped     = "stopped"
	ProbeStatusInterrupted = "interrupted"
	ProbeStatusBlocked     = "blocked"
)

// ProbeSession is one probe-time monitoring run against a single proxy.
type ProbeSession struct {
	ID              int64
	ExampleDir      string
	Variant         string
	IntervalSeconds int
	Actions         []string
	Status          string
	StartedAt       time.Time
	StoppedAt       *time.Time
	BlockedAt       *time.Time
}

// ProbeSample is one tick of a probe session.
type ProbeSample struct {
	ID                  int64
	SessionID           int64
	Timestamp           time.Time
	OK                  bool
	DownloadBPS         float64
	UploadBPS           float64
	PageloadMs          float64
	Err                 string
	ConsecutiveFailures int
	BaselineFailed      bool
	CoreRestarted       bool
}

func migrateProbe(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS probe_sessions (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			example_dir      TEXT NOT NULL,
			variant          TEXT NOT NULL DEFAULT '',
			interval_seconds INTEGER NOT NULL DEFAULT 0,
			actions_json     TEXT NOT NULL DEFAULT '[]',
			status           TEXT NOT NULL DEFAULT 'running',
			started_at       INTEGER NOT NULL,
			stopped_at       INTEGER,
			blocked_at       INTEGER
		);
		CREATE INDEX IF NOT EXISTS idx_probe_sessions_dir ON probe_sessions(example_dir);

		CREATE TABLE IF NOT EXISTS probe_samples (
			id                   INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id           INTEGER NOT NULL,
			ts                   INTEGER NOT NULL,
			ok                   INTEGER NOT NULL DEFAULT 0,
			download_bps         REAL NOT NULL DEFAULT 0,
			upload_bps           REAL NOT NULL DEFAULT 0,
			pageload_ms          REAL NOT NULL DEFAULT 0,
			err                  TEXT NOT NULL DEFAULT '',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			baseline_failed      INTEGER NOT NULL DEFAULT 0,
			core_restarted       INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_probe_samples_session ON probe_samples(session_id);
	`)
	return err
}

// StartProbeSession creates a new probe session row and returns its ID.
func (d *DB) StartProbeSession(exampleDir, variant string, interval time.Duration, actions []string) (int64, error) {
	actionsJSON, _ := json.Marshal(actions)
	res, err := d.db.Exec(`
		INSERT INTO probe_sessions(example_dir, variant, interval_seconds, actions_json, status, started_at)
		VALUES (?,?,?,?,?,?)`,
		exampleDir, variant, int(interval.Seconds()), string(actionsJSON), ProbeStatusRunning, time.Now().UTC().Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("store: start probe session: %w", err)
	}
	return res.LastInsertId()
}

// SaveProbeSample persists one tick of a probe session, then prunes old
// samples beyond the retention window so a long-running session doesn't grow
// the DB unbounded.
func (d *DB) SaveProbeSample(sessionID int64, s ProbeSample) error {
	_, err := d.db.Exec(`
		INSERT INTO probe_samples(session_id, ts, ok, download_bps, upload_bps, pageload_ms, err, consecutive_failures, baseline_failed, core_restarted)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		sessionID, s.Timestamp.UTC().Unix(), boolInt(s.OK), s.DownloadBPS, s.UploadBPS, s.PageloadMs,
		s.Err, s.ConsecutiveFailures, boolInt(s.BaselineFailed), boolInt(s.CoreRestarted),
	)
	if err != nil {
		return fmt.Errorf("store: save probe sample: %w", err)
	}
	return d.PruneProbeSamples(sessionID, probeSampleRetention)
}

// probeSampleRetention is the max number of samples kept per session.
const probeSampleRetention = 10_000

// PruneProbeSamples deletes the oldest samples for sessionID beyond the most
// recent keep rows.
func (d *DB) PruneProbeSamples(sessionID int64, keep int) error {
	_, err := d.db.Exec(`
		DELETE FROM probe_samples
		WHERE session_id = ? AND id NOT IN (
			SELECT id FROM probe_samples WHERE session_id = ? ORDER BY ts DESC LIMIT ?
		)`, sessionID, sessionID, keep)
	return err
}

// MarkProbeBlocked records when a session's proxy was first detected blocked.
func (d *DB) MarkProbeBlocked(sessionID int64, at time.Time) error {
	_, err := d.db.Exec(`UPDATE probe_sessions SET status = ?, blocked_at = ? WHERE id = ? AND blocked_at IS NULL`,
		ProbeStatusBlocked, at.UTC().Unix(), sessionID)
	return err
}

// StopProbeSession marks a session cleanly stopped.
func (d *DB) StopProbeSession(sessionID int64) error {
	_, err := d.db.Exec(`UPDATE probe_sessions SET status = ?, stopped_at = ? WHERE id = ?`,
		ProbeStatusStopped, time.Now().UTC().Unix(), sessionID)
	return err
}

// SweepInterruptedSessions marks any session still "running" from a prior
// process lifetime as "interrupted" — the monitor died, not necessarily the
// proxy. Call once at process startup before accepting new probe requests.
func (d *DB) SweepInterruptedSessions() error {
	_, err := d.db.Exec(`UPDATE probe_sessions SET status = ?, stopped_at = ? WHERE status = ?`,
		ProbeStatusInterrupted, time.Now().UTC().Unix(), ProbeStatusRunning)
	return err
}

// ProbeSamples returns all samples for a session, oldest first.
func (d *DB) ProbeSamples(sessionID int64) ([]ProbeSample, error) {
	rows, err := d.db.Query(`
		SELECT id, session_id, ts, ok, download_bps, upload_bps, pageload_ms, err, consecutive_failures, baseline_failed, core_restarted
		FROM probe_samples WHERE session_id = ? ORDER BY ts ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProbeSample
	for rows.Next() {
		var s ProbeSample
		var okInt, baseFailInt, restartInt int
		var ts int64
		if err := rows.Scan(&s.ID, &s.SessionID, &ts, &okInt, &s.DownloadBPS, &s.UploadBPS, &s.PageloadMs,
			&s.Err, &s.ConsecutiveFailures, &baseFailInt, &restartInt); err != nil {
			return nil, err
		}
		s.Timestamp = time.Unix(ts, 0).UTC()
		s.OK = okInt == 1
		s.BaselineFailed = baseFailInt == 1
		s.CoreRestarted = restartInt == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

// ProbeSessions returns the most recent n sessions for exampleDir (n<=0 = all).
func (d *DB) ProbeSessions(exampleDir string, n int) ([]ProbeSession, error) {
	limit := -1
	if n > 0 {
		limit = n
	}
	rows, err := d.db.Query(`
		SELECT id, example_dir, variant, interval_seconds, actions_json, status, started_at, stopped_at, blocked_at
		FROM probe_sessions WHERE example_dir = ? ORDER BY started_at DESC LIMIT ?`, exampleDir, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProbeSession
	for rows.Next() {
		var s ProbeSession
		var actionsJSON string
		var startedAt int64
		var stoppedAt, blockedAt sql.NullInt64
		if err := rows.Scan(&s.ID, &s.ExampleDir, &s.Variant, &s.IntervalSeconds, &actionsJSON, &s.Status,
			&startedAt, &stoppedAt, &blockedAt); err != nil {
			return nil, err
		}
		s.StartedAt = time.Unix(startedAt, 0).UTC()
		_ = json.Unmarshal([]byte(actionsJSON), &s.Actions)
		if stoppedAt.Valid {
			t := time.Unix(stoppedAt.Int64, 0).UTC()
			s.StoppedAt = &t
		}
		if blockedAt.Valid {
			t := time.Unix(blockedAt.Int64, 0).UTC()
			s.BlockedAt = &t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
