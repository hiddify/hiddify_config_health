# Health Checks

Health checks run through the client's SOCKS5 proxy after it becomes ready.
All checks share the same `timeout_sec` from `run.json`.

## Available checks

| Check name | Protocol | What it tests |
|---|---|---|
| `dns` | UDP DNS | Resolves `DNSTarget` (default `google.com`) via `DNSServer` (default `1.1.1.1:53`) |
| `tcp-dns` | TCP DNS | Same as `dns` but forces TCP transport |
| `http` | HTTP HEAD | `HEAD` request to `HTTPTarget` (default `http://connectivitycheck.gstatic.com/generate_204`). Success = 2xx/3xx response. |
| `quic` | QUIC TLS handshake | Connects to `1.1.1.1:443` over UDP via the proxy and completes a QUIC handshake |
| `ping` | TCP connect | Measures TCP connect latency to `PingTarget` (default `1.1.1.1:443`) N times. Reports avg/min/max. |
| `download` | HTTPS GET | Downloads from `DownloadURL` (default Cloudflare speed test) and measures throughput (MB/s) |
| `upload` | HTTPS POST | Uploads a payload to `UploadURL` and measures throughput |
| `speedtest` | — | Alias: expands to `download` + `upload` + `ping` |

## run.json check list examples

```json
"checks": ["dns", "http"]                       // minimal — fast
"checks": ["dns", "http", "quic"]               // also verifies QUIC/UDP forwarding
"checks": ["dns", "http", "quic", "speedtest"]  // full test incl. throughput
"checks": ["ping"]                              // latency-only benchmark
```

## Per-check result fields

Each check produces a `Result` with:

| Field | Type | Description |
|---|---|---|
| `Name` | string | Check name (`dns`, `http`, …) |
| `OK` | bool | `true` = passed |
| `Duration` | duration | Wall time for the check |
| `Extra` | string | Human summary: `throughput=12.50MB/s`, `avg=38ms min=31ms max=55ms` |
| `Err` | error | Non-nil on failure |
| `PingAvg/Min/Max` | duration | Populated by `ping` check |
| `Jitter` | duration | Standard deviation of ping samples |
| `Throughput` | float64 | Bytes per second (download/upload checks) |

## Customising check targets

Override via `run.json` vars or directly by modifying `Config` in Go:

```json
"vars": {
  "DNS_SERVER": "8.8.8.8:53",
  "DNS_TARGET": "cloudflare.com"
}
```

> Note: The current runner wires `DNSServer`/`DNSTarget` from `health.Config`
> defaults. Custom DNS targets via vars require wiring in `runner.go` — see
> the `hcfg` struct construction.

## Timeouts

Each check independently respects `timeout_sec`. A slow proxy that takes
28 s for HTTP but passes is still counted as PASS. Increase `timeout_sec`
for links with high latency (e.g. satellite).

## What check failure means

| Failure | Likely cause |
|---|---|
| `dns` fails, `http` passes | DNS is blocked; proxy is doing its own resolution |
| `quic` fails, others pass | UDP not forwarded; firewall drops UDP, or proxy doesn't support UDP |
| All fail | Proxy didn't start, config syntax error, wrong port |
| `download` slow | Proxy overhead, server bandwidth capped, or encoding adds overhead |
