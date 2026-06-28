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
| `--sub-only` | `false` | Subscription-only UI — hide the config/deploy mode tabs |
| `--secret-path` | `""` | Serve the whole UI under a secret prefix `/<path>/health/` (`auto` = random token) |
| `--admin-token` | `""` | Require this token (cookie login) to access the UI/API — empty = open |
| `--force-local` | `false` | Resolve subscription hosts to `127.0.0.1` instead of real DNS — use behind a reverse proxy |

## Secret base path

By default the UI is served at the root (`http://host:8090/`). To hide it
behind an unguessable prefix, pass `--secret-path`:

```bash
# fixed secret
./hiddify-health serve --secret-path DSJKNLIWPFKLKS
# → http://host:8090/DSJKNLIWPFKLKS/health/

# random token (printed at startup)
./hiddify-health serve --secret-path auto
# → http://host:8090/DDE6YYXAYMI2UMFJ/health/
```

The value is normalised to `/<path>/health/`. Behaviour:

- Everything under the prefix works normally (page + `api/*` + SSE).
- Anything **outside** the prefix returns **404** — `/`, `/api/examples`, any
  other path. The path acts as a shared secret (obscurity gate, not auth).
- A bare prefix without trailing slash (`/<path>/health`) `301`-redirects to
  the trailing-slash form.
- The page injects `<base href="/<path>/health/">`; all in-page requests are
  relative, so they resolve under the prefix automatically.

> This is obscurity, not authentication — anyone with the link has full
> access. For real protection, also front it with a reverse proxy + auth.

## Admin login (`--admin-token`)

`--secret-path` alone is just obscurity — no login. To require auth on top
of it, pass `--admin-token`:

```bash
./hiddify-health serve --secret-path auto --admin-token <strong-random-token>
```

Behaviour:

- Gates the **whole** UI and API (everything under the secret prefix,
  including `/api/sub`) behind a login cookie.
- Unauthenticated requests are redirected to `login` (an in-prefix page);
  `/api/*` requests get `401` instead of a redirect.
- Login: POST the token to `login` (form field `token` or `?token=`).
  Sets an `HttpOnly` cookie (`hch_admin`), valid 30 days.
- Empty/unset `--admin-token` (default) = no auth, current open behaviour.
- This panel is meant for the admin/operator to test their own configs and
  subscription links — not for end users. Always set `--admin-token` on any
  internet-reachable deployment.

## Deploying behind a reverse proxy (TLS)

Recommended production setup: run `hiddify-health serve` bound to
`127.0.0.1`, and put a TLS-terminating reverse proxy (nginx, Caddy, etc.) in
front of it serving `https://a.com/<secret-path>/health/`.

```bash
./hiddify-health serve --addr 127.0.0.1:8090 --secret-path auto --admin-token <token>
```

Point the reverse proxy at `127.0.0.1:8090`, terminate TLS there, and forward
the path through unchanged.

### `--force-local`

When testing a subscription link that points back at your own domain (e.g.
the panel is reachable at `https://a.com/secret_path/...` and you paste that
same domain as the subscription URL), the panel would otherwise resolve
`a.com` over real DNS and go back out through the public internet. Pass
`--force-local` to make subscription fetches resolve straight to
`127.0.0.1` (port preserved) instead:

```bash
./hiddify-health serve --addr 127.0.0.1:8090 --force-local
```

Only affects subscription URL fetches (`POST /api/sub` with `sub_url`); pasted
proxy links (`text`) are unaffected since they're not fetched over HTTP.

## Modes

The page has two mutually-exclusive modes, switched by the tabs at the top of
the sidebar:

- **Configs / Deploy** — pick an example config, run it locally, or deploy the
  server to a remote host (Global SSH). Default.
- **Subscription / Links** — paste a subscription URL or proxy links; each
  proxy is tested on sing-box & xray, scored, and compared to a no-proxy
  baseline.

### Subscription-only deployment

To run a public, link-testing-only instance (no example/deploy UI), start with
`--sub-only`:

```bash
./hiddify-health serve --addr :8080 --sub-only
```

The mode tabs are hidden and the page boots straight into the Subscription
mode. (Server-side: injects `window.SUB_ONLY=true` into the page.)

### URL parameters

The page reads query parameters on load, so a subscription can be opened with a
pre-filled link — e.g. `https://host/?sub=https://example.com/sub.txt`:

| Param | Effect |
|---|---|
| `?sub=<url>` | Pre-fill the subscription URL and switch to Subscription mode |
| `?text=<links>` | Pre-fill the proxy-links box (`,` is treated as newline) |
| `?full=1` | Tick the full-suite checkbox |
| `?run=1` | Auto-start the test once filled |
| `?subonly=1` | Hide the mode tabs for this link (per-URL, no server flag) |

Combine freely:

```
https://host/?sub=https://example.com/sub.txt&full=1&run=1
```

fills the URL, enables the full suite, and starts testing immediately.

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

### `POST /api/sub`

Tests a subscription or proxy list. Body:

```json
{ "sub_url": "https://example.com/sub.txt", "text": "vless://…\nvmess://…",
  "full": false, "submit": false }
```

`sub_url` or `text` (one required). `full` runs the heavy check suite;
`submit` shares anonymous, PII-stripped results to the central server and
returns a private link. Streams SSE events:

| Event | Data |
|---|---|
| `baseline` | No-proxy baseline metrics (JSON) |
| `row` | One proxy×core result (JSON) as each finishes |
| `link` | Private results URL (only when `submit:true`) |
| `done` | Empty — stream ends |

## Concurrent runs

The server blocks a second run for the same example directory while one is
in progress (returns `error: already running`). Different examples can run
in parallel.
