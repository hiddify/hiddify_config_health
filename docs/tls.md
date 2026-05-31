# Auto-generated TLS Certificates

Setting `"tls": true` in `run.json` tells the runner to generate a fresh
self-signed CA and leaf certificate before starting any process.

## What gets generated

```
temp dir (per run)/
├── ca.pem    ← self-signed CA (ECDSA P-256, 90-day validity)
├── cert.pem  ← leaf cert signed by CA (SANs: HOST_NAME, SNI_NAME, 127.0.0.1, localhost)
└── key.pem   ← leaf private key (ECDSA P-256)
```

The temp directory is deleted when the test finishes (deferred cleanup).

## Injected placeholders

| Placeholder | Value |
|---|---|
| `{{TLS_CERT}}` | Absolute path to `cert.pem` |
| `{{TLS_KEY}}` | Absolute path to `key.pem` |
| `{{TLS_CA}}` | Absolute path to `ca.pem` |
| `{{CA_FINGERPRINT}}` | SHA-256 hex digest of the CA's DER bytes (64 hex chars) |

## Server config example (sing-box)

```json
"tls": {
  "enabled": true,
  "certificate_path": "{{TLS_CERT}}",
  "key_path": "{{TLS_KEY}}"
}
```

## Client config example (sing-box — trust generated CA)

```json
"tls": {
  "enabled": true,
  "server_name": "{{SNI_NAME}}",
  "certificate_path": "{{TLS_CA}}"
}
```

## Client config example (xray — trust generated CA)

```json
"tlsSettings": {
  "serverName": "{{SNI_NAME}}",
  "allowInsecure": false,
  "certificates": [{"certificateFile": "{{TLS_CA}}"}]
}
```

## Using the CA fingerprint (REALITY / pinning)

Some protocols accept a certificate fingerprint instead of a file path:

```json
"tls": {
  "enabled": true,
  "server_name": "{{SNI_NAME}}",
  "fingerprint": "{{CA_FINGERPRINT}}"
}
```

## Disabling TLS auto-gen

Leave `"tls": false` (default). Provide cert paths manually in `vars`:

```json
"tls": false,
"vars": {
  "TLS_CERT": "/etc/ssl/my-cert.pem",
  "TLS_KEY":  "/etc/ssl/my-key.pem",
  "TLS_CA":   "/etc/ssl/my-ca.pem"
}
```

## SANs (Subject Alternative Names)

The generated leaf cert includes SANs derived from:
- `HOST_NAME` var (if set)
- `SNI_NAME` var (if set)
- `127.0.0.1` (always)
- `localhost` (always)

If your server listens on a public IP, add it to `vars` as `SERVER`
and the runner currently does **not** auto-add it to the SAN list —
add it explicitly to the client TLS `insecure` setting or open an issue.
