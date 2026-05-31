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
	"github.com/hiddify/hiddify_config_health/internal/tmpl"
)

// Result is the outcome of one complete example run.
type Result struct {
	Name        string
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

// Run loads run.json from dir and executes the full test pipeline, streaming
// log lines to out (nil = discard).
func Run(ctx context.Context, dir string, out io.Writer) (*Result, error) {
	if out == nil {
		out = io.Discard
	}
	log := newLogWriter(out)

	cfg, err := loadRunConfig(dir)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	res := &Result{
		Name:      cfg.Name,
		Dir:       dir,
		StartedAt: start,
	}

	defer func() { res.Duration = time.Since(start) }()

	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// --- vars + TLS ---
	vars := mergeVars(cfg.Vars)
	if cfg.TLS {
		hosts := []string{cfg.Vars["HOST_NAME"], cfg.Vars["SNI_NAME"], "127.0.0.1", "localhost"}
		bundle, err := cert.Generate(dedup(hosts))
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
		log.Printf("[cert] generated self-signed CA fingerprint=%s", bundle.CAFingerprint[:16]+"…")
	}

	// --- before_start hooks ---
	for _, cmd := range cfg.BeforeStart {
		log.Printf("[hook] before_start: %s", cmd)
		if err := runShell(ctx, cmd, out); err != nil {
			log.Printf("[hook] warning: %v", err)
		}
	}

	// --- build topology ---
	var nodes []nodeSpec
	if len(cfg.Topology) > 0 {
		nodes, err = buildTopology(cfg, dir)
	} else {
		nodes, err = buildSimple(cfg, dir)
	}
	if err != nil {
		res.Err = err
		return res, err
	}

	// --- render configs ---
	renderedVars := vars
	for i := range nodes {
		rendered, resolved, err := renderConfig(nodes[i].configPath, renderedVars)
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
		// Propagate resolved ports for chaining.
		for k, v := range resolved {
			renderedVars[k] = v
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
		for _, fn := range stopFns {
			fn()
		}
	}()

	for i := range nodes {
		n := &nodes[i]
		c := core.New(n.coreName, "")
		if c == nil {
			res.Err = fmt.Errorf("unknown core %q", n.coreName)
			return res, res.Err
		}
		res.CoreVersion = c.Version()
		log.Printf("[core] starting %s (%s) role=%s", n.coreName, res.CoreVersion, n.role)

		if n.deploy != nil && n.deploy.IsRemote() {
			sc, err := dialSSH(n.deploy.URL)
			if err != nil {
				res.Err = fmt.Errorf("SSH dial %s: %w", n.deploy.URL, err)
				return res, res.Err
			}
			remoteDir := n.deploy.RemoteDir
			if remoteDir == "" {
				remoteDir = "/tmp/hch"
			}
			if err := scpFile(sc, n.renderedPath, remoteDir+"/server.json"); err != nil {
				sc.Close()
				res.Err = err
				return res, err
			}
			if err := sshExec(sc, fmt.Sprintf("nohup %s run -c %s/server.json > /tmp/hch.log 2>&1 &", n.coreName, remoteDir)); err != nil {
				sc.Close()
				res.Err = err
				return res, err
			}
			stopFns = append(stopFns, func() {
				_ = sshExec(sc, fmt.Sprintf("pkill -f '%s'", n.coreName))
				sc.Close()
			})
		} else {
			runCtx, cancel := context.WithCancel(ctx)
			if err := c.Start(runCtx, n.renderedPath, log); err != nil {
				cancel()
				res.Err = fmt.Errorf("start %s: %w", n.role, err)
				return res, res.Err
			}
			stopFns = append(stopFns, func() {
				cancel()
				_ = c.Stop()
			})
		}
	}

	// --- wait for client SOCKS port ---
	socksAddr := net.JoinHostPort("127.0.0.1", socksPort)
	log.Printf("[wait] SOCKS proxy at %s", socksAddr)
	if err := waitTCP(ctx, socksAddr, timeout); err != nil {
		res.Err = fmt.Errorf("SOCKS port not ready: %w", err)
		return res, res.Err
	}
	log.Printf("[wait] SOCKS ready")

	// --- health checks ---
	hcfg := health.Config{
		ProxyAddr: "socks5://" + socksAddr,
		Checks:    cfg.Checks,
		Timeout:   timeout,
	}
	hresults, err := health.Run(ctx, hcfg)
	if err != nil {
		res.Err = err
	}
	res.Checks = hresults

	pass := err == nil
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

	// --- protocol detection ---
	res.Fingerprint = detect.Passive(hresults)

	// --- after_stop hooks ---
	for _, cmd := range cfg.AfterStop {
		log.Printf("[hook] after_stop: %s", cmd)
		_ = runShell(ctx, cmd, out)
	}

	res.Log = log.String()
	return res, nil
}

// --- helpers ---

type nodeSpec struct {
	role         string
	coreName     string
	configPath   string
	renderedPath string
	deploy       *DeployConfig
}

func buildSimple(cfg RunConfig, dir string) ([]nodeSpec, error) {
	coreName := cfg.Core
	nodes := []nodeSpec{
		{role: "server", coreName: coreName, configPath: filepath.Join(dir, cfg.ServerConfig)},
		{role: "client", coreName: coreName, configPath: filepath.Join(dir, cfg.ClientConfig)},
	}
	if cfg.Deploy.IsRemote() {
		d := cfg.Deploy
		nodes[0].deploy = &d
	}
	return nodes, nil
}

func buildTopology(cfg RunConfig, dir string) ([]nodeSpec, error) {
	coreName := cfg.Core
	var nodes []nodeSpec
	for _, t := range cfg.Topology {
		c := t.Core
		if c == "" {
			c = coreName
		}
		var d *DeployConfig
		if t.Host != "" {
			dc := DeployConfig{URL: t.Host}
			d = &dc
		}
		nodes = append(nodes, nodeSpec{
			role:       t.Role,
			coreName:   c,
			configPath: filepath.Join(dir, t.Config),
			deploy:     d,
		})
	}
	return nodes, nil
}

func loadRunConfig(dir string) (RunConfig, error) {
	path := filepath.Join(dir, "run.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return RunConfig{}, fmt.Errorf("load run.json: %w", err)
	}
	var cfg RunConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return RunConfig{}, fmt.Errorf("parse run.json: %w", err)
	}
	if len(cfg.Checks) == 0 {
		cfg.Checks = []string{"dns", "http"}
	}
	return cfg, nil
}

func mergeVars(v map[string]string) map[string]string {
	out := make(map[string]string, len(v))
	for k, val := range v {
		out[k] = val
	}
	return out
}

func renderConfig(path string, vars map[string]string) ([]byte, map[string]string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return tmpl.Render(src, vars)
}

func writeTempConfig(content []byte, role string) (string, error) {
	f, err := os.CreateTemp("", fmt.Sprintf("hch-%s-*.json", role))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func waitTCP(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
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

// logWriter captures output and also streams to an underlying writer.
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
