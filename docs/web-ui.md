# Web UI

The web UI is a single embedded HTML page — no npm, no build step, no CDN.

## Starting

```bash
./hiddify-health serve --addr :8080
# Open http://localhost:8080
```

Flags:

| Flag | Default | Description |
|---|---|---|
| `--addr` | `:8080` | Listen address |
| `--examples` | `examples` | Root directory to scan for `run.json` files |
| `--db` | `~/.hiddify-health/results.db` | SQLite history database |

## Layout

```
┌─────────────────┬──────────────────────────────────┬──────────────────┐
│  Sidebar        │  Live log                        │  Results panel   │
│                 │                                  │                  │
│  • Shadowsocks  │  [core] starting sing-box…       │  Results         │
│    sing-box     │  [wait] SOCKS ready              │  dns      ✓      │
│    PASS         │  [check] dns  PASS               │  http     ✓      │
│                 │  [check] http PASS               │  quic     ✓      │
│  • VLESS xHTTP  │  [check] quic PASS               │                  │
│    xray         │  PASS duration=450ms…            │  Verdict: opaque │
│    FAIL         │                                  │  Entropy: 100%   │
│                 │                                  │                  │
│                 │                                  │  History         │
│                 │                                  │  ✓ 2025-06-01    │
│  [▶ Run]        │                                  │  ✗ 2025-05-31    │
└─────────────────┴──────────────────────────────────┴──────────────────┘
```

## Sidebar

- Lists all examples found under `--examples`.
- Each entry shows: name, core, last result badge (PASS / FAIL / RUNNING).
- Badges update live when a run completes.

## Log panel

Live output streamed via **Server-Sent Events (SSE)** as the test runs.
Log lines are colour-coded: green for PASS, red for FAIL, blue for info.
The panel auto-scrolls to the bottom.

## Results panel

After a run completes:

- Per-check table: name, pass/fail icon, extra detail (throughput, ping avg).
- **Protocol fingerprint badge** — `opaque` / `recognizable` / `leaking` / `blocked`.
  See [detect.md](detect.md).
- Core version string.
- **History** — last 10 runs for the selected example with timestamp and duration.

## REST API

The web server exposes a small JSON API consumed by the HTML page:

### `GET /api/examples`

Returns all discovered examples.

```json
[
  {
    "dir":  "examples/sing-box/shadowsocks",
    "name": "Shadowsocks (sing-box)",
    "core": "sing-box",
    "last_run": { "pass": true, "started_at": 1748736000, "duration_ms": 450 }
  }
]
```

`last_run` is `null` if no run has been recorded yet.

### `POST /api/run?dir=<path>`

Starts a test and streams output via SSE. Event types:

| Event | Data |
|---|---|
| `log` | One log line (string) |
| `result` | JSON-encoded `runner.Result` |
| `error` | Error message string |
| `done` | Empty — stream ends |

### `GET /api/status?dir=<path>`

Returns the most recent `store.Record` for `dir`, or `null`.

### `GET /api/history?dir=<path>`

Returns the last 50 `store.Record` rows for `dir` (newest first).

## Concurrent runs

The server blocks a second run for the same example directory while one is
in progress (returns `error: already running`). Different examples can run
in parallel.
