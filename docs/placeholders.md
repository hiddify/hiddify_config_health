# Placeholders

Config templates use `{{KEY}}` syntax (or Pongo2's `{{ KEY }}`). The runner
resolves every placeholder before the config reaches the core process.
JSON5 comments are stripped after rendering. Integers must not be quoted:

```json5
{"listen_port": {{PORT}}}       ✓  integer
{"listen_port": "{{PORT}}"}     ✗  string — most cores reject this
```

See [templates.md](templates.md) for the full Pongo2/Jinja2 syntax reference.

---

## Network

| Placeholder | Description | `"auto"` resolution |
|---|---|---|
| `{{SERVER}}` | Server IP / hostname for outbounds | — (set explicitly) |
| `{{LISTEN_SERVER}}` | Server-side listen address (alias of `SERVER`) | Copied from `SERVER` when unset |
| `{{PORT}}` | Main server listen port | Random free TCP port |
| `{{TCP_PORT}}` | Explicit TCP port (alias of `PORT`) | Random free TCP port |
| `{{UDP_PORT}}` | UDP-specific port | Random free UDP port |
| `{{QUIC_PORT}}` | QUIC-specific port | Random free port |
| `{{SOCKS_PORT}}` | Client SOCKS5 listen port | Random free TCP port |
| `{{UPSTREAM_SERVER}}` | Previous topology node's server address | Propagated from chain |
| `{{UPSTREAM_PORT}}` | Previous topology node's port | Propagated from chain |

---

## Identity

| Placeholder | Description | `"auto"` resolution |
|---|---|---|
| `{{UUID}}` | VLESS / VMess user UUID | `uuid.New()` v4 |
| `{{PASSWORD}}` | Shadowsocks / Trojan / HTTP password | 16 random bytes, hex-encoded |

---

## TLS

| Placeholder | Description | `"auto"` resolution |
|---|---|---|
| `{{HOST_NAME}}` | HTTP Host header / xHTTP host | — (set explicitly) |
| `{{SNI_NAME}}` | TLS SNI server name | — (set explicitly) |
| `{{TLS_CERT}}` | Absolute path to leaf `cert.pem` | Injected when `"tls": true` |
| `{{TLS_KEY}}` | Absolute path to leaf `key.pem` | Injected when `"tls": true` |
| `{{TLS_CA}}` | Absolute path to CA `ca.pem` | Injected when `"tls": true` |
| `{{CA_FINGERPRINT}}` | SHA-256 hex of CA cert DER (64 chars, no colons) | Injected when `"tls": true` |

---

## Protocol-specific (VLESS / XTLS)

| Placeholder | Description | Example value |
|---|---|---|
| `{{VLESS_FLOW}}` | XTLS flow mode | `xtls-rprx-vision` or `""` |
| `{{VLESS_ENC}}` | Client-side encryption setting | `none` |
| `{{VLESS_DEC}}` | Server-side decryption setting | `none` |

---

## System

| Placeholder | Description | Default |
|---|---|---|
| `{{LOG_LEVEL}}` | Core log verbosity | `error` (set automatically if unset) |

---

## Custom placeholders

Any key in `vars` becomes a placeholder:

```json5
// run.json
"vars": {
  "OBFS_HOST":    "www.bing.com",
  "REALITY_DEST": "www.google.com:443"
}
```

```json5
// template
"obfs": {"type": "http", "host": "{{ OBFS_HOST }}"}
```

Custom keys can be uppercase or lowercase — both `{{ OBFS_HOST }}` and
`{{ obfs_host }}` resolve the same value.

---

## Resolution order

1. Explicit value in variant `vars` (highest priority)
2. Value inherited from ancestor `run.json` (parent/grandparent dirs)
3. `"auto"` → runtime resolution (port / UUID / password)
4. Alias propagation (`LISTEN_SERVER` ← `SERVER`, etc.)
5. Defaults (`LOG_LEVEL` → `"error"`)
6. TLS bundle paths injected when `"tls": true`
7. Topology chain (`UPSTREAM_SERVER` / `UPSTREAM_PORT`)
8. Unknown key → placeholder left verbatim (visible in output for debugging)
