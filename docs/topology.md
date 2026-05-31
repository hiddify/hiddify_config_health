# Multi-hop Topology

The `topology` array in `run.json` replaces the flat `server_config` /
`client_config` pair and lets you describe chains: server → relay → client.

## Schema

```json
"topology": [
  {
    "role":   "server",
    "core":   "sing-box",
    "config": "server.json",
    "host":   ""
  },
  {
    "role":   "relay",
    "core":   "xray",
    "config": "relay.json",
    "host":   "ssh://root@relay-host:22"
  },
  {
    "role":   "client",
    "core":   "sing-box",
    "config": "client.json",
    "host":   ""
  }
]
```

| Field | Description |
|---|---|
| `role` | `"server"`, `"relay"`, or `"client"`. Arbitrary strings are accepted — only `"client"` has special handling (SOCKS port). |
| `core` | Core name. Defaults to the top-level `core` field when omitted. |
| `config` | Config template path relative to `run.json`. |
| `host` | Optional `ssh://user@host:port` for remote nodes. Empty = local. |

## Start order

Nodes start **in array order** (server first, client last). The runner waits
for the client's SOCKS port to become reachable before running health checks.

## Placeholder chaining

After each node's config is rendered, the runner propagates resolved ports
forward so the next node can reference them:

| Injected var | Source |
|---|---|
| `{{UPSTREAM_SERVER}}` | `{{SERVER}}` resolved by the previous node |
| `{{UPSTREAM_PORT}}` | `{{PORT}}` resolved by the previous node |

**Example:** 3-hop chain

`server.json` — listens on `{{PORT}}` (auto-assigned, e.g. 54321):
```json
{"inbounds": [{"listen_port": {{PORT}}}]}
```

`relay.json` — forwards to the server's resolved port:
```json
{
  "outbounds": [{"server": "{{UPSTREAM_SERVER}}", "server_port": {{UPSTREAM_PORT}}}],
  "inbounds":  [{"listen_port": {{PORT}}}]
}
```

`client.json` — forwards to the relay's resolved port:
```json
{
  "outbounds": [{"server": "{{UPSTREAM_SERVER}}", "server_port": {{UPSTREAM_PORT}}}],
  "inbounds":  [{"listen_port": {{SOCKS_PORT}}}]
}
```

## Remote relay example

```json
{
  "name": "Shadowsocks → xHTTP relay (double-hop)",
  "core": "sing-box",
  "tls": true,
  "topology": [
    {
      "role":   "server",
      "config": "server.json",
      "host":   "ssh://root@origin-server"
    },
    {
      "role":   "relay",
      "core":   "xray",
      "config": "relay.json",
      "host":   "ssh://root@relay-server"
    },
    {
      "role":   "client",
      "config": "client.json"
    }
  ],
  "vars": {
    "SERVER":       "origin-ip",
    "RELAY_SERVER": "relay-ip",
    "PORT":         "auto",
    "SOCKS_PORT":   "auto",
    "UUID":         "auto"
  },
  "checks": ["dns", "http", "quic"]
}
```

## Cleanup

Nodes are stopped in **reverse order** (client first, server last) via the
deferred stop functions registered during startup. Remote processes are killed
via `pkill` over SSH.
