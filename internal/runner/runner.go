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
		Name:      cfg.Name,
		Variant:   v.Title,
		Dir:       dir,
		StartedAt: start,
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
		// argField is e.g. "run -c " — trailing space + config path appended by Start
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

// --- config path resolution (.j2 → .tpl → .json fallback) ---

var configExts = []string{".j2", ".tpl", ".json", ""}

func resolveConfigPath(base string) (string, error) {
	// If it already has a known extension, use as-is.
	ext := filepath.Ext(base)
	for _, e := range configExts[3:] { // non-empty exts
		if ext == e {
			if _, err := os.Stat(base); err == nil {
				return base, nil
			}
		}
	}
	// Strip extension and try each in order.
	stem := strings.TrimSuffix(base, ext)
	for _, e := range configExts[:3] {
		candidate := stem + e
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	// Try original path.
	if _, err := os.Stat(base); err == nil {
		return base, nil
	}
	return "", fmt.Errorf("config file not found: %s (tried .j2/.tpl/.json)", base)
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

// loadRunConfig loads run.json from dir and merges ancestor run.json files
// (walking up the directory tree) so parent directories provide defaults.
// Child values always win over parent values.
func loadRunConfig(dir string) (RunConfig, error) {
	// Collect run.json paths from root down to dir.
	var chain []string
	d := filepath.Clean(dir)
	for {
		p := filepath.Join(d, "run.json")
		if _, err := os.Stat(p); err == nil {
			chain = append([]string{p}, chain...) // prepend (root first)
		}
		parent := filepath.Dir(d)
		if parent == d {
			break // filesystem root
		}
		// Stop if there's no run.json at all in the parent — avoid reading
		// random directories above the examples tree.
		if _, err := os.Stat(filepath.Join(parent, "run.json")); err != nil {
			break
		}
		d = parent
	}

	if len(chain) == 0 {
		return RunConfig{}, fmt.Errorf("run.json not found in %s", dir)
	}

	// Load and merge: start with outermost (root), override with each child.
	var merged map[string]interface{}
	for _, path := range chain {
		b, err := os.ReadFile(path)
		if err != nil {
			return RunConfig{}, fmt.Errorf("read %s: %w", path, err)
		}
		clean, err := json5.Strip(b)
		if err != nil {
			return RunConfig{}, fmt.Errorf("json5 strip %s: %w", path, err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(clean, &m); err != nil {
			return RunConfig{}, fmt.Errorf("parse %s: %w", path, err)
		}
		if merged == nil {
			merged = m
		} else {
			for k, v := range m {
				// Non-empty child value wins.
				if !isEmptyVal(v) {
					merged[k] = v
				}
			}
		}
	}

	// Re-marshal merged map into RunConfig.
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

func renderConfig(path string, vars map[string]string) ([]byte, map[string]string, error) {
	// Use RenderFile so {% include "…" %} resolves relative to the template dir.
	return tmpl.RenderFile(path, vars)
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
