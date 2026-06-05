// Package tmpl renders config templates using Pongo2 (Jinja2-compatible syntax)
// and strips JSON5 extensions (// # comments, trailing commas) to produce
// valid JSON that proxy cores can consume.
//
// # Template syntax
//
// Templates use Pongo2 / Jinja2 syntax:
//
//	{{ SERVER }}          variable substitution
//	{% if tls %}…{% endif %}   conditionals
//	{% for u in users %}…{% endfor %}   loops
//	{{ PORT | default("8388") }}   filters
//
// Legacy {{KEY}} (no spaces) is also supported — auto-normalised before parse.
//
// # JSON5 extensions allowed in templates
//
//	// single-line comment
//	#  single-line comment
//	/* block comment */
//	trailing commas in objects and arrays
//
// All extensions are stripped before the rendered output is written to disk.
//
// # Auto-resolved vars
//
// Set a var's value to an AUTO placeholder in run.json and the runner picks:
//
//	{{AUTO_PORT}}, {{AUTO_TCP_PORT}}, {{AUTO_UDP_PORT}},
//	{{AUTO_QUIC_PORT}}, {{AUTO_SOCKS_PORT}}, {{AUTO_UPSTREAM_PORT}}  →  random free TCP port
//	{{AUTO_UUID}}      →  new UUID v4
//	{{AUTO_PASSWORD}}  →  16 random bytes, hex-encoded
package tmpl

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/flosch/pongo2/v6"
	"github.com/google/uuid"

	"github.com/hiddify/hiddify_config_health/internal/json5"
)

// KnownVars lists standard placeholder keys recognised by the runner.
var KnownVars = []string{
	"SERVER", "LISTEN_SERVER",
	"PORT", "TCP_PORT", "UDP_PORT", "QUIC_PORT", "SOCKS_PORT",
	"UUID", "PASSWORD",
	"HOST_NAME", "SNI_NAME",
	"TLS_CERT", "TLS_KEY", "TLS_CA", "CA_FINGERPRINT",
	"UPSTREAM_SERVER", "UPSTREAM_PORT",
	"LOG_LEVEL",
	// Protocol-specific
	"VLESS_FLOW", "VLESS_ENC", "VLESS_DEC",
	// WireGuard
	"WG_SERVER_PRIVKEY", "WG_SERVER_PUBKEY",
	"WG_CLIENT_PRIVKEY", "WG_CLIENT_PUBKEY",
}

// reNoSpacePlaceholder matches {{KEY}} or {{KEY}} patterns without spaces
// so we can normalise them to {{ KEY }} for pongo2.
var reNoSpacePlaceholder = regexp.MustCompile(`\{\{([A-Z_][A-Z0-9_]*)\}\}`)

func init() {
	// split filter: "a,b,c"|split:"," → ["a","b","c"]
	// Enables for-loops over comma-separated strings in run.json.j2.
	pongo2.RegisterFilter("split", func(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
		sep := param.String()
		if sep == "" {
			sep = ","
		}
		parts := strings.Split(in.String(), sep)
		items := make([]interface{}, len(parts))
		for i, p := range parts {
			items[i] = strings.TrimSpace(p)
		}
		return pongo2.AsValue(items), nil
	})
}

// Render renders src as a Pongo2/Jinja2 template against vars, resolves {{AUTO_*}}
// sentinels, then optionally strips JSON5 extensions.
//
// stripJSON5 (optional, default true): when false, JSON5 comments and trailing
// commas are kept in the output — useful for cores that accept JSON5, or for
// debugging rendered output.
//
// Returns: rendered bytes, fully-resolved vars map, error.
func Render(src []byte, vars map[string]string, stripJSON5 ...bool) ([]byte, map[string]string, error) {
	doStrip := len(stripJSON5) == 0 || stripJSON5[0]
	// 1. Resolve {{AUTO_*}} sentinels before rendering so the template sees real values.
	resolved, err := resolveAuto(vars)
	if err != nil {
		return nil, nil, err
	}

	// 2. Normalise {{KEY}} (no spaces) → {{ KEY }} for pongo2.
	normalised := reNoSpacePlaceholder.ReplaceAll(src, []byte(`{{ $1 }}`))

	// 3. Pongo2 render.
	tpl, err := pongo2.FromString(string(normalised))
	if err != nil {
		return nil, nil, fmt.Errorf("tmpl: parse template: %w", err)
	}

	ctx := buildContext(resolved)

	rendered, err := tpl.ExecuteBytes(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("tmpl: render: %w", err)
	}

	// 4. Optionally strip JSON5 extensions → valid JSON.
	if doStrip {
		clean, err := json5.Strip(rendered)
		if err != nil {
			return nil, nil, fmt.Errorf("tmpl: json5 strip: %w", err)
		}
		return clean, resolved, nil
	}
	return rendered, resolved, nil
}

// RenderFile renders a template file using pongo2.FromFile so that
// {% include %} and {% extends %} resolve relative to the template's directory.
// stripJSON5 (optional, default true): pass false to keep JSON5 comments.
func RenderFile(path string, vars map[string]string, stripJSON5 ...bool) ([]byte, map[string]string, error) {
	doStrip := len(stripJSON5) == 0 || stripJSON5[0]
	// Read source so we can normalise {{KEY}} before pongo2 parses it.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("tmpl: read %s: %w", path, err)
	}

	resolved, err := resolveAuto(vars)
	if err != nil {
		return nil, nil, err
	}

	// Normalise {{KEY}} → {{ KEY }}.
	normalised := reNoSpacePlaceholder.ReplaceAll(raw, []byte(`{{ $1 }}`))

	// Write normalised source to a temp file so pongo2.FromFile can resolve
	// includes relative to the original template's directory.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hch-tpl-*.j2")
	if err != nil {
		return nil, nil, fmt.Errorf("tmpl: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(normalised); err != nil {
		tmp.Close()
		return nil, nil, err
	}
	tmp.Close()

	tpl, err := pongo2.FromFile(tmpPath)
	if err != nil {
		return nil, nil, fmt.Errorf("tmpl: parse %s: %w", path, err)
	}

	ctx := buildContext(resolved)

	rendered, err := tpl.ExecuteBytes(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("tmpl: render %s: %w", path, err)
	}

	if doStrip {
		clean, err := json5.Strip(rendered)
		if err != nil {
			return nil, nil, fmt.Errorf("tmpl: json5 strip: %w", err)
		}
		return clean, resolved, nil
	}
	return rendered, resolved, nil
}

// buildContext constructs the pongo2 render context from resolved vars.
//
// Exposes:
//   - Every key from resolved, e.g. {{ SERVER }}, {{ PORT }}
//   - Lowercase aliases: {{ server }}, {{ port }}
//   - env map: {{ env.MY_VAR }} reads os.Getenv("MY_VAR")
func buildContext(resolved map[string]string) pongo2.Context {
	// Build a lazy env map using a custom type that reads from os.Getenv.
	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}

	ctx := pongo2.Context{"env": envMap}
	for k, v := range resolved {
		ctx[k] = v
		ctx[lowercase(k)] = v
	}
	return ctx
}

// autoPlaceholder returns the {{AUTO_KEY}} sentinel for a given var name.
// Used when checking if a var value requests auto-resolution.
func autoPlaceholder(key string) string { return "{{AUTO_" + key + "}}" }

// resolveAuto returns a copy of vars with {{AUTO_*}} sentinels resolved.
// Using explicit placeholders (instead of the bare string "auto") avoids
// false matches when a config legitimately contains the word "auto".
func resolveAuto(vars map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(vars))
	for k, v := range vars {
		out[k] = v
	}

	// Port vars — {{AUTO_PORT}} etc. get a random free port each.
	for _, k := range []string{"PORT", "TCP_PORT", "UDP_PORT", "SOCKS_PORT", "UPSTREAM_PORT", "QUIC_PORT"} {
		if out[k] == autoPlaceholder(k) {
			p, err := freePort()
			if err != nil {
				return nil, fmt.Errorf("tmpl: free port for %s: %w", k, err)
			}
			out[k] = fmt.Sprintf("%d", p)
		}
	}

	// Alias propagation: if a canonical var is set but its alias is not,
	// copy the canonical value so templates can use either name.
	aliases := [][2]string{
		{"SERVER", "LISTEN_SERVER"}, // server-side listen addr
	}
	for _, pair := range aliases {
		canon, alias := pair[0], pair[1]
		if out[alias] == "" && out[canon] != "" {
			out[alias] = out[canon]
		}
	}

	// Built-in protocol defaults — override in vars if needed.
	builtinDefaults := map[string]string{
		"LOG_LEVEL":  "error",
		"VLESS_ENC":  "none",
		"VLESS_DEC":  "none",
		"VLESS_FLOW": "",
		"HOST_NAME":  "example.com",
		"SNI_NAME":   "example.com",
	}
	for k, def := range builtinDefaults {
		if out[k] == "" {
			out[k] = def
		}
	}

	if out["UUID"] == autoPlaceholder("UUID") {
		out["UUID"] = uuid.New().String()
	}

	if out["PASSWORD"] == autoPlaceholder("PASSWORD") {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("tmpl: rand password: %w", err)
		}
		out["PASSWORD"] = hex.EncodeToString(b)
	}

	return out, nil
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// lowercase converts "SERVER" → "server" for case-insensitive template vars.
func lowercase(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(bytes.ToLower([]byte(s)))
}
