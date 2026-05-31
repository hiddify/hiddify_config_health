# Protocol Detection (Censor View)

After health checks complete, the runner derives a `TrafficFingerprint` that
describes how the traffic looks to an outside observer. This is a **passive**
analysis — it infers from the health check results without capturing packets.

## Fingerprint fields

| Field | Type | Description |
|---|---|---|
| `Verdict` | string | One-word summary: `opaque`, `recognizable`, `leaking`, `blocked` |
| `EntropyScore` | float64 | 0.0–1.0. Fraction of checks that passed. Higher = harder to block. |
| `LooksLikeHTTP` | bool | HTTP check passed (proxy didn't break HTTP) |
| `LooksLikeQUIC` | bool | QUIC check passed (UDP traffic flows through) |
| `HasDNSLeak` | bool | DNS check **failed** but HTTP check **passed** — proxy may be leaking DNS |
| `SpeedAboveMBps` | bool | Download throughput > 1 MB/s |

## Verdicts

| Verdict | Meaning |
|---|---|
| `opaque` | ≥75% of checks passed; no DNS leak. Censor has little to act on. |
| `recognizable` | Some checks passed but fingerprint is identifiable (e.g. plaintext HTTP patterns). |
| `leaking` | DNS leak detected — real destinations may be visible to the network. |
| `blocked` | Both HTTP and QUIC failed. Proxy likely completely blocked. |

## Web UI badge

The results panel shows a coloured verdict badge:

- 🟢 `opaque` — green
- 🔵 `recognizable` — blue
- 🟡 `leaking` — yellow
- 🔴 `blocked` — red

## Limitations

Passive analysis only. It cannot detect:
- Active probing by a censor (sending crafted packets to the server port)
- TLS fingerprinting (JA3 hash) — requires packet capture
- Traffic-analysis attacks (inter-packet timing, packet sizes)

For deeper analysis, integrate a packet capture tool:

```bash
# Capture during a test (Linux)
tcpdump -i lo -w /tmp/test.pcap &
./hiddify-health run examples/sing-box/shadowsocks
kill %1
# Analyse with Wireshark / tshark
```

Active PCAP analysis via `gopacket` is planned as an optional Linux feature
(requires `tcpdump` in `PATH` and `--detect` flag).
