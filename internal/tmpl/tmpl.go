// Package tmpl performs {{PLACEHOLDER}} substitution in config files.
// "auto" values are resolved to random ports, UUIDs, or passwords at render time.
package tmpl

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"

	"github.com/google/uuid"
)

// KnownVars lists the standard placeholder keys understood by the runner.
var KnownVars = []string{
	"SERVER", "PORT", "SOCKS_PORT",
	"UUID", "PASSWORD",
	"HOST_NAME", "SNI_NAME",
	"TLS_CERT", "TLS_KEY", "TLS_CA", "CA_FINGERPRINT",
	"UPSTREAM_SERVER", "UPSTREAM_PORT",
}

// Render replaces every {{KEY}} in src with the value from vars.
// Values equal to "auto" are resolved as follows:
//   - PORT, SOCKS_PORT, UPSTREAM_PORT → random free TCP port
//   - UUID                             → new UUID v4
//   - PASSWORD                         → 16 random hex bytes
//
// Returns the rendered bytes and the fully-resolved vars map (so callers
// can propagate auto-assigned ports to the next node in a chain).
func Render(src []byte, vars map[string]string) ([]byte, map[string]string, error) {
	resolved := make(map[string]string, len(vars))
	for k, v := range vars {
		resolved[k] = v
	}

	// Resolve "auto" values before substitution.
	for _, k := range []string{"PORT", "SOCKS_PORT", "UPSTREAM_PORT"} {
		if resolved[k] == "auto" {
			p, err := freePort()
			if err != nil {
				return nil, nil, fmt.Errorf("tmpl: find free port for %s: %w", k, err)
			}
			resolved[k] = fmt.Sprintf("%d", p)
		}
	}
	if resolved["UUID"] == "auto" {
		resolved["UUID"] = uuid.New().String()
	}
	if resolved["PASSWORD"] == "auto" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, fmt.Errorf("tmpl: rand password: %w", err)
		}
		resolved["PASSWORD"] = hex.EncodeToString(b)
	}

	out := src
	for k, v := range resolved {
		if v == "" || v == "auto" {
			continue
		}
		out = bytes.ReplaceAll(out, []byte("{{"+k+"}}"), []byte(v))
	}
	return out, resolved, nil
}

// freePort asks the OS for an available TCP port.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
