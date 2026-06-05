# Template Engine

Config files are rendered through a two-stage pipeline:

```
template source  →  Pongo2 render  →  JSON5 strip  →  valid JSON  →  core process
                         ↑                ↑
               extends/block/include   skipped when strip_json5:false
               resolves automatically
```

---

## Stage 1 — Pongo2 (Jinja2-compatible)

Templates use **Pongo2** syntax — a Go implementation of Django/Jinja2.

### Variable substitution

Both forms work:

```json5
// legacy form (auto-normalised)
"password": "{{PASSWORD}}"

// pongo2 form (preferred)
"password": "{{ PASSWORD }}"
"password": "{{ password }}"   // lowercase alias also works
```

### Filters

```json5
"port":     {{ PORT|default:"8388" }},
"method":   "{{ METHOD|default:"chacha20-ietf-poly1305" }}",
"path":     "{{ PATH|lower }}",
"name":     "{{ NAME|upper }}",
```

### Conditionals

```json5
{
  "tls": {
    "enabled": {% if TLS_CERT %}true{% else %}false{% endif %},
    {% if TLS_CERT %}
    "certificate_path": "{{ TLS_CERT }}",
    "key_path":         "{{ TLS_KEY }}"
    {% endif %}
  }
}
```

### Environment variable access

Templates can read OS environment variables via the `env` map:

```json5
"private_key": "{{ env.WG_SERVER_PRIVKEY }}",
"api_token":   "{{ env.MY_API_KEY }}",
{% if env.DEBUG %}"log_level": "debug",{% endif %}
```

Missing env vars render as empty string — no error.

### Loops

Use `|split` filter to iterate over comma-separated strings:

```json5
// run.json.j2 — auto-generate variant list
"vars": [
  {% for flow in ",xtls-rprx-vision"|split:"," %}
  {
    "TITLE":      "{% if flow %}vless-flow{% else %}plain-tls{% endif %}",
    "VLESS_FLOW": "{{ flow }}"
  },
  {% endfor %}
]
```

### Full Pongo2 filter reference

<https://github.com/flosch/pongo2?tab=readme-ov-file#filters>

| Filter | Example | Result |
|---|---|---|
| `default` | `{{ X\|default:"foo" }}` | `foo` if X empty |
| `lower` | `{{ NAME\|lower }}` | lowercase |
| `upper` | `{{ NAME\|upper }}` | UPPERCASE |
| `truncatechars` | `{{ S\|truncatechars:8 }}` | first 8 chars |
| `replace` | `{{ S\|replace:"a":"b" }}` | char replace |
| `split` | `{{ "a,b"\|split:"," }}` | list (for `{% for %}`) |

---

## Stage 2 — JSON5 extensions

After Pongo2 rendering, the output is stripped of JSON5 syntax to produce
standard JSON that proxy cores accept.

### Supported extensions

```json5
{
  // single-line comment (C++ style)
  "method": "chacha20-ietf-poly1305", // inline comment

  # single-line comment (shell / Python style)
  "password": "{{ PASSWORD }}", # inline comment

  /* block comment
     can span multiple lines */
  "network": ["tcp", "udp"],   // trailing comma OK in arrays
  "listen_port": {{ PORT }},   // trailing comma OK in objects
}
```

### Disabling JSON5 stripping

Some cores accept comments natively, or you want to debug rendered output.
Set `"strip_json5": false` in `run.json`:

```json5
{
  "strip_json5": false,   // keep // and # comments in rendered config
  ...
}
```

Default: `true` (strip — required by most proxy cores).

### What is NOT stripped (when strip_json5:true)

- `//` inside a string value: `"url": "https://example.com/path//foo"` → kept
- `#` inside a string value: `"color": "#ff0000"` → kept
- Single-quoted strings: `'value'` → normalised to `"value"`

---

## Template inheritance: `{% extends %}` + `{% block %}`

**Recommended pattern** — protocol templates extend the base template.
Pongo2 handles composition natively; no Go-side merging.

`examples/xray/templates/base/client.json.j2`:

```json5
{
  "log": {"loglevel": "{{ LOG_LEVEL }}"},
  "inbounds": [
    {"tag": "socks-in", "port": {{ SOCKS_PORT }}, "protocol": "socks"}
  ],
  "outbounds": [
    {% block outbound %}
    {"protocol": "freedom", "tag": "direct"}
    {% endblock %}
  ]
}
```

`examples/xray/vless-xhttp/client.json.j2`:

```json5
{% extends "../templates/base/client.json.j2" %}

{% block outbound %}
{
  "tag": "vless-out",
  "protocol": "vless",
  "streamSettings": {
    {% include "../templates/tls/client.tpl" %}
    "network": "xhttp"
  }
},
{"protocol": "freedom", "tag": "direct"}
{% endblock %}
```

- `{% extends %}` must be the first statement in the file
- `{% include %}` still works inside blocks
- When `{% extends %}` is detected, the Go-side deep-merge fallback is skipped

## Deep-merge fallback (without `{% extends %}`)

If a template does **not** use `{% extends %}`, the runner checks for
`templates/base/<role>.json.j2` in ancestor directories and deep-merges:
base = defaults, protocol = overrides.

**Merge rules:**
- Object keys only in base → kept
- Object keys in both → protocol wins (recursive for nested objects)
- Arrays → protocol array replaces base array entirely
- `null` in protocol → removes the key from base

The base template lookup walks up from the protocol template's directory,
checking `../templates/base/`, `../../templates/base/`, etc. (up to 4 levels).

### Partial includes

Use `{% include %}` for shared JSON fragments within a template:

```json5
// examples/xray/vless-xhttp/server.json.j2
"streamSettings": {
  {% include "../templates/tls/server.tpl" %}  // ← injects TLS block
  "network": "xhttp",
  "xhttpSettings": { "mode": "stream-up", "path": "/xhttp" }
}
```

Include paths are relative to the template file's directory. Pongo2 resolves
them correctly because `RenderFile` creates the temp normalised copy in the
same directory as the original.

---

## Auto-resolved vars

Use `{{AUTO_*}}` placeholders as var values in `run.json` and the runner fills them in:

```json5
"vars": {
  "PORT":       "{{AUTO_PORT}}",
  "SOCKS_PORT": "{{AUTO_SOCKS_PORT}}",
  "UUID":       "{{AUTO_UUID}}",
  "PASSWORD":   "{{AUTO_PASSWORD}}"
}
```

| Placeholder | Resolved to |
|---|---|
| `{{AUTO_PORT}}`, `{{AUTO_TCP_PORT}}`, `{{AUTO_UDP_PORT}}`, `{{AUTO_QUIC_PORT}}` | Random free port |
| `{{AUTO_SOCKS_PORT}}`, `{{AUTO_UPSTREAM_PORT}}` | Random free port |
| `{{AUTO_UUID}}` | UUID v4 (`uuid.New()`) |
| `{{AUTO_PASSWORD}}` | 16 random bytes, hex-encoded |
| `LISTEN_SERVER` | Copied from `SERVER` when unset (automatic) |
| `LOG_LEVEL` | `"error"` when unset (automatic) |

Using explicit placeholders means a var value that legitimately contains the
word `"auto"` is never misinterpreted.

Full list: [placeholders.md](placeholders.md).

---

## Full example

```json5
{
  // sing-box shadowsocks server — generated by hiddify-health
  "log": {"level": "error"},

  "inbounds": [
    {
      "type": "shadowsocks",
      "tag":  "ss-in",
      "listen":      "{{ SERVER }}",       // replaced with 127.0.0.1
      "listen_port": {{ PORT }},           // replaced with random port (integer)
      "method":      "chacha20-ietf-poly1305",
      "password":    "{{ PASSWORD }}",     // replaced with random hex password

      {% if obfs %}
      "plugin":      "obfs-local",
      "plugin_opts": "obfs=http;obfs-host={{ HOST_NAME }}",
      {% endif %}
    },
  ],  # trailing comma OK

  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route":     {"final": "direct"},
}
```
