package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps the Postgres pool and the ingest schema.
type Store struct{ pool *pgxpool.Pool }

func openStore(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{pool: pool}
	return s, s.migrate(ctx)
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS reports (
  id          BIGSERIAL PRIMARY KEY,
  anon_id     TEXT NOT NULL,
  protocol    TEXT,
  transport   TEXT,
  security    TEXT,
  has_flow    BOOLEAN,
  has_reality BOOLEAN,
  cipher      TEXT,
  core        TEXT,
  pass        BOOLEAN,
  censor      TEXT,
  score       INT,
  latency_ms  DOUBLE PRECISION,
  jitter_ms   DOUBLE PRECISION,
  down_bps    DOUBLE PRECISION,
  up_bps      DOUBLE PRECISION,
  country     TEXT,
  asn         TEXT,
  client_ver  TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_reports_anon ON reports(anon_id);
CREATE INDEX IF NOT EXISTS idx_reports_created ON reports(created_at);
`)
	return err
}

// Report is one ingested row (mirrors the client telemetry.Report).
type Report struct {
	AnonID     string  `json:"anon_id"`
	Shape      Shape   `json:"shape"`
	Core       string  `json:"core"`
	Pass       bool    `json:"pass"`
	Censor     string  `json:"censor"`
	Score      int     `json:"score"`
	LatencyMs  float64 `json:"latency_ms"`
	JitterMs   float64 `json:"jitter_ms"`
	DownBPS    float64 `json:"down_bps"`
	UpBPS      float64 `json:"up_bps"`
	Country    string  `json:"country"`
	ASN        string  `json:"asn"`
	ClientVer  string  `json:"client_version"`
}

// Shape is the anonymous structural description (mirrors client side).
type Shape struct {
	Protocol   string `json:"protocol"`
	Transport  string `json:"transport"`
	Security   string `json:"security"`
	HasFlow    bool   `json:"has_flow"`
	HasReality bool   `json:"has_reality"`
	HasALPN    bool   `json:"has_alpn"`
	Cipher     string `json:"cipher"`
}

func (s *Store) insertReports(ctx context.Context, anonID string, reports []Report) error {
	batchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, r := range reports {
		_, err := s.pool.Exec(batchCtx, `
INSERT INTO reports(anon_id,protocol,transport,security,has_flow,has_reality,cipher,
  core,pass,censor,score,latency_ms,jitter_ms,down_bps,up_bps,country,asn,client_ver)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
			anonID, r.Shape.Protocol, r.Shape.Transport, r.Shape.Security,
			r.Shape.HasFlow, r.Shape.HasReality, r.Shape.Cipher,
			r.Core, r.Pass, r.Censor, r.Score, r.LatencyMs, r.JitterMs,
			r.DownBPS, r.UpBPS, r.Country, r.ASN, r.ClientVer)
		if err != nil {
			return err
		}
	}
	return nil
}

// userRows returns recent reports for one anon id (the private results view).
func (s *Store) userRows(ctx context.Context, anonID string, limit int) ([]Report, error) {
	rows, err := s.pool.Query(ctx, `
SELECT protocol,transport,security,core,pass,censor,score,latency_ms,down_bps,up_bps,country,asn,created_at
FROM reports WHERE anon_id=$1 ORDER BY created_at DESC LIMIT $2`, anonID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Report
	for rows.Next() {
		var r Report
		var created time.Time
		if err := rows.Scan(&r.Shape.Protocol, &r.Shape.Transport, &r.Shape.Security,
			&r.Core, &r.Pass, &r.Censor, &r.Score, &r.LatencyMs, &r.DownBPS, &r.UpBPS,
			&r.Country, &r.ASN, &created); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Aggregate is one row of the global dashboard: stats per (protocol,transport,security).
type Aggregate struct {
	Protocol   string  `json:"protocol"`
	Transport  string  `json:"transport"`
	Security   string  `json:"security"`
	Samples    int     `json:"samples"`
	PassRate   float64 `json:"pass_rate"`
	AvgScore   float64 `json:"avg_score"`
	OpaqueRate float64 `json:"opaque_rate"`
}

func (s *Store) aggregates(ctx context.Context) ([]Aggregate, error) {
	rows, err := s.pool.Query(ctx, `
SELECT protocol,transport,security,
       COUNT(*),
       AVG(CASE WHEN pass THEN 1.0 ELSE 0.0 END),
       AVG(score),
       AVG(CASE WHEN censor='opaque' THEN 1.0 ELSE 0.0 END)
FROM reports
WHERE created_at > now() - interval '7 days'
GROUP BY protocol,transport,security
ORDER BY AVG(score) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Aggregate
	for rows.Next() {
		var a Aggregate
		if err := rows.Scan(&a.Protocol, &a.Transport, &a.Security,
			&a.Samples, &a.PassRate, &a.AvgScore, &a.OpaqueRate); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
