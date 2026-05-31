# run.json Reference

Every example directory must contain a `run.json` file. It controls what core
to use, which config files to render, what vars to substitute, and which health
checks to run.

## Full schema

```json
{
  "name":          "Human-readable label",
  "core":          "sing-box",
  "server_config": "server.json",
  "client_config": "client.json",

  "topology": [],

  "tls": false,

  "vars": {
    "SERVER":      "127.0.0.1",
    "PORT":        "auto",
    "SOCKS_PORT":  "auto",
    "UUID":        "auto",
    "PASSWORD":    "auto",
    "HOST_NAME":   "example.com",
    "SNI_NAME":    "example.com"
  },

  "deploy": {
    "url":        "",
    "remote_dir": "/tmp/hch"
  },

  "checks":       ["dns", "http", "quic"],
  "before_start": [],
  "after_stop":   [],
  "timeout_sec":  30
}
```

## Fields

### `name` (string)
Label shown in CLI output and the web UI sidebar. Defaults to the directory name.

### `core` (string, required)
Which proxy core runs both server and client. Valid values: `sing-box`, `xray`,
`hiddify-core`. Can be overridden per-node in `topology`.

### `server_config` / `client_config` (string)
Paths **relative to the `run.json` directory** for the server and client config
templates. Ignored when `topology` is set.

### `topology` (array of objects)
Multi-hop chain. When non-empty, replaces `server_config`/`client_config`.
See [topology.md](topology.md) for full details.

```json
"topology": [
  {"role": "server", "core": "sing-box", "config": "server.json"},
  {"role": "relay",  "core": "xray",     "config": "relay.json",  "host": "ssh://root@relay"},
  {"role": "client", "core": "sing-box", "config": "client.json"}
]
```

### `tls` (bool, default `false`)
When `true`, auto-generates a self-signed CA + leaf cert before starting
processes. Injects `{{TLS_CERT}}`, `{{TLS_KEY}}`, `{{TLS_CA}}`,
`{{CA_FINGERPRINT}}` into template vars. See [tls.md](tls.md).

### `vars` (object)
Key-value pairs substituted into config templates as `{{KEY}}`.
Special value `"auto"` triggers runtime resolution:

| Key | `"auto"` behaviour |
|---|---|
| `PORT` | Random free TCP port |
| `SOCKS_PORT` | Random free TCP port |
| `UPSTREAM_PORT` | Random free TCP port |
| `UUID` | New UUID v4 |
| `PASSWORD` | 16 random bytes, hex-encoded |

All other keys: `"auto"` is treated as an empty string (placeholder left in
place). See [placeholders.md](placeholders.md) for the full list.

### `deploy` (object)
Optional SSH deployment of the server process.
See [ssh-deploy.md](ssh-deploy.md) for setup details.

| Field | Description |
|---|---|
| `url` | `ssh://user@host:port`. Empty = run locally. |
| `remote_dir` | Working directory on the remote (default `/tmp/hch`). |

### `checks` (array of strings)
Health checks to run after the client SOCKS proxy is ready.
Valid values and their meaning are documented in [health-checks.md](health-checks.md).

Default: `["dns", "http"]`.

### `before_start` / `after_stop` (array of strings)
Shell commands executed before starting any process / after all processes stop.
Useful for firewall rules, iptables redirects, or custom setup steps.

```json
"before_start": ["iptables -I INPUT -p tcp --dport 8388 -j ACCEPT"],
"after_stop":   ["iptables -D INPUT -p tcp --dport 8388 -j ACCEPT"]
```

### `timeout_sec` (int, default `30`)
Per-check timeout in seconds. Applied to each health check independently.
