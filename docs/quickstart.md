# Quickstart

## Prerequisites

- Go 1.21+
- At least one proxy core binary (sing-box, xray, or hiddify-core) in `PATH`
  or pointed to by an env var (see [cores.md](cores.md))

## Build

```bash
git clone https://github.com/hiddify/hiddify_config_health
cd hiddify_config_health
go build -o hiddify-health ./cmd
```

## Run your first test (CLI)

```bash
# Shadowsocks via sing-box — all ports/passwords auto-assigned
./hiddify-health run examples/sing-box/shadowsocks
```

Output:

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

Exit code 0 = all checks passed. Exit code 1 = at least one failed.

## Run all examples (CI mode)

```bash
./hiddify-health run-all examples/
echo $?    # 0 = all pass
```

## Web UI

```bash
./hiddify-health serve --addr :8080
# Open http://localhost:8080
```

- Left panel: list of examples with last pass/fail badge.
- Click an example → click **▶ Run** → watch live log stream.
- Right panel: per-check results table + protocol fingerprint badge + history.

## Specifying core binaries

```bash
# Environment variable override (takes priority over PATH)
SINGBOX_BIN=/opt/homebrew/bin/sing-box ./hiddify-health run examples/sing-box/shadowsocks
XRAY_BIN=/usr/local/bin/xray ./hiddify-health run examples/xray/vless-xhttp
HIDDIFY_BIN=/usr/local/bin/hiddify-core ./hiddify-health run examples/hiddify-core/...
```

## Global flags

| Flag | Default | Description |
|---|---|---|
| `--examples` | `examples` | Root directory scanned for `run.json` files |
| `--db` | `~/.hiddify-health/results.db` | SQLite history database |
| `--timeout` | `30` | Per-check timeout (seconds) |
