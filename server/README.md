# health-check-server

Central ingest + dashboard service for Hiddify health-check, deployed at
**health-check.hiddify.com**.

Clients submit **anonymous, PII-stripped** result batches (opt-in). The server
stores them in Postgres, shows a global aggregate dashboard, and gives each
anonymous user a private link (`/r/<anon_id>`) to view only their own results.

## Privacy

The server only ever receives the anonymised structural *shape* of a config
(protocol, transport, security, flow/reality/alpn flags, cipher class) plus
measured metrics (score, censor verdict, latency, throughput, coarse
country/ASN). It receives **no** server IP/host, **no** uuid/password/keys,
**no** SNI/domains, **no** node tags. The `anon_id` is a random local token,
not derived from any personal data.

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/ingest` | accept `{anon_id, reports[]}` batch |
| GET | `/` | global aggregate dashboard |
| GET | `/api/aggregates` | aggregate JSON (last 7 days) |
| GET | `/r/<anon_id>` | private per-user results page |
| GET | `/api/r/<anon_id>` | private results JSON |
| GET | `/healthz` | liveness |

## Run

```bash
export DATABASE_URL="postgres://user:pass@host:5432/healthcheck?sslmode=disable"
export PUBLIC_URL="https://health-check.hiddify.com"
export ADDR=":8080"
go run .            # or: docker build -t hc . && docker run -p 8080:8080 -e DATABASE_URL=... hc
```

Without `DATABASE_URL` the server still starts (health endpoint only) so you
can smoke-test; ingest/dashboard return empty/unavailable.

## Deploy to health-check.hiddify.com

1. Provision Postgres; set `DATABASE_URL`.
2. `docker build -t health-check-server ./server && push to your registry`.
3. Run behind a TLS-terminating reverse proxy / ingress pointing the DNS
   record `health-check.hiddify.com` at the container's `:8080`.
4. Set `PUBLIC_URL=https://health-check.hiddify.com` so private links are
   correct.

The schema auto-migrates on startup (`reports` table + indexes).
