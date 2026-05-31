# Placeholders

Config templates use `{{KEY}}` syntax. The runner replaces every occurrence
before writing the rendered config to a temp file. JSON comments are stripped
and integers are written without quotes — make sure your template omits quotes
around numeric placeholders:

```json
{"listen_port": {{PORT}}}       ✓  integer
{"listen_port": "{{PORT}}"}     ✗  string — most cores reject this
```

## Standard placeholders

| Placeholder | Description | `"auto"` resolution |
|---|---|---|
| `{{SERVER}}` | Server IP / hostname | — (set explicitly) |
| `{{PORT}}` | Server listen port | Random free TCP port |
| `{{SOCKS_PORT}}` | Client SOCKS5 listen port | Random free TCP port |
| `{{UUID}}` | VLESS / VMess user UUID | `uuid.New()` |
| `{{PASSWORD}}` | Shadowsocks / Trojan password | 16-byte random hex |
| `{{HOST_NAME}}` | HTTP Host header / xHTTP host | — |
| `{{SNI_NAME}}` | TLS SNI server name | — |
| `{{UPSTREAM_SERVER}}` | Server address from previous topology node | Propagated automatically |
| `{{UPSTREAM_PORT}}` | Port from previous topology node | Propagated automatically |

## TLS placeholders (requires `"tls": true` in run.json)

| Placeholder | Value injected |
|---|---|
| `{{TLS_CERT}}` | Absolute path to leaf `cert.pem` |
| `{{TLS_KEY}}` | Absolute path to leaf `key.pem` |
| `{{TLS_CA}}` | Absolute path to CA `ca.pem` |
| `{{CA_FINGERPRINT}}` | SHA-256 hex of CA DER (64 chars, no colons) |

Example server config using TLS placeholders (sing-box):

```json
"tls": {
  "enabled": true,
  "certificate_path": "{{TLS_CERT}}",
  "key_path": "{{TLS_KEY}}"
}
```

Example client config trusting the generated CA:

```json
"tls": {
  "enabled": true,
  "server_name": "{{SNI_NAME}}",
  "certificate_path": "{{TLS_CA}}"
}
```

## Adding custom placeholders

Any key added to `vars` in `run.json` becomes available as `{{KEY}}`:

```json
"vars": {
  "OBFS_HOST": "www.bing.com",
  "REALITY_DEST": "www.google.com:443"
}
```

```json
"obfs": {"type": "http", "host": "{{OBFS_HOST}}"}
```

Keys are case-sensitive. Placeholders with no matching var entry are left
verbatim in the rendered config (useful for spotting misconfigured templates).

## Resolution order

1. Explicit value in `vars` (highest priority).
2. `"auto"` → resolved at runtime.
3. TLS vars injected when `"tls": true`.
4. `{{UPSTREAM_SERVER}}` / `{{UPSTREAM_PORT}}` injected from topology chain.
5. Unknown key → placeholder left unchanged.
