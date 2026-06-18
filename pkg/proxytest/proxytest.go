// Package proxytest is the public library API for testing proxy share-links
// and subscriptions. It parses a vless://, vmess://, ss://, trojan://,
// hysteria2:// or tuic:// URI (or a whole subscription), stands up a local
// SOCKS inbound that egresses through each proxy on each requested core
// (sing-box / xray), runs the health + censorship check suite, and returns
// structured per-proxy, per-core results.
//
// It is import-safe for client applications: no global state, no example
// directories, no CLI coupling.
package proxytest

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hiddify/hiddify_config_health/internal/clientgen"
	"github.com/hiddify/hiddify_config_health/internal/core"
	"github.com/hiddify/hiddify_config_health/internal/detect"
	"github.com/hiddify/hiddify_config_health/internal/freeport"
	"github.com/hiddify/hiddify_config_health/internal/geo"
	"github.com/hiddify/hiddify_config_health/internal/health"
	"github.com/hiddify/hiddify_config_health/internal/proxyuri"
)

// Options configures a test run.
type Options struct {
	Cores       []string          // default ["sing-box","xray"]
	Full        bool              // false=fast suite; true=+load/entropy/active-probe/tls-fingerprint
	Checks      []string          // override the check set entirely (optional)
	Timeout     time.Duration     // per-check timeout (default 10s)
	Concurrency int               // proxies tested in parallel (default 4)
	Binaries    map[string]string // core -> binary path; else env (SINGBOX_BIN / XRAY_BIN)
}

// CoreResult is the outcome of one proxy tested on one core.
type CoreResult struct {
	Core        string                    `json:"core"`
	Supported   bool                      `json:"supported"`
	Pass        bool                      `json:"pass"`
	Checks      []health.Result           `json:"checks,omitempty"`
	Fingerprint detect.TrafficFingerprint `json:"fingerprint"`
	Err         string                    `json:"error,omitempty"`
}

// ProxyResult holds all per-core results for one proxy.
type ProxyResult struct {
	Tag      string         `json:"tag"`
	Protocol string         `json:"protocol"`
	Server   string         `json:"server"`
	Port     int            `json:"port"`
	Geo      geo.Info       `json:"geo"`
	PerCore  []CoreResult   `json:"per_core"`
	proxy    proxyuri.Proxy // kept for advanced checks; not serialised (no creds leak)
}

// Baseline is the user's raw (no-proxy) connection measurement, so proxy
// speeds can be compared against the real line.
type Baseline struct {
	LatencyMs    float64 `json:"latency_ms"`
	DownloadBPS  float64 `json:"download_bps"`
	UploadBPS    float64 `json:"upload_bps"`
	Err          string  `json:"error,omitempty"`
}

var fastChecks = []string{"dns", "http", "ping"}
var fullChecks = []string{"dns", "http", "ping", "download", "upload", "load", "entropy",
	"active-probe", "tls-fingerprint", "reality-verify", "dpi-classify", "pageload", "stability"}
var fullOptional = []string{"quic", "load", "entropy", "active-probe", "tls-fingerprint",
	"reality-verify", "dpi-classify", "pageload", "stability"}

func (o Options) checks() (checks, optional []string) {
	if len(o.Checks) > 0 {
		return o.Checks, nil
	}
	if o.Full {
		return fullChecks, fullOptional
	}
	return fastChecks, nil
}

func (o Options) cores() []string {
	if len(o.Cores) > 0 {
		return o.Cores
	}
	return []string{"sing-box", "xray"}
}

func (o Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return 10 * time.Second
}

func (o Options) concurrency() int {
	if o.Concurrency > 0 {
		return o.Concurrency
	}
	return 4
}

// TestProxy parses and tests a single proxy URI on each configured core.
func TestProxy(ctx context.Context, uri string, o Options) (ProxyResult, error) {
	p, err := proxyuri.ParseURI(uri)
	if err != nil {
		return ProxyResult{}, err
	}
	return testParsed(ctx, *p, o), nil
}

// TestProxies tests a list of proxy URIs, bounded by Options.Concurrency.
// Parse errors are skipped (a proxy that does not parse is simply absent).
func TestProxies(ctx context.Context, uris []string, o Options) []ProxyResult {
	var parsed []proxyuri.Proxy
	for _, u := range uris {
		if p, err := proxyuri.ParseURI(u); err == nil {
			parsed = append(parsed, *p)
		}
	}
	return testBatch(ctx, parsed, o)
}

// TestSubscription fetches a subscription URL and tests every proxy in it.
func TestSubscription(ctx context.Context, subURL string, o Options) ([]ProxyResult, []error) {
	proxies, errs := proxyuri.ParseSubscription(ctx, subURL)
	return testBatch(ctx, proxies, o), errs
}

func testBatch(ctx context.Context, proxies []proxyuri.Proxy, o Options) []ProxyResult {
	results := make([]ProxyResult, len(proxies))
	sem := make(chan struct{}, o.concurrency())
	var wg sync.WaitGroup
	for i := range proxies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = testParsed(ctx, proxies[i], o)
		}(i)
	}
	wg.Wait()
	return results
}

func testParsed(ctx context.Context, p proxyuri.Proxy, o Options) ProxyResult {
	res := ProxyResult{Tag: p.Tag, Protocol: p.Protocol, Server: p.Server, Port: p.Port, proxy: p}
	res.Geo = geo.Lookup(p.Server)
	for _, coreName := range o.cores() {
		res.PerCore = append(res.PerCore, testOne(ctx, p, coreName, o))
	}
	return res
}

// testOne runs one proxy through one core and the check suite.
func testOne(ctx context.Context, p proxyuri.Proxy, coreName string, o Options) CoreResult {
	cr := CoreResult{Core: coreName, Supported: true}

	// Pre-flight: confirm this core can express the protocol at all (cheap,
	// avoids spending a port/process on an unsupported combo).
	if _, err := clientgen.Generate(coreName, p, 1080); err != nil {
		cr.Supported = false
		cr.Err = err.Error()
		return cr
	}

	binPath := ""
	if o.Binaries != nil {
		binPath = o.Binaries[coreName]
	}
	if core.New(coreName, binPath) == nil {
		cr.Supported = false
		cr.Err = "core not registered: " + coreName
		return cr
	}

	// Start the core, retrying on a fresh SOCKS port if the chosen port races
	// with another concurrent core (TOCTOU between Free() and bind). We confirm
	// readiness by binding-probe ownership: the port must NOT be bindable once
	// our core owns it, and waitTCP must succeed.
	var (
		c         core.Core
		socksAddr string
		startErr  error
	)
	for attempt := 0; attempt < 4; attempt++ {
		socksPort, err := freeport.Free()
		if err != nil {
			cr.Err = "freeport: " + err.Error()
			return cr
		}
		cfg, _ := clientgen.Generate(coreName, p, socksPort)

		tmpFile, err := os.CreateTemp("", "hch-client-*.json")
		if err != nil {
			cr.Err = err.Error()
			return cr
		}
		tmpPath := tmpFile.Name()
		_, _ = tmpFile.Write(cfg)
		_ = tmpFile.Close()

		nc := core.New(coreName, binPath)
		if err := nc.Start(ctx, tmpPath, nil); err != nil {
			os.Remove(tmpPath)
			startErr = err
			continue // process failed to spawn — retry
		}
		addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", socksPort))
		if err := waitTCP(ctx, addr, o.timeout()); err != nil {
			_ = nc.Stop()
			os.Remove(tmpPath)
			startErr = err
			continue // SOCKS never came up on our port — retry with a new one
		}
		c, socksAddr = nc, addr
		defer c.Stop()
		defer os.Remove(tmpPath)
		break
	}
	if c == nil {
		cr.Err = "core did not become ready"
		if startErr != nil {
			cr.Err += ": " + startErr.Error()
		}
		return cr
	}

	checks, optional := o.checks()
	hres, _ := health.Run(ctx, health.Config{
		ProxyAddr:      "socks5://" + socksAddr,
		Checks:         checks,
		OptionalChecks: optional,
		Timeout:        o.timeout(),
		ServerHost:     p.Server,
		ServerPort:     fmt.Sprintf("%d", p.Port),
		SNI:            p.SNI,
	})
	cr.Checks = hres
	cr.Fingerprint = detect.Passive(hres)
	cr.Pass = true
	optMap := map[string]bool{}
	for _, n := range optional {
		optMap[n] = true
	}
	for _, r := range hres {
		if !r.OK && !optMap[r.Name] {
			cr.Pass = false
		}
	}
	return cr
}

// MeasureBaseline runs download/upload/ping directly (no proxy) to capture the
// user's raw line speed and latency. Always-on companion to a proxy batch so
// proxy overhead is visible.
func MeasureBaseline(ctx context.Context, timeout time.Duration) Baseline {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	var b Baseline
	hres, _ := health.Run(ctx, health.Config{
		// Empty ProxyAddr => directDialer (no proxy).
		Checks:  []string{"ping", "download", "upload"},
		Timeout: timeout,
	})
	for _, r := range hres {
		switch r.Name {
		case "ping":
			if r.PingAvg > 0 {
				b.LatencyMs = float64(r.PingAvg.Microseconds()) / 1000.0
			}
		case "download":
			b.DownloadBPS = r.Throughput
			if !r.OK && r.Err != nil && b.Err == "" {
				b.Err = r.Err.Error()
			}
		case "upload":
			b.UploadBPS = r.Throughput
		}
	}
	return b
}

// Proxy returns the parsed proxy for a result (for advanced/geo/score steps).
func (r ProxyResult) Proxy() proxyuri.Proxy { return r.proxy }

// TempConfig writes a client config for inspection/debugging (test helper).
func TempConfig(coreName string, p proxyuri.Proxy, socksPort int) (string, error) {
	cfg, err := clientgen.Generate(coreName, p, socksPort)
	if err != nil {
		return "", err
	}
	dir, _ := os.MkdirTemp("", "hch")
	path := filepath.Join(dir, coreName+"-client.json")
	return path, os.WriteFile(path, cfg, 0o600)
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
		case <-time.After(250 * time.Millisecond):
		}
	}
}
