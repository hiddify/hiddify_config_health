# CLI Reference

## Global flags

Available on every subcommand.

| Flag | Default | Description |
|---|---|---|
| `--examples <dir>` | `examples` | Root directory scanned for `run.json` files |
| `--db <path>` | `~/.hiddify-health/results.db` | SQLite history database path |
| `--timeout <sec>` | `30` | Per-check timeout in seconds |

---

## `hiddify-health run <example-dir>`

Runs one example end-to-end and prints results.

```bash
./hiddify-health run examples/sing-box/shadowsocks
./hiddify-health run examples/xray/vless-xhttp
```

Output format:
```
▶ examples/sing-box/shadowsocks
[core] starting sing-box (sing-box version 1.11.0) role=server
[core] starting sing-box (sing-box version 1.11.0) role=client
[wait] SOCKS proxy at 127.0.0.1:54321
[wait] SOCKS ready
[check] dns        PASS duration=38ms
[check] http       PASS duration=210ms
[check] quic       PASS duration=185ms
  PASS  duration=450ms  censor=opaque
```

Exit code: `0` = pass, `1` = fail or error.

---

## `hiddify-health run-all [examples-dir]`

Runs every example found under `examples-dir` (default: `--examples` value).
Useful for CI.

```bash
# Run everything
./hiddify-health run-all

# Specify a subdirectory
./hiddify-health run-all examples/sing-box

# CI: fail the pipeline if any test fails
./hiddify-health run-all examples/ || exit 1
```

Summary at the end:
```
--- 3 passed  1 failed ---
```

Exit code: `0` = all passed, `1` = at least one failed.

---

## `hiddify-health check <example-dir>`

Validates that `run.json` exists and referenced config files are present.
Does **not** start any process or make network connections.

```bash
./hiddify-health check examples/sing-box/shadowsocks
# OK
```

Useful as a pre-flight lint step in CI before running the full test.

---

## `hiddify-health serve`

Starts the web UI.

```bash
./hiddify-health serve
./hiddify-health serve --addr 0.0.0.0:9090
```

| Flag | Default | Description |
|---|---|---|
| `--addr` | `:8080` | HTTP listen address |

Opens `http://localhost:8080`. See [web-ui.md](web-ui.md) for full details.

---

## `hiddify-health history [example-dir]`

Shows run history from the SQLite database.

```bash
# Latest result per example (all examples)
./hiddify-health history

# Last 20 runs for one example
./hiddify-health history examples/sing-box/shadowsocks
```

Output is a tab-formatted table: ID, dir, PASS/FAIL, core version, timestamp, duration.

---

## Environment variables

| Var | Description |
|---|---|
| `SINGBOX_BIN` | Path to `sing-box` binary (overrides PATH lookup) |
| `XRAY_BIN` | Path to `xray` binary |
| `HIDDIFY_BIN` | Path to `hiddify-core` binary |
| `SSH_AUTH_SOCK` | SSH agent socket (used for remote deploy auth) |

---

## Examples

```bash
# Build
go build -o hiddify-health ./cmd

# Run one test with a specific binary
SINGBOX_BIN=/opt/homebrew/bin/sing-box ./hiddify-health run examples/sing-box/shadowsocks

# Run all, save to a custom DB, 60s timeout
./hiddify-health --db /tmp/test.db --timeout 60 run-all examples/

# CI: run all, exit non-zero on failure
./hiddify-health run-all examples/ && echo "ALL PASS" || echo "SOME FAILED"

# Serve on all interfaces
./hiddify-health serve --addr 0.0.0.0:8080 --examples /data/examples
```
