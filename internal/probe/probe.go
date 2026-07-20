// Package probe runs long-lived, low-frequency checks against a single proxy
// to simulate real user traffic (pageload / download / upload) and detect
// when that proxy becomes blocked over time. Unlike internal/health, which
// runs one check suite and returns, a probe.Runner keeps one proxy core alive
// across many ticks spaced by Config.Interval, recording one Sample per tick.
//
// Only one Runner may be active at a time (package-level guard): running two
// probes concurrently on different proxies would compete for the same
// upstream bandwidth and make both sets of measurements meaningless.
package probe

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"sync"
	"time"

	"github.com/hiddify/hiddify_config_health/internal/clientgen"
	"github.com/hiddify/hiddify_config_health/internal/core"
	"github.com/hiddify/hiddify_config_health/internal/health"
	"github.com/hiddify/hiddify_config_health/internal/proxyuri"
)

// Action is one kind of simulated traffic a probe tick can perform.
const (
	ActionPageload = "pageload"
	ActionDownload = "download"
	ActionUpload   = "upload"
	// ActionMix runs pageload+download+upload in a single tick.
	ActionMix = "mix"
)

// Config controls one probe session against a single proxy.
type Config struct {
	// ExampleDir / Variant identify the proxy under test, mirroring
	// store.Record's key so sessions and one-shot health runs share history.
	ExampleDir string
	Variant    string

	// ProxyURI is the share-link (vless://, vmess://, ...) to test. The
	// Runner parses it once and keeps one core process alive for the whole
	// session.
	ProxyURI string
	Core     string // "sing-box" or "xray"
	BinPath  string // core binary path; empty = resolve via env (see internal/core)

	// Interval is the nominal time between ticks (default 5m).
	Interval time.Duration
	// JitterPercent randomizes each tick's delay by up to this fraction
	// (default 0.15) so the probe is not a perfectly periodic, easily
	// fingerprinted traffic pattern.
	JitterPercent float64
	// Seed drives the jitter RNG. Callers should pass a stable-but-varying
	// value (e.g. time-derived at session start, or a request counter) since
	// this package does not read the clock itself.
	Seed int64

	Actions []string // subset of Action*; default [ActionDownload]

	PageloadURL   string
	DownloadURL   string
	DownloadBytes int64
	UploadURL     string
	UploadBytes   int64

	CheckTimeout time.Duration // per-check timeout inside a tick (default 15s)

	// BlockAfterFailures is the number of consecutive proxy-side failures
	// (with a healthy baseline) that mark the proxy as blocked (default 3).
	BlockAfterFailures int
}

func (c *Config) defaults() {
	if c.Interval <= 0 {
		c.Interval = 5 * time.Minute
	}
	if c.JitterPercent <= 0 {
		c.JitterPercent = 0.15
	}
	if len(c.Actions) == 0 {
		c.Actions = []string{ActionDownload}
	}
	if c.CheckTimeout <= 0 {
		c.CheckTimeout = 15 * time.Second
	}
	if c.BlockAfterFailures <= 0 {
		c.BlockAfterFailures = 3
	}
}

// Sample is the outcome of one probe tick.
type Sample struct {
	Timestamp time.Time
	OK        bool
	Err       string

	DownloadBPS float64
	UploadBPS   float64
	PageloadMs  float64

	// ConsecutiveFailures is the current losing streak length (proxy failed,
	// baseline healthy) as of this sample, 0 when OK or when a failure is
	// attributed to a local/network outage instead of the proxy.
	ConsecutiveFailures int
	// BaselineFailed marks a tick where the proxy failed AND the no-proxy
	// baseline also failed — i.e. likely the user's own network, not the
	// proxy being blocked. Does not count toward ConsecutiveFailures.
	BaselineFailed bool
	// CoreRestarted marks a tick where the core process was found dead and
	// was restarted before the checks ran. This is an infra event, not
	// evidence of blocking, and is reported separately from OK/Err.
	CoreRestarted bool
}

// Event is emitted once when the session's block state changes.
type Event struct {
	Timestamp time.Time
	Blocked   bool // true = became blocked, false = recovered
}

// Runner drives one probe session: starts a core, ticks health checks against
// it on Config.Interval (+/- jitter), and reports Sample/Event values via
// channels until Stop is called or ctx is done.
type Runner struct {
	cfg    Config
	proxy  proxyuri.Proxy
	rng    *rand.Rand
	core   core.Core
	socks  string
	cfgDir string

	samples chan Sample
	events  chan Event

	cancel context.CancelFunc
	done   chan struct{}

	streak int
}

var (
	activeMu sync.Mutex
	active   *Runner
)

// ErrAlreadyRunning is returned by Start when another probe session is active.
var ErrAlreadyRunning = fmt.Errorf("probe: a session is already running; stop it before starting another")

// New validates cfg and prepares a Runner. It does not start the core or the
// tick loop — call Start for that.
func New(cfg Config) (*Runner, error) {
	cfg.defaults()
	if cfg.ProxyURI == "" {
		return nil, fmt.Errorf("probe: ProxyURI is required")
	}
	p, err := proxyuri.ParseURI(cfg.ProxyURI)
	if err != nil {
		return nil, fmt.Errorf("probe: parse proxy uri: %w", err)
	}
	if core.New(cfg.Core, cfg.BinPath) == nil {
		return nil, fmt.Errorf("probe: core not registered: %s", cfg.Core)
	}
	return &Runner{
		cfg:     cfg,
		proxy:   *p,
		rng:     rand.New(rand.NewSource(cfg.Seed)),
		samples: make(chan Sample, 16),
		events:  make(chan Event, 4),
	}, nil
}

// Samples returns the channel of per-tick samples. Closed when the runner stops.
func (r *Runner) Samples() <-chan Sample { return r.samples }

// Events returns the channel of block/unblock events. Closed when the runner stops.
func (r *Runner) Events() <-chan Event { return r.events }

// Start claims the single global probe slot, starts the proxy core, and
// begins ticking in a background goroutine. Returns ErrAlreadyRunning if
// another session is active.
func (r *Runner) Start(ctx context.Context) error {
	activeMu.Lock()
	if active != nil {
		activeMu.Unlock()
		return ErrAlreadyRunning
	}
	active = r
	activeMu.Unlock()

	if err := r.startCore(ctx); err != nil {
		activeMu.Lock()
		active = nil
		activeMu.Unlock()
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.done = make(chan struct{})
	go r.loop(runCtx)
	return nil
}

// Stop ends the session: cancels the tick loop, waits for it to exit, stops
// the core, and releases the single-session slot.
func (r *Runner) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.done != nil {
		<-r.done
	}
	r.stopCore()
	activeMu.Lock()
	if active == r {
		active = nil
	}
	activeMu.Unlock()
}

// IsActive reports whether any probe session is currently running.
func IsActive() bool {
	activeMu.Lock()
	defer activeMu.Unlock()
	return active != nil
}

func (r *Runner) startCore(ctx context.Context) error {
	socksPort, err := freePort()
	if err != nil {
		return fmt.Errorf("probe: free port: %w", err)
	}
	cfgBytes, err := clientgen.Generate(r.cfg.Core, r.proxy, socksPort)
	if err != nil {
		return fmt.Errorf("probe: generate client config: %w", err)
	}
	tmpFile, err := os.CreateTemp("", "hch-probe-*.json")
	if err != nil {
		return err
	}
	r.cfgDir = tmpFile.Name()
	if _, err := tmpFile.Write(cfgBytes); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	c := core.New(r.cfg.Core, r.cfg.BinPath)
	if err := c.Start(ctx, r.cfgDir, nil); err != nil {
		os.Remove(r.cfgDir)
		return fmt.Errorf("probe: start core: %w", err)
	}
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", socksPort))
	if err := waitTCP(ctx, addr, r.cfg.CheckTimeout); err != nil {
		c.Stop()
		os.Remove(r.cfgDir)
		return fmt.Errorf("probe: core did not become ready: %w", err)
	}
	r.core = c
	r.socks = addr
	return nil
}

func (r *Runner) stopCore() {
	if r.core != nil {
		r.core.Stop()
		r.core = nil
	}
	if r.cfgDir != "" {
		os.Remove(r.cfgDir)
		r.cfgDir = ""
	}
}

// restartCore is called mid-session when the core process is found dead.
func (r *Runner) restartCore(ctx context.Context) error {
	r.stopCore()
	return r.startCore(ctx)
}

func (r *Runner) loop(ctx context.Context) {
	defer close(r.done)
	defer close(r.samples)
	defer close(r.events)

	for {
		s := r.tick(ctx)
		select {
		case r.samples <- s:
		case <-ctx.Done():
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(r.nextDelay()):
		}
	}
}

func (r *Runner) nextDelay() time.Duration {
	base := r.cfg.Interval
	jitter := float64(base) * r.cfg.JitterPercent * (2*r.rng.Float64() - 1)
	d := time.Duration(float64(base) + jitter)
	if d < time.Second {
		d = time.Second
	}
	return d
}

func (r *Runner) tick(ctx context.Context) Sample {
	s := Sample{Timestamp: time.Now()}

	// core.Core exposes no liveness probe, so detect death by the same signal
	// that would mean the SOCKS inbound is gone: a quick dial to it failing.
	if conn, err := net.DialTimeout("tcp", r.socks, 500*time.Millisecond); err != nil {
		if err := r.restartCore(ctx); err != nil {
			s.OK = false
			s.Err = "core restart failed: " + err.Error()
			return s
		}
		s.CoreRestarted = true
	} else {
		conn.Close()
	}

	checks := r.checksFor()
	hres, _ := health.Run(ctx, health.Config{
		ProxyAddr:     "socks5://" + r.socks,
		Checks:        checks,
		Timeout:       r.cfg.CheckTimeout,
		PageloadURL:   r.cfg.PageloadURL,
		DownloadURL:   r.cfg.DownloadURL,
		DownloadBytes: r.cfg.DownloadBytes,
		UploadURL:     r.cfg.UploadURL,
		UploadBytes:   r.cfg.UploadBytes,
	})

	ok := true
	var firstErr string
	for _, res := range hres {
		switch res.Name {
		case "download":
			s.DownloadBPS = res.Throughput
		case "upload":
			s.UploadBPS = res.Throughput
		case "pageload":
			s.PageloadMs = float64(res.Duration.Milliseconds())
		}
		if !res.OK && firstErr == "" && res.Err != nil {
			firstErr = res.Err.Error()
		}
		ok = ok && res.OK
	}
	s.OK = ok
	s.Err = firstErr

	if ok {
		if r.streak > 0 {
			r.streak = 0
			r.emitEvent(Event{Timestamp: s.Timestamp, Blocked: false})
		}
		return s
	}

	// Failure: distinguish "proxy blocked" from "user's own network is down"
	// by firing a cheap direct (no-proxy) baseline check.
	baseline, _ := health.Run(ctx, health.Config{
		Checks:  []string{"download"},
		Timeout: r.cfg.CheckTimeout,
	})
	baselineOK := len(baseline) > 0 && baseline[0].OK
	if !baselineOK {
		s.BaselineFailed = true
		return s
	}

	r.streak++
	s.ConsecutiveFailures = r.streak
	if r.streak == r.cfg.BlockAfterFailures {
		r.emitEvent(Event{Timestamp: s.Timestamp, Blocked: true})
	}
	return s
}

func (r *Runner) emitEvent(e Event) {
	select {
	case r.events <- e:
	default:
		// events channel is small and consumed promptly by the caller; drop
		// rather than block the tick loop if the consumer is slow.
	}
}

func (r *Runner) checksFor() []string {
	set := map[string]bool{}
	for _, a := range r.cfg.Actions {
		switch a {
		case ActionMix:
			set[ActionPageload], set[ActionDownload], set[ActionUpload] = true, true, true
		default:
			set[a] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
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

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
