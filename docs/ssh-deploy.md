# SSH Deployment

When `deploy.url` is set in `run.json`, the server process runs on a remote
machine. The client still runs locally and connects to the remote via the
proxy under test.

## run.json deploy block

```json
"deploy": {
  "url":        "ssh://root@203.0.113.10:22",
  "remote_dir": "/tmp/hch"
}
```

| Field | Default | Description |
|---|---|---|
| `url` | `""` (local) | `ssh://[user@]host[:port]`. Port defaults to 22. |
| `remote_dir` | `/tmp/hch` | Directory created on the remote for configs and logs. |

## Authentication

Order of attempts:

1. **SSH agent** — if `SSH_AUTH_SOCK` is set, the system agent is queried.
2. **Key files** — `~/.ssh/id_ed25519`, `~/.ssh/id_rsa`, `~/.ssh/id_ecdsa`
   (in that order; first one that loads wins).
3. Known hosts — read from `~/.ssh/known_hosts`. Falls back to
   `InsecureIgnoreHostKey` if the file doesn't exist (logs a warning).

Password authentication is not supported. Add your public key to the remote's
`~/.ssh/authorized_keys` before running.

## What the runner does

```
1. Render server.json template locally (with auto-resolved vars)
2. SFTP upload: rendered server.json → <remote_dir>/server.json
3. SSH exec: nohup <core> run -c <remote_dir>/server.json &
4. Start client locally with {{SERVER}} = remote host IP
5. Run health checks through local client SOCKS proxy
6. SSH exec: pkill -f '<core>'   (cleanup)
```

The `{{SERVER}}` placeholder in the **client** config is **not** automatically
replaced with the remote IP — you must set it explicitly:

```json
"vars": {
  "SERVER": "203.0.113.10"
}
```

Or leave it as `"127.0.0.1"` for local-only tests.

## Example: remote Shadowsocks test

```json
{
  "name": "Shadowsocks — remote server",
  "core": "sing-box",
  "server_config": "server.json",
  "client_config": "client.json",
  "vars": {
    "SERVER":     "203.0.113.10",
    "PORT":       "auto",
    "SOCKS_PORT": "auto",
    "PASSWORD":   "auto"
  },
  "deploy": {
    "url": "ssh://root@203.0.113.10:22"
  },
  "checks": ["dns", "http", "quic", "speedtest"]
}
```

The sing-box binary must already be installed on the remote.

## Topology with mixed local/remote nodes

In `topology` mode, individual nodes can be remote:

```json
"topology": [
  {"role": "server", "core": "sing-box", "config": "server.json",
   "host": "ssh://root@203.0.113.10"},
  {"role": "client", "core": "sing-box", "config": "client.json"}
]
```

Each remote node gets its own SSH session. See [topology.md](topology.md).

## Troubleshooting

| Problem | Fix |
|---|---|
| `ssh dial: connection refused` | Check firewall; SSH port open? |
| `no authentication methods available` | Add key to `~/.ssh/` or start `ssh-agent` |
| `sftp: permission denied` on upload | Check `remote_dir` write permission |
| Server starts but client can't connect | Firewall blocking proxy port; or `SERVER` var is still `127.0.0.1` |
| Process lingers after test | `pkill` uses core name — ensure the binary name matches |
