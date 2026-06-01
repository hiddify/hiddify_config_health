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
// Set a var's value to "auto" in run.json and the runner picks:
//
//	PORT, SOCKS_PORT, UPSTREAM_PORT  →  random free TCP port
//	UUID                              →  new UUID v4
//	PASSWORD                          →  16 random bytes, hex-encoded
package tmpl

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"

	"github.com/flosch/pongo2/v6"
	"github.com/google/uuid"

	"github.com/hiddify/hiddify_config_health/internal/json5"
)

// KnownVars lists standard placeholder keys recognised by the runner.
var KnownVars = []string{
	"SERVER", "PORT", "SOCKS_PORT",
	"UUID", "PASSWORD",
	"HOST_NAME", "SNI_NAME",
	"TLS_CERT", "TLS_KEY", "TLS_CA", "CA_FINGERPRINT",
	"UPSTREAM_SERVER", "UPSTREAM_PORT",
}

// reNoSpacePlaceholder matches {{KEY}} or {{KEY}} patterns without spaces
// so we can normalise them to {{ KEY }} for pongo2.
var reNoSpacePlaceholder = regexp.MustCompile(`\{\{([A-Z_][A-Z0-9_]*)\}\}`)

// Render renders src as a Pongo2/Jinja2 template against vars, resolves "auto"
// values, then strips JSON5 extensions from the result.
//
// Returns:
//   - rendered valid-JSON bytes
//   - fully-resolved vars map (auto-assigned values are filled in)
//   - error
func Render(src []byte, vars map[string]string) ([]byte, map[string]string, error) {
	// 1. Resolve "auto" vars before rendering so the template sees real values.
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

	ctx := pongo2.Context{}
	for k, v := range resolved {
		ctx[k] = v
		// Also expose lowercase alias so {{ server }} works too.
		ctx[lowercase(k)] = v
	}

	rendered, err := tpl.ExecuteBytes(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("tmpl: render: %w", err)
	}

	// 4. Strip JSON5 extensions → valid JSON.
	clean, err := json5.Strip(rendered)
	if err != nil {
		return nil, nil, fmt.Errorf("tmpl: json5 strip: %w", err)
	}

	return clean, resolved, nil
}

// resolveAuto returns a copy of vars with "auto" values resolved.
func resolveAuto(vars map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(vars))
	for k, v := range vars {
		out[k] = v
	}

	for _, k := range []string{"PORT", "SOCKS_PORT", "UPSTREAM_PORT"} {
		if out[k] == "auto" {
			p, err := freePort()
			if err != nil {
				return nil, fmt.Errorf("tmpl: free port for %s: %w", k, err)
			}
			out[k] = fmt.Sprintf("%d", p)
		}
	}

	if out["UUID"] == "auto" {
		out["UUID"] = uuid.New().String()
	}

	if out["PASSWORD"] == "auto" {
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
