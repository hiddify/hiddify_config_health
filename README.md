# hiddify-config-health

Automated testing tool for VPN/proxy configuration files.
Starts real proxy core processes, runs connectivity checks through them, and reports results — locally or on remote servers via SSH.

## Features

- **Multi-core**: sing-box, xray, hiddify-core (add new cores with ~10 lines of Go)
- **Template engine**: Pongo2/Jinja2 syntax + JSON5 (comments, trailing commas)
- **Auto vars**: `"auto"` → random free port / UUID / password at runtime
- **TLS auto-gen**: self-signed CA + leaf cert injected as `{{TLS_CERT}}` etc.
- **SSH deploy**: run server on remote host, client locally — test real paths
- **Multi-hop**: chain server → relay → client with `topology` in `run.json`
- **Health checks**: DNS, TCP-DNS, HTTP, QUIC, ping, download, upload
- **Protocol detection**: passive censor-view fingerprint (opaque / leaking / blocked)
- **Web UI**: live log stream via SSE, per-check results, history sparkline
- **History**: every run stored in SQLite (`~/.hiddify-health/results.db`)
- **CI-friendly**: `run-all` exits 1 on any failure

## Quickstart

```bash
git clone https://github.com/hiddify/hiddify_config_health
cd hiddify_config_health
go build -o hiddify-health ./cmd

# Run one example (sing-box must be in PATH or SINGBOX_BIN set)
./hiddify-health run examples/sing-box/shadowsocks

# Run all examples
./hiddify-health run-all examples/

# Web UI
./hiddify-health serve --addr :8080
```

## Example structure

```
examples/
├── sing-box/
│   ├── shadowsocks/
│   │   ├── server.json    ← JSON5 template with {{ PORT }}, {{ PASSWORD }}, …
│   │   ├── client.json
│   │   └── run.json       ← core, vars, checks, deploy, tls
│   └── vless-xhttp/
└── xray/
    └── vless-xhttp/
```

Template syntax (JSON5 + Pongo2/Jinja2):

```json5
{
  // comments supported
  "listen_port": {{ PORT }},           # auto-assigned free port
  "password":    "{{ PASSWORD }}",     # auto random hex
  {% if TLS_CERT %}
  "tls": {
    "certificate_path": "{{ TLS_CERT }}",
    "key_path":         "{{ TLS_KEY }}",
  },
  {% endif %}
  "trailing": "comma ok",
}
```

## CLI

```
hiddify-health run       <dir>        run one example
hiddify-health run-all   [dir]        run all, exit 1 if any fail
hiddify-health check     <dir>        validate config syntax only
hiddify-health serve     [--addr]     start web UI (default :8080)
hiddify-health history   [dir]        show run history

Global: --examples <dir>  --db <path>  --timeout <sec>
```

## Environment

| Var | Description |
|---|---|
| `SINGBOX_BIN` | Path to sing-box binary |
| `XRAY_BIN` | Path to xray binary |
| `HIDDIFY_BIN` | Path to hiddify-core binary |

## Documentation

See [`docs/`](docs/) for full reference:
[quickstart](docs/quickstart.md) · [run.json](docs/run-json.md) · [templates](docs/templates.md) · [placeholders](docs/placeholders.md) · [cores](docs/cores.md) · [health checks](docs/health-checks.md) · [TLS](docs/tls.md) · [SSH deploy](docs/ssh-deploy.md) · [topology](docs/topology.md) · [web UI](docs/web-ui.md) · [history](docs/history.md) · [detect](docs/detect.md) · [CLI](docs/cli.md)
