# Result History

Every test run is persisted in a SQLite database so you can track regressions
across core versions and config changes.

## Database location

Default: `~/.hiddify-health/results.db`

Override with `--db`:

```bash
./hiddify-health --db /var/lib/hiddify-health/results.db run examples/sing-box/shadowsocks
```

The database is created automatically on first use. No manual migration needed.

## Schema

```sql
CREATE TABLE runs (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  example_dir      TEXT,
  name             TEXT,
  core_version     TEXT,
  pass             INTEGER,       -- 0 or 1
  checks_json      TEXT,          -- JSON array of health.Result
  fingerprint_json TEXT,          -- JSON detect.TrafficFingerprint
  log              TEXT,
  started_at       INTEGER,       -- Unix timestamp
  duration_ms      INTEGER
);
```

## CLI: `history`

```bash
# Latest result per example (all examples)
./hiddify-health history

# Last 20 runs for one example
./hiddify-health history examples/sing-box/shadowsocks
```

Output:

```
ID   DIR                                RESULT  CORE                            STARTED               DURATION
42   examples/sing-box/shadowsocks      PASS    sing-box version 1.11.0         2025-06-01 12:34:56   450ms
41   examples/sing-box/shadowsocks      FAIL    sing-box version 1.10.2         2025-05-31 09:10:00   31000ms
40   examples/xray/vless-xhttp          PASS    Xray 25.5.1 (XTLS)              2025-05-30 18:00:00   620ms
```

## Web UI history

The results panel shows the last 10 runs for the currently-selected example
with a ✓/✗ icon, timestamp, and duration.

## Comparing versions (manual)

Use the raw SQLite file with any SQLite browser or `sqlite3` CLI:

```bash
sqlite3 ~/.hiddify-health/results.db \
  "SELECT core_version, pass, duration_ms FROM runs WHERE example_dir='examples/sing-box/shadowsocks' ORDER BY started_at DESC LIMIT 20;"
```

To compare check-level results across two runs:

```bash
sqlite3 ~/.hiddify-health/results.db \
  "SELECT id, checks_json FROM runs WHERE id IN (41,42);" | python3 -m json.tool
```

## Retention

There is no automatic pruning — the database grows indefinitely. To trim:

```bash
sqlite3 ~/.hiddify-health/results.db \
  "DELETE FROM runs WHERE started_at < strftime('%s', 'now', '-30 days');"
sqlite3 ~/.hiddify-health/results.db "VACUUM;"
```
