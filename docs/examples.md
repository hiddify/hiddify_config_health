# Writing Examples

## Directory layout

```
examples/
└── <core>/                      # sing-box | xray | hiddify-core
    └── <protocol-name>/
        ├── server.json          # server-side config template
        ├── client.json          # client-side config template
        └── run.json             # test metadata
```

Each leaf directory that contains a `run.json` is treated as one testable
example. The `run-all` command recursively scans the examples root for all
such directories.

## Minimal example

```
examples/
└── sing-box/
    └── trojan/
        ├── server.json
        ├── client.json
        └── run.json
```

`run.json`:

```json
{
  "name": "Trojan (sing-box)",
  "core": "sing-box",
  "server_config": "server.json",
  "client_config": "client.json",
  "vars": {
    "SERVER":     "127.0.0.1",
    "PORT":       "auto",
    "SOCKS_PORT": "auto",
    "PASSWORD":   "auto"
  },
  "checks": ["dns", "http", "quic"],
  "timeout_sec": 30
}
```

`server.json`:

```json
{
  "inbounds": [{
    "type": "trojan",
    "tag": "trojan-in",
    "listen": "{{SERVER}}",
    "listen_port": {{PORT}},
    "users": [{"password": "{{PASSWORD}}"}],
    "tls": {
      "enabled": true,
      "certificate_path": "{{TLS_CERT}}",
      "key_path": "{{TLS_KEY}}"
    }
  }],
  "outbounds": [{"type": "direct", "tag": "direct"}]
}
```

`client.json`:

```json
{
  "inbounds": [{
    "type": "mixed",
    "listen": "127.0.0.1",
    "listen_port": {{SOCKS_PORT}}
  }],
  "outbounds": [{
    "type": "trojan",
    "server": "{{SERVER}}",
    "server_port": {{PORT}},
    "password": "{{PASSWORD}}",
    "tls": {
      "enabled": true,
      "server_name": "{{SNI_NAME}}",
      "certificate_path": "{{TLS_CA}}"
    }
  }]
}
```

Add `"tls": true` to `run.json` and the cert placeholders are auto-filled.

## Naming conventions

- Use lowercase hyphenated directory names: `vless-xhttp`, `shadowsocks-obfs`, `wireguard`.
- `run.json` `name` field is the human label — make it descriptive:
  `"Shadowsocks chacha20 + HTTP obfs (sing-box)"`.
- Put variants in sibling directories, not in separate top-level trees:
  ```
  sing-box/
    shadowsocks-plain/
    shadowsocks-http-obfs/
    shadowsocks-v2ray-plugin/
  ```

## Porting from examples.zip

The bundled bash scripts in `examples.zip` use shell variable substitution
(`${SERVER}`, `${CONFIG_TCP_PORT}`, etc.). The mapping to Go placeholders is:

| Shell var | Placeholder |
|---|---|
| `${SERVER}` | `{{SERVER}}` |
| `${CONFIG_TCP_PORT}` | `{{PORT}}` |
| `${SOCKS_PORT}` | `{{SOCKS_PORT}}` |
| `${UUID}` | `{{UUID}}` |
| `${PASSWORD}` | `{{PASSWORD}}` |
| `${HOST_NAME}` | `{{HOST_NAME}}` |
| `${SNI_NAME}` | `{{SNI_NAME}}` |

Strip the `add_default_items_to_config` jq calls — the runner adds SOCKS
inbound via the client config template directly.
