// Package runner orchestrates one test run: template substitution, process
// lifecycle, health checks, and result collection.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hiddify/hiddify_config_health/internal/cert"
	"github.com/hiddify/hiddify_config_health/internal/core"
	"github.com/hiddify/hiddify_config_health/internal/detect"
	"github.com/hiddify/hiddify_config_health/internal/health"
	"github.com/hiddify/hiddify_config_health/internal/json5"
	"github.com/hiddify/hiddify_config_health/internal/jsonmerge"
	"github.com/hiddify/hiddify_config_health/internal/tmpl"
)

// Result is the outcome of one complete example run.
type Result struct {
	Name        string
	Variant     string
	Dir         string
	Pass        bool
	Checks      []health.Result
	Fingerprint detect.TrafficFingerprint
	CoreVersion string
	Log         string
	StartedAt   time.Time
	Duration    time.Duration
	Err         error
}

// Run loads run.json from dir and executes the full test pipeline once per
// variant (vars array entry). Returns one Result per variant.
// Streams log to out (nil = discard).
func Run(ctx context.Context, dir string, out io.Writer) ([]*Result, error) {
	if out == nil {
		out = io.Discard
	}

	cfg, err := loadRunConfig(dir)
	if err != nil {
		return nil, err
	}

	variants := cfg.Variants()
	results := make([]*Result, 0, len(variants))
	for _, v := range variants {
		r, _ := runVariant(ctx, dir, cfg, v, out)
		results = append(results, r)
	}
	return results, nil
}

// RunFirst is a convenience wrapper that runs only the first variant and
// returns a single Result — preserves backward compatibility with callers
// that don't care about multi-variant.
func RunFirst(ctx context.Context, dir string, out io.Writer) (*Result, error) {
	results, err := Run(ctx, dir, out)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no variants in %s", dir)
	}
	return results[0], nil
}

func runVariant(ctx context.Context, dir string, cfg RunConfig, v Variant, out io.Writer) (*Result, error) {
	log := newLogWriter(out)
	if len(cfg.Variants()) > 1 {
		log.Printf("=== variant: %s ===", v.Title)
	}

	start := time.Now()
	res := &Result{
		Name:        cfg.Name,
		Variant:     v.Title,
		Dir:         dir,
		StartedAt:   start,
		Fingerprint: detect.TrafficFingerprint{Verdict: "unknown"},
	}
	defer func() { res.Duration = time.Since(start) }()

	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// --- build vars: variant vars as base ---
	vars := make(map[string]string, len(v.Vars))
	for k, val := range v.Vars {
		vars[k] = val
	}

	// --- TLS cert injection ---
	if cfg.TLS {
		hosts := dedup([]string{vars["HOST_NAME"], vars["SNI_NAME"], "127.0.0.1", "localhost"})
		bundle, err := cert.Generate(hosts)
		if err != nil {
			res.Err = fmt.Errorf("TLS cert gen: %w", err)
			return res, res.Err
		}
		certDir := filepath.Join(os.TempDir(), fmt.Sprintf("hch-%d", time.Now().UnixNano()))
		caPath, certPath, keyPath, err := cert.WriteToDir(bundle, certDir)
		if err != nil {
			res.Err = err
			return res, err
		}
		defer os.RemoveAll(certDir)
		vars["TLS_CA"] = caPath
		vars["TLS_CERT"] = certPath
		vars["TLS_KEY"] = keyPath
		vars["CA_FINGERPRINT"] = bundle.CAFingerprint
		log.Printf("[cert] self-signed CA fingerprint=%s…", bundle.CAFingerprint[:16])
	}

	// --- before_start hooks ---
	for _, cmd := range cfg.BeforeStart {
		log.Printf("[hook] before_start: %s", cmd)
		_ = runShell(ctx, cmd, log)
	}

	// --- build node list ---
	var nodes []nodeSpec
	var buildErr error
	if len(cfg.Topology) > 0 {
		nodes, buildErr = buildTopology(cfg, dir)
	} else {
		nodes, buildErr = buildSimple(cfg, dir)
	}
	if buildErr != nil {
		res.Err = buildErr
		return res, buildErr
	}

	// --- render configs (chaining propagates resolved vars forward) ---
	renderedVars := vars
	for i := range nodes {
		path, err := resolveConfigPath(nodes[i].configPath)
		if err != nil {
			res.Err = err
			return res, err
		}
		rendered, resolved, err := renderConfig(path, renderedVars)
		if err != nil {
			res.Err = fmt.Errorf("render %s: %w", nodes[i].role, err)
			return res, res.Err
		}
		tmp, err := writeTempConfig(rendered, nodes[i].role)
		if err != nil {
			res.Err = err
			return res, err
		}
		nodes[i].renderedPath = tmp
		for k, val := range resolved {
			renderedVars[k] = val
		}
		if nodes[i].role == "client" {
			renderedVars["UPSTREAM_SERVER"] = resolved["SERVER"]
			renderedVars["UPSTREAM_PORT"] = resolved["PORT"]
		}
	}

	socksPort := renderedVars["SOCKS_PORT"]
	if socksPort == "" {
		socksPort = "1080"
	}

	// --- start processes ---
	var stopFns []func()
	defer func() {
		for i := len(stopFns) - 1; i >= 0; i-- {
			stopFns[i]()
		}
	}()

	for i := range nodes {
		n := &nodes[i]

		binPath, args, err := resolveProcessArgs(cfg, n.role)
		if err != nil {
			res.Err = err
			return res, err
		}

		if n.sshURL != "" {
			sc, err := dialSSH(n.sshURL)
			if err != nil {
				res.Err = fmt.Errorf("SSH dial %s: %w", n.sshURL, err)
				return res, res.Err
			}
			remoteDir := "/tmp/hch"
			if err := scpFile(sc, n.renderedPath, remoteDir+"/config.json"); err != nil {
				sc.Close()
				res.Err = err
				return res, err
			}
			remoteCmd := fmt.Sprintf("nohup %s %s%s/config.json > /tmp/hch.log 2>&1 &",
				binPath, strings.Join(args, " ")+" ", remoteDir)
			if err := sshExec(sc, remoteCmd); err != nil {
				sc.Close()
				res.Err = err
				return res, err
			}
			coreName := cfg.Core
			stopFns = append(stopFns, func() {
				_ = sshExec(sc, fmt.Sprintf("pkill -f '%s'", coreName))
				sc.Close()
			})
		} else {
			c := buildProcessCore(binPath, args)
			res.CoreVersion = c.Version()
			log.Printf("[core] %s (%s) role=%s", binPath, res.CoreVersion, n.role)
			runCtx, cancel := context.WithCancel(ctx)
			if err := c.Start(runCtx, n.renderedPath, log); err != nil {
				cancel()
				res.Err = fmt.Errorf("start %s: %w", n.role, err)
				return res, res.Err
			}
			stopFns = append(stopFns, func() { cancel(); _ = c.Stop() })
		}
	}

	// --- wait for client SOCKS ---
	socksAddr := net.JoinHostPort("127.0.0.1", socksPort)
	log.Printf("[wait] SOCKS at %s", socksAddr)
	if err := waitTCP(ctx, socksAddr, timeout); err != nil {
		res.Err = fmt.Errorf("SOCKS not ready: %w", err)
		return res, res.Err
	}
	log.Printf("[wait] SOCKS ready")

	// --- health checks ---
	hresults, _ := health.Run(ctx, health.Config{
		ProxyAddr: "socks5://" + socksAddr,
		Checks:    cfg.Checks,
		Timeout:   timeout,
	})
	res.Checks = hresults

	pass := true
	for _, r := range hresults {
		if !r.OK {
			pass = false
		}
		status := "PASS"
		if !r.OK {
			status = "FAIL"
		}
		msg := fmt.Sprintf("[check] %-10s %s", r.Name, status)
		if r.Extra != "" {
			msg += " " + r.Extra
		}
		if !r.OK && r.Err != nil {
			msg += " err=" + r.Err.Error()
		}
		log.Printf("%s", msg)
	}
	res.Pass = pass
	res.Fingerprint = detect.Passive(hresults)

	for _, cmd := range cfg.AfterStop {
		log.Printf("[hook] after_stop: %s", cmd)
		_ = runShell(ctx, cmd, log)
	}

	res.Log = log.String()
	return res, nil
}

// --- process args resolution ---

// resolveProcessArgs returns (binaryPath, runArgs, error) for the given role.
// Priority: run.json ClientProcessPath/ServerProcessPath > core registry.
func resolveProcessArgs(cfg RunConfig, role string) (bin string, args []string, err error) {
	var pathField, argField string
	if role == "server" || role == "relay" {
		pathField = cfg.ServerProcessPath
		argField = cfg.ServerArg
		if pathField == "" {
			pathField = cfg.ClientProcessPath // fallback
		}
		if argField == "" {
			argField = cfg.ClientArg
		}
	} else {
		pathField = cfg.ClientProcessPath
		argField = cfg.ClientArg
	}

	if pathField != "" {
		bin = resolveEnvPath(pathField)
		if bin == "" {
			return "", nil, fmt.Errorf("binary path %q resolved to empty", pathField)
		}
		// argField may contain {{CONFIG_PATH}} placeholder (e.g. "run -c {{CONFIG_PATH}}").
		// If it does, the placeholder is substituted with the actual path at Start() time.
		// If it doesn't, the config path is appended at the end (backward compat).
		for _, a := range strings.Fields(argField) {
			args = append(args, a)
		}
		return bin, args, nil
	}

	// Fall back to core registry.
	if cfg.Core == "" {
		return "", nil, fmt.Errorf("run.json: core or client_process_path required")
	}
	c := core.New(cfg.Core, "")
	if c == nil {
		return "", nil, fmt.Errorf("unknown core %q", cfg.Core)
	}
	// processCore exposes its bin/args via Start; return a sentinel.
	return "_core_registry_:" + cfg.Core, nil, nil
}

// resolveEnvPath resolves "env.VAR_NAME" to os.Getenv("VAR_NAME"),
// or returns the string as-is if it doesn't start with "env.".
func resolveEnvPath(s string) string {
	if strings.HasPrefix(s, "env.") {
		return os.Getenv(strings.TrimPrefix(s, "env."))
	}
	return s
}

// buildProcessCore wraps a raw binary path + args into a core.Core.
// When bin starts with "_core_registry_:" it delegates to the registered core.
func buildProcessCore(bin string, args []string) core.Core {
	if strings.HasPrefix(bin, "_core_registry_:") {
		name := strings.TrimPrefix(bin, "_core_registry_:")
		return core.New(name, "")
	}
	return core.NewRaw(bin, args)
}

// --- config path resolution ---
//
// Priority order for config file names (given stem e.g. "server"):
//   conf-<stem>.json.j2  →  <stem>.json.j2  →  <stem>.tpl  →  <stem>.json

func resolveConfigPath(base string) (string, error) {
	dir := filepath.Dir(base)
	// Determine stem: strip any extension the caller provided.
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(filepath.Base(base), ext)

	// Candidates in priority order.
	candidates := []string{
		filepath.Join(dir, "conf-"+stem+".json.j2"),
		filepath.Join(dir, stem+".json.j2"),
		filepath.Join(dir, stem+".tpl"),
		filepath.Join(dir, stem+".json"),
		base, // original path as final fallback
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("config file not found: %s (tried conf-%s.json.j2, %s.json.j2, .tpl, .json)",
		base, stem, stem)
}

// --- node spec ---

type nodeSpec struct {
	role         string
	configPath   string
	renderedPath string
	sshURL       string
}

func buildSimple(cfg RunConfig, dir string) ([]nodeSpec, error) {
	serverCfg := cfg.ServerConfig
	clientCfg := cfg.ClientConfig
	if serverCfg == "" {
		serverCfg = "server.json"
	}
	if clientCfg == "" {
		clientCfg = "client.json"
	}
	sshURL := ""
	if cfg.DeployIsRemote() {
		sshURL = cfg.DeployToServer
	}
	return []nodeSpec{
		{role: "server", configPath: filepath.Join(dir, serverCfg), sshURL: sshURL},
		{role: "client", configPath: filepath.Join(dir, clientCfg)},
	}, nil
}

func buildTopology(cfg RunConfig, dir string) ([]nodeSpec, error) {
	var nodes []nodeSpec
	for _, t := range cfg.Topology {
		nodes = append(nodes, nodeSpec{
			role:       t.Role,
			configPath: filepath.Join(dir, t.Config),
			sshURL:     t.Host,
		})
	}
	return nodes, nil
}

// --- helpers ---

// findRunJSON returns the run.json or run.json.j2 path in dir, preferring .j2.
func findRunJSON(dir string) string {
	for _, name := range []string{"run.json.j2", "run.json"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// loadRunConfig loads run.json (or run.json.j2) from dir and merges ancestor
// run.json files (walking up the directory tree). Child values always win.
//
// run.json.j2 files are rendered through Pongo2 before parsing, allowing
// automatic test generation (loops, conditionals in run config itself).
func loadRunConfig(dir string) (RunConfig, error) {
	// Collect run.json / run.json.j2 paths from root down to dir.
	var chain []string
	d := filepath.Clean(dir)
	for {
		if p := findRunJSON(d); p != "" {
			chain = append([]string{p}, chain...) // prepend (root first)
		}
		parent := filepath.Dir(d)
		if parent == d {
			break // filesystem root
		}
		// Stop walking up when no run.json* in parent.
		if findRunJSON(parent) == "" {
			break
		}
		d = parent
	}

	if len(chain) == 0 {
		return RunConfig{}, fmt.Errorf("run.json not found in %s", dir)
	}

	// Load each level as raw map, accumulate scalar fields and vars separately.
	// Scalar fields: child wins if non-empty.
	// Vars (array or map): flatten all ancestor variants into a base-vars map,
	// then merge into each child variant so child vars always win.

	type level struct {
		raw      map[string]interface{}
		baseVars map[string]string // common vars from this level's variants
	}

	levels := make([]level, 0, len(chain))
	for _, path := range chain {
		b, err := loadAndRenderRunJSON(path)
		if err != nil {
			return RunConfig{}, fmt.Errorf("load %s: %w", path, err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(b, &m); err != nil {
			return RunConfig{}, fmt.Errorf("parse %s: %w", path, err)
		}
		levels = append(levels, level{raw: m, baseVars: flattenVars(m["vars"])})
	}


	// Merge scalar fields root → child.
	merged := make(map[string]interface{})
	for _, lv := range levels {
		for k, v := range lv.raw {
			if k == "vars" {
				continue // handled separately below
			}
			if !isEmptyVal(v) {
				merged[k] = v
			}
		}
	}

	// Merge vars: each child variant gets ancestor base vars as defaults.
	// Build cumulative base from all ancestor levels (not the final child).
	ancestorBase := make(map[string]string)
	for _, lv := range levels[:len(levels)-1] {
		for k, v := range lv.baseVars {
			ancestorBase[k] = v
		}
	}

	// Child (final level) variants inherit ancestorBase; child vars win.
	childVars := levels[len(levels)-1].raw["vars"]
	merged["vars"] = injectBaseIntoVars(childVars, ancestorBase)

	// Re-marshal into RunConfig.
	b, _ := json.Marshal(merged)
	var cfg RunConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return RunConfig{}, fmt.Errorf("merge run.json: %w", err)
	}
	if len(cfg.Checks) == 0 {
		cfg.Checks = []string{"dns", "http"}
	}
	return cfg, nil
}

// flattenVars extracts a single merged map from a vars array or map,
// stripping TITLE keys. Used to build ancestor defaults.
// loadAndRenderRunJSON reads a run.json or run.json.j2 file, renders it
// through Pongo2 if it has a .j2 extension, strips JSON5 extensions, and
// returns valid JSON bytes ready for json.Unmarshal.
func loadAndRenderRunJSON(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// For .j2 files, render through Pongo2 first (no vars — context comes
	// from the template itself, e.g. hardcoded loops for auto-generation).
	if strings.HasSuffix(path, ".j2") {
		rendered, _, err := tmpl.Render(b, map[string]string{})
		if err != nil {
			return nil, fmt.Errorf("render run.json.j2: %w", err)
		}
		return rendered, nil
	}
	// Plain run.json — just strip JSON5.
	clean, err := json5.Strip(b)
	if err != nil {
		return nil, fmt.Errorf("json5 strip: %w", err)
	}
	return clean, nil
}

func flattenVars(raw interface{}) map[string]string {
	out := make(map[string]string)
	switch v := raw.(type) {
	case map[string]interface{}:
		for k, val := range v {
			if k != "TITLE" {
				out[k] = anyStr(val)
			}
		}
	case []interface{}:
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			for k, val := range m {
				if k != "TITLE" {
					out[k] = anyStr(val)
				}
			}
		}
	}
	return out
}

// injectBaseIntoVars prepends base vars into each variant (base = defaults,
// variant vars win on conflict). Returns the modified vars value suitable
// for re-marshalling into RunConfig.VarsRaw.
func injectBaseIntoVars(childVarsRaw interface{}, base map[string]string) interface{} {
	if len(base) == 0 {
		return childVarsRaw
	}

	inject := func(m map[string]interface{}) map[string]interface{} {
		out := make(map[string]interface{}, len(base)+len(m))
		for k, v := range base {
			out[k] = v
		}
		for k, v := range m { // child wins
			out[k] = v
		}
		return out
	}

	switch v := childVarsRaw.(type) {
	case map[string]interface{}:
		return inject(v)
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				result[i] = inject(m)
			} else {
				result[i] = item
			}
		}
		return result
	case nil:
		// No child vars — return base as a single variant map.
		out := make(map[string]interface{}, len(base))
		for k, v := range base {
			out[k] = v
		}
		return out
	}
	return childVarsRaw
}

func isEmptyVal(v interface{}) bool {
	if v == nil {
		return true
	}
	switch t := v.(type) {
	case string:
		return t == ""
	case []interface{}:
		return len(t) == 0
	case map[string]interface{}:
		return len(t) == 0
	case bool:
		return !t
	}
	return false
}

// renderConfig renders path as a Pongo2 template, then checks whether a
// base template exists at <parent>/templates/base/<role>.json.j2 and, if
// so, renders it too and deep-merges (base = defaults, protocol = overrides).
func renderConfig(path string, vars map[string]string) ([]byte, map[string]string, error) {
	rendered, resolved, err := tmpl.RenderFile(path, vars)
	if err != nil {
		return nil, nil, err
	}

	// Determine role from filename: "server*" → server, otherwise client.
	role := "client"
	base := filepath.Base(path)
	if strings.HasPrefix(base, "server") {
		role = "server"
	}

	// Look for <example-parent>/templates/base/<role>.json.j2
	baseTpl := findBaseTemplate(filepath.Dir(path), role)
	if baseTpl == "" {
		return rendered, resolved, nil
	}

	baseRendered, _, err := tmpl.RenderFile(baseTpl, resolved)
	if err != nil {
		// Non-fatal: base template render failure just skips merging.
		return rendered, resolved, nil
	}

	// Only merge when rendered output is a valid JSON object.
	if !isJSONObject(rendered) || !isJSONObject(baseRendered) {
		return rendered, resolved, nil
	}

	merged, err := jsonmerge.Merge(baseRendered, rendered)
	if err != nil {
		return rendered, resolved, nil
	}
	return merged, resolved, nil
}

// findBaseTemplate walks up from dir looking for
//
//	<dir>/../templates/base/<role>.json.j2
//	<dir>/../../templates/base/<role>.json.j2
//
// Returns empty string if not found.
func findBaseTemplate(dir, role string) string {
	d := filepath.Clean(dir)
	for i := 0; i < 4; i++ {
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		for _, ext := range []string{".json.j2", ".j2", ".tpl", ".json"} {
			candidate := filepath.Join(parent, "templates", "base", role+ext)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
		d = parent
	}
	return ""
}

func isJSONObject(b []byte) bool {
	for _, c := range b {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		return c == '{'
	}
	return false
}

func writeTempConfig(content []byte, role string) (string, error) {
	f, err := os.CreateTemp("", fmt.Sprintf("hch-%s-*.json", role))
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.Write(content)
	return f.Name(), err
}

func waitTCP(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s", addr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func runShell(ctx context.Context, cmd string, out io.Writer) error {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Stdout = out
	c.Stderr = out
	return c.Run()
}

func dedup(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

type logWriter struct {
	w   io.Writer
	buf strings.Builder
}

func newLogWriter(w io.Writer) *logWriter { return &logWriter{w: w} }

func (l *logWriter) Printf(format string, args ...any) {
	line := fmt.Sprintf(format+"\n", args...)
	l.buf.WriteString(line)
	_, _ = l.w.Write([]byte(line))
}

func (l *logWriter) Write(p []byte) (int, error) {
	l.buf.Write(p)
	return l.w.Write(p)
}

func (l *logWriter) String() string { return l.buf.String() }
