## TODO: test hiddify-sing-box + Firebase-Tunnel via hiddify_config_health

Repo: https://github.com/Hiddify2/Firebase-Tunnel

Plan:
1. Clone Firebase-Tunnel, build `fb-tunnel-client`/`fb-tunnel-server`.
2. Get real Firebase Realtime Database project creds (`firebase_url`, `firebase_secret`) — needed in both `client.toml` and `server.toml`. Blocked without these.
3. Run `fb-tunnel-server` (needs real internet egress) and `fb-tunnel-client` (exposes local SOCKS5 at `127.0.0.1:1080` by default).
4. Point a hiddify-sing-box outbound (`type: socks`, `server: 127.0.0.1`, `server_port: 1080`) at the tunnel client.
5. Add a `hiddify_config_health` example dir (e.g. `examples/sing-box/firebase-tunnel/`) with `server.json`/`client.json`/`run.json` wiring the sing-box config through that SOCKS5 outbound, then `./hiddify-health run examples/sing-box/firebase-tunnel`.

Blocked on: Firebase project credentials.
