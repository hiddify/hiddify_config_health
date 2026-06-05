# run.json Reference

Every example directory must contain a `run.json` file. It controls what core
to use, which config files to render, what vars to substitute, and which health
checks to run.

`run.json` files are **inherited**: hiddify-health walks from the example dir
up to the examples root, loading each ancestor `run.json`. Child values always
override parent values. This means binary paths and common vars (SERVER, PORT,
UUID…) can live in a shared parent file. See the [Inheritance](#inheritance) section.

JSON5 syntax is supported in all `run.json` files: `//` and `#` comments,
`/* block comments */`, and trailing commas.

---

## Full schema

```json5
{
  // Human-readable label (web UI + CLI)
  "name": "VLESS + xHTTP (xray)",

  // Core: "sing-box" | "xray" | "hiddify-core"
  // Ignored when client_process_path is set.
  "core": "sing-box",

  // Binary path overrides (take priority over core registry).
  // Use "env.VAR_NAME" to read from an environment variable at runtime.
  "client_process_path": "env.XRAY_CLIENT_PATH",
  "server_process_path": "env.XRAY_SERVER_PATH", // defaults to client_process_path
  "client_arg": "run -c ",   // args passed before the config file path
  "server_arg": "run -c ",   // defaults to client_arg

  // Config template paths relative to this run.json directory.
  // Extension fallback: .j2 → .tpl → .json (first match wins).
  "server_config": "server.json",
  "client_config": "client.json",

  // Multi-hop topology (replaces server_config/client_config when set).
  "topology": [],

  // Auto-generate a self-signed TLS bundle and inject TLS_CERT / TLS_KEY /
  // TLS_CA / CA_FINGERPRINT placeholders.
  "tls": false,

  // vars: EITHER a single object (one run) OR an array of objects (one run
  // per entry — multi-variant mode).
  //
  // Single object:
  "vars": {
    "SERVER":     "127.0.0.1",
    "PORT":       "auto",
    "SOCKS_PORT": "auto",
    "UUID":       "auto",
    "PASSWORD":   "auto",
    "HOST_NAME":  "example.com",
    "SNI_NAME":   "example.com"
  },

  // SSH deploy: copy server binary+config to remote and exec there.
  "deploy_to_server": "",  // e.g. "ssh://root@203.0.113.10:22"

  // Health checks to run after the client SOCKS proxy is ready.
  "checks": ["dns", "http", "quic"],

  // Shell commands executed before any process starts / after all stop.
  "before_start": [],
  "after_stop":   [],

  // Per-check timeout in seconds (default 30).
  "timeout_sec": 30
}
```

---

## Fields

### `name` (string)
Label shown in CLI output and the web UI sidebar. Defaults to directory name.

### `core` (string)
Core name: `sing-box`, `xray`, `hiddify-core`. Ignored when
`client_process_path` is set.

### `client_process_path` / `server_process_path` (string)
Explicit path to the core binary. Overrides `core` registry lookup.

Supports `"env.VAR_NAME"` — resolved to `os.Getenv("VAR_NAME")` at runtime:

```json5
"client_process_path": "env.XRAY_CLIENT_PATH",   // reads $XRAY_CLIENT_PATH
"client_process_path": "/usr/local/bin/xray"       // literal path also works
```

`server_process_path` defaults to `client_process_path` when unset.

### `client_arg` / `server_arg` (string)
Arguments passed to the binary **before** the config file path.
Space-separated; the config path is appended by the runner:

```json5
"client_arg": "run -c "   // → xray run -c /tmp/hch-client-*.json
"client_arg": "srun -c "  // hiddify-core
```

`server_arg` defaults to `client_arg`.

### `server_config` / `client_config` (string)
Paths relative to the `run.json` directory. Extension fallback order:
`.j2` → `.tpl` → `.json` (first existing match wins).

If `templates/base/<role>.json.j2` exists in a parent directory, it is
rendered and deep-merged with the protocol template (base = defaults,
protocol = overrides). See [templates.md](templates.md#base-template-composition).

### `topology` (array)
Multi-hop chain. When non-empty, replaces `server_config`/`client_config`.
See [topology.md](topology.md).

### `tls` (bool, default `false`)
Auto-generates a self-signed CA + leaf cert before starting processes.
Injects `{{TLS_CERT}}`, `{{TLS_KEY}}`, `{{TLS_CA}}`, `{{CA_FINGERPRINT}}`.
See [tls.md](tls.md).

---

## `vars` — single vs multi-variant

### Single run (object)

```json5
"vars": {
  "SERVER": "127.0.0.1",
  "PORT":   "auto"
}
```

Runs the example once.

### Multi-variant (array)

```json5
"vars": [
  {
    "TITLE":      "plain-tls",   // variant label in output
    "TLS":        "1",
    "VLESS_ENC":  "none",
    "VLESS_FLOW": ""
  },
  {
    "TITLE":      "xtls-vision",
    "TLS":        "1",
    "VLESS_ENC":  "none",
    "VLESS_FLOW": "xtls-rprx-vision"
  }
]
```

Runs the example **once per array entry**. Each variant reports independently
in CLI output and history. `TITLE` is the variant label; it is **not** passed
to the template.

`bool` values (`true`/`false`) are converted to `"1"`/`"0"` automatically so
`{% if TLS %}` works in templates.

### Auto-resolution

Use `{{AUTO_*}}` placeholders as var values to trigger runtime resolution:

| Value | Resolution |
|---|---|
| `{{AUTO_PORT}}` | Random free TCP port |
| `{{AUTO_TCP_PORT}}` | Random free TCP port |
| `{{AUTO_UDP_PORT}}` | Random free TCP port |
| `{{AUTO_QUIC_PORT}}` | Random free TCP port |
| `{{AUTO_SOCKS_PORT}}` | Random free TCP port |
| `{{AUTO_UPSTREAM_PORT}}` | Random free TCP port |
| `{{AUTO_UUID}}` | `uuid.New()` v4 |
| `{{AUTO_PASSWORD}}` | 16 random bytes, hex-encoded |

Unlike the old `"auto"` string, these placeholders are unambiguous — a config
value that legitimately contains the word `"auto"` is left untouched.

Full placeholder reference: [placeholders.md](placeholders.md).

---

## `deploy_to_server` (string)

SSH URL for remote server deployment: `ssh://user@host:port`.
Empty = run locally. See [ssh-deploy.md](ssh-deploy.md).

---

## `checks` (array of strings)

Built-in checks:

| Value | What runs |
|---|---|
| `dns` | UDP DNS query via the proxy |
| `tcp-dns` | TCP DNS query via the proxy |
| `http` | HTTP HEAD request via the proxy |
| `quic` | QUIC TLS handshake via the proxy |
| `ping` | TCP connect latency (N samples) |
| `download` | Download throughput |
| `upload` | Upload throughput |
| `speedtest` | Alias: `download` + `upload` + `ping` |

**Custom executable check** — any path that is not a built-in name is
executed as a binary. Exit code 0 = PASS, non-zero = FAIL.

```json5
"checks": [
  "dns", "http",
  // "./my-tester.sh",          // uncomment to enable custom check
  // "/usr/local/bin/my-check"  // absolute path also works
]
```

The binary receives these env vars:

| Var | Value |
|---|---|
| `HCH_PROXY_ADDR` | `socks5://host:port` (the client SOCKS proxy) |
| `HCH_TIMEOUT` | timeout in seconds |

Stdout/stderr from the binary appears in the result `Extra` field (truncated to 200 chars).

Default: `["dns", "http"]`.

---

## `strip_json5` (bool, default `true`)

When `true` (default), JSON5 extensions (comments, trailing commas) are stripped
from rendered config files before passing them to the core binary.

Set to `false` for cores that natively accept JSON5, or to debug rendered output:

```json5
{
  "strip_json5": false
}
```

---

## `before_start` / `after_stop` (array of strings)

Shell commands (`sh -c`). `before_start` runs before any process starts;
`after_stop` runs after all processes stop. Failures are logged but non-fatal.

---

## Inheritance

hiddify-health walks parent directories looking for `run.json` files. Fields
are merged **root → child** (child wins if non-empty):

```
examples/run.json           ← common vars: SERVER, PORT, SOCKS_PORT, UUID
examples/xray/run.json      ← binary paths: client_process_path, client_arg
examples/xray/vless-xhttp/run.json  ← protocol: name, server_config, client_config, variants
```

Variant vars: ancestor variant maps are flattened into a base-vars map and
injected as defaults into each child variant. Child variant values always win.

Example: if root `run.json` has `SERVER: 127.0.0.1` and the child variant
doesn't set `SERVER`, the child inherits `127.0.0.1`.

The walk stops when no `run.json` is found in the parent directory.

---

## `timeout_sec` (int, default `30`)

Per-check timeout in seconds. Applied independently to each health check.
