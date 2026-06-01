package runner

import (
	"fmt"
	"strconv"
	"strings"
)

// RunConfig is the schema of run.json placed next to each example's config files.
//
// Two forms of vars are supported:
//
//  1. Single run (legacy): "vars": {"KEY": "value", …}
//  2. Multi-variant:       "vars": [{"TITLE":"name","KEY":"value"}, …]
//
// Multi-variant runs the same example once per variant entry; results are
// reported separately with the TITLE as the variant label.
type RunConfig struct {
	// Name is a human-readable label shown in the web UI and CLI output.
	Name string `json:"name"`

	// Core is "sing-box", "xray", or "hiddify-core".
	// Ignored when ClientProcessPath / ServerProcessPath are set.
	Core string `json:"core,omitempty"`

	// Binary path overrides. Value may be a literal path or "env.VAR_NAME"
	// to read from an environment variable at runtime.
	ClientProcessPath string `json:"client_process_path,omitempty"`
	ServerProcessPath string `json:"server_process_path,omitempty"` // default = ClientProcessPath
	ClientArg         string `json:"client_arg,omitempty"`           // e.g. "run -c "
	ServerArg         string `json:"server_arg,omitempty"`           // default = ClientArg

	// ServerConfig and ClientConfig are paths relative to the run.json directory.
	// Files are discovered with extension fallback: .j2 → .tpl → .json
	ServerConfig string `json:"server_config,omitempty"`
	ClientConfig string `json:"client_config,omitempty"`

	// Topology describes a multi-hop chain. When set it replaces ServerConfig/ClientConfig.
	Topology []TopologyNode `json:"topology,omitempty"`

	// VarsRaw holds substitution values. May be a JSON object (single run)
	// or a JSON array of objects (multi-variant). Use Variants() to iterate.
	VarsRaw interface{} `json:"vars"`

	// DeployToServer is ssh://user:pass@host:port for remote server deployment.
	DeployToServer string `json:"deploy_to_server,omitempty"`

	// TLS, when true, auto-generates a self-signed cert bundle and injects
	// {{TLS_CERT}}, {{TLS_KEY}}, {{TLS_CA}}, {{CA_FINGERPRINT}} placeholders.
	TLS bool `json:"tls,omitempty"`

	// Checks lists which health checks to run.
	Checks []string `json:"checks"`

	BeforeStart []string `json:"before_start,omitempty"`
	AfterStop   []string `json:"after_stop,omitempty"`
	TimeoutSec  int      `json:"timeout_sec,omitempty"`
}

// Variant is one resolved test variant derived from a vars entry.
type Variant struct {
	Title string
	Vars  map[string]string
}

// Variants returns the list of test variants described by VarsRaw.
//
// Shapes handled:
//  1. nil / missing → single variant with empty vars
//  2. map[string]interface{} → single variant
//  3. []interface{} (each element a map) → one variant per element
//
// The special key "TITLE" is used as the variant label and removed from vars.
func (c *RunConfig) Variants() []Variant {
	if c.VarsRaw == nil {
		return []Variant{{Title: c.Name}}
	}

	switch v := c.VarsRaw.(type) {
	case map[string]interface{}:
		m := toStrMap(v)
		title := extractTitle(m, c.Name)
		return []Variant{{Title: title, Vars: m}}

	case []interface{}:
		var out []Variant
		for i, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			sm := toStrMap(m)
			title := extractTitle(sm, fmt.Sprintf("%s [%d]", c.Name, i+1))
			out = append(out, Variant{Title: title, Vars: sm})
		}
		return out
	}

	return []Variant{{Title: c.Name}}
}

// DeployIsRemote reports whether DeployToServer is set.
func (c *RunConfig) DeployIsRemote() bool {
	return strings.TrimSpace(c.DeployToServer) != ""
}

// TopologyNode describes one node in a multi-hop chain.
type TopologyNode struct {
	Role   string `json:"role"`
	Core   string `json:"core,omitempty"`
	Config string `json:"config"`
	Host   string `json:"host,omitempty"` // ssh://user@host:port
}

// toStrMap converts map[string]interface{} → map[string]string.
// bool true → "1", false → "0". Numbers → decimal string.
func toStrMap(m map[string]interface{}) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = anyStr(v)
	}
	return out
}

func extractTitle(m map[string]string, fallback string) string {
	t := strings.TrimSpace(m["TITLE"])
	delete(m, "TITLE")
	if t == "" {
		return fallback
	}
	return t
}

func anyStr(v interface{}) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "1"
		}
		return "0"
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case int:
		return strconv.Itoa(t)
	}
	return fmt.Sprintf("%v", v)
}
