// Package health tests connectivity through a SOCKS5 proxy or directly.
// Ported from cmd_health.go.
package health

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/socks"
)

// Config controls what health checks to run and against what targets.
type Config struct {
	// ProxyAddr is an optional socks5://host:port proxy.
	ProxyAddr string
	// Checks is a subset of: dns, tcp-dns, http, quic, ping, download, upload, speedtest.
	// "speedtest" expands to download+upload+ping.
	// If empty, defaults to ["http"].
	Checks []string

	Timeout     time.Duration
	DNSServer   string
	DNSTarget   string // hostname to resolve; defaults to "google.com"
	HTTPTarget  string // URL; defaults to http://connectivitycheck.gstatic.com/generate_204
	QUICTarget  string // host:port; defaults to cloudflare-dns.com / 1.1.1.1:443
	PingTarget  string // host:port; defaults to 1.1.1.1:443
	DownloadURL string
	UploadURL   string
	PingCount   int
	DownloadBytes int64
	UploadBytes   int64
}

func (c *Config) defaults() {
	if c.Timeout == 0 {
		c.Timeout = 15 * time.Second
	}
	if c.DNSServer == "" {
		c.DNSServer = "1.1.1.1:53"
	}
	if c.DNSTarget == "" {
		c.DNSTarget = "google.com"
	}
	if c.HTTPTarget == "" {
		c.HTTPTarget = "http://connectivitycheck.gstatic.com/generate_204"
	}
	if c.QUICTarget == "" {
		c.QUICTarget = "1.1.1.1:443"
	}
	if c.PingTarget == "" {
		c.PingTarget = "1.1.1.1:443"
	}
	if c.DownloadURL == "" {
		c.DownloadURL = "https://speed.cloudflare.com/__down?bytes=1000000"
	}
	if c.UploadURL == "" {
		c.UploadURL = "https://speed.cloudflare.com/__up"
	}
	if c.PingCount == 0 {
		c.PingCount = 5
	}
	if c.DownloadBytes == 0 {
		c.DownloadBytes = 1_000_000
	}
	if c.UploadBytes == 0 {
		c.UploadBytes = 512_000
	}
}

// Result is the outcome of one health check.
type Result struct {
	Name      string
	OK        bool
	// Optional marks a check whose failure is a warning, not a run failure.
	Optional  bool
	Duration  time.Duration
	Err       error
	// Extra human-readable detail (throughput, ping stats, …).
	Extra string

	// Populated by ping / jitter checks.
	PingAvg time.Duration
	PingMin time.Duration
	PingMax time.Duration
	Jitter  time.Duration

	// Populated by download / upload checks.
	Throughput float64 // bytes per second
}

// FormatThroughput formats bytes/s as MB/s, KB/s, or B/s.
func FormatThroughput(bps float64) string {
	switch {
	case bps >= 1<<20:
		return fmt.Sprintf("%.2fMB/s", bps/(1<<20))
	case bps >= 1<<10:
		return fmt.Sprintf("%.2fKB/s", bps/(1<<10))
	default:
		return fmt.Sprintf("%.0fB/s", bps)
	}
}

// FormatDuration rounds to ms (or µs for sub-ms).
func FormatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	return d.Round(time.Millisecond).String()
}

// Run executes the configured health checks and returns one Result per check.
func Run(ctx context.Context, cfg Config) ([]Result, error) {
	cfg.defaults()

	checks := expandChecks(cfg.Checks)
	if len(checks) == 0 {
		checks = []string{"http"}
	}

	d, err := newDialer(cfg.ProxyAddr)
	if err != nil {
		return nil, fmt.Errorf("health: dialer: %w", err)
	}

	var results []Result
	var pingSamples []time.Duration

	add := func(name string, fn func() (Result, error)) {
		start := time.Now()
		r, err := fn()
		if r.Name == "" {
			r.Name = name
		}
		if r.Duration == 0 {
			r.Duration = time.Since(start)
		}
		r.OK = err == nil
		r.Err = err
		results = append(results, r)
	}

	for _, check := range checks {
		switch check {
		case "dns":
			add("dns", func() (Result, error) {
				dur, err := testDNS(ctx, d, cfg)
				return Result{Duration: dur}, err
			})
		case "tcp-dns":
			add("tcp-dns", func() (Result, error) {
				dur, err := testTCPDNS(ctx, d, cfg)
				return Result{Duration: dur}, err
			})
		case "http":
			add("http", func() (Result, error) {
				dur, err := testHTTP(d, cfg)
				return Result{Duration: dur}, err
			})
		case "quic":
			add("quic", func() (Result, error) {
				dur, err := testQUIC(d, cfg)
				return Result{Duration: dur}, err
			})
		case "ping":
			add("ping", func() (Result, error) {
				samples, err := testPing(d, cfg)
				if err != nil {
					return Result{}, err
				}
				pingSamples = samples
				avg, mn, mx, jitter := summarizePing(samples)
				return Result{
					PingAvg: avg, PingMin: mn, PingMax: mx, Jitter: jitter,
					Extra: fmt.Sprintf("avg=%s min=%s max=%s", FormatDuration(avg), FormatDuration(mn), FormatDuration(mx)),
				}, nil
			})
		case "jitter":
			if len(pingSamples) == 0 {
				// Run ping first to collect samples.
				samples, err := testPing(d, cfg)
				if err == nil {
					pingSamples = samples
				}
			}
			if len(pingSamples) > 0 {
				_, _, _, jitter := summarizePing(pingSamples)
				results = append(results, Result{
					Name: "jitter", OK: true, Jitter: jitter,
					Extra: fmt.Sprintf("value=%s", FormatDuration(jitter)),
				})
			}
		case "download":
			add("download", func() (Result, error) {
				tp, dur, err := testDownload(d, cfg)
				if err != nil {
					return Result{}, err
				}
				return Result{Duration: dur, Throughput: tp, Extra: fmt.Sprintf("throughput=%s", FormatThroughput(tp))}, nil
			})
		case "upload":
			add("upload", func() (Result, error) {
				tp, dur, err := testUpload(d, cfg)
				if err != nil {
					return Result{}, err
				}
				return Result{Duration: dur, Throughput: tp, Extra: fmt.Sprintf("throughput=%s", FormatThroughput(tp))}, nil
			})
		default:
			// Treat unknown check names as custom tester executable paths.
			// The executable receives connection info via environment variables:
			//   HCH_PROXY_ADDR   socks5://host:port
			//   HCH_SERVER       server host
			//   HCH_PORT         server port
			//   HCH_TIMEOUT      timeout in seconds
			// Exit 0 = PASS, non-zero = FAIL.
			checkName := check
			add(checkName, func() (Result, error) {
				return runCustomCheck(ctx, checkName, cfg)
			})
		}
	}

	return results, nil
}

// runCustomCheck executes an arbitrary binary as a health check.
// The binary receives connection context via environment variables and its
// exit code determines pass (0) / fail (non-zero).
//
// Environment variables passed to the custom tester:
//
//	HCH_PROXY_ADDR   socks5://host:port (the client SOCKS proxy)
//	HCH_SOCKS_PORT   port number only (e.g. "1080")
//	HCH_SERVER       proxy host (e.g. "127.0.0.1")
//	HCH_TIMEOUT      timeout in seconds
func runCustomCheck(ctx context.Context, path string, cfg Config) (Result, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, path)
	env := []string{
		"HCH_PROXY_ADDR=" + cfg.ProxyAddr,
		"HCH_TIMEOUT=" + strconv.Itoa(int(cfg.Timeout.Seconds())),
	}
	if u, err := url.Parse(cfg.ProxyAddr); err == nil {
		host, port, _ := net.SplitHostPort(u.Host)
		env = append(env, "HCH_SERVER="+host, "HCH_SOCKS_PORT="+port)
	}
	cmd.Env = append(cmd.Environ(), env...)

	out, err := cmd.CombinedOutput()
	extra := strings.TrimSpace(string(out))
	if len(extra) > 200 {
		extra = extra[:200] + "…"
	}
	if err != nil {
		return Result{Extra: extra}, fmt.Errorf("custom check %q: %w", path, err)
	}
	return Result{Extra: extra}, nil
}

func expandChecks(checks []string) []string {
	var out []string
	for _, c := range checks {
		if c == "speedtest" {
			out = append(out, "download", "upload", "ping")
		} else {
			out = append(out, c)
		}
	}
	return out
}

// --- dialer ---

type dialer interface {
	Dial(network, address string) (net.Conn, error)
}

type directDialer struct{}

func (directDialer) Dial(network, address string) (net.Conn, error) {
	return N.SystemDialer.DialContext(context.Background(), network, metadata.ParseSocksaddr(address))
}

type socksDialer struct{ client *socks.Client }

func (d socksDialer) Dial(network, address string) (net.Conn, error) {
	return d.client.DialContext(context.Background(), network, metadata.ParseSocksaddr(address))
}

func newDialer(proxyAddr string) (dialer, error) {
	if proxyAddr == "" {
		return directDialer{}, nil
	}
	if !strings.Contains(proxyAddr, "://") {
		proxyAddr = "socks://" + proxyAddr
	}
	client, err := socks.NewClientFromURL(N.SystemDialer, proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("parse proxy %q: %w", proxyAddr, err)
	}
	return socksDialer{client: client}, nil
}

// --- DNS ---

func testDNS(ctx context.Context, d dialer, cfg Config) (time.Duration, error) {
	return doDNS(d, cfg, "udp")
}

func testTCPDNS(ctx context.Context, d dialer, cfg Config) (time.Duration, error) {
	return doDNS(d, cfg, "tcp")
}

func doDNS(d dialer, cfg Config, network string) (time.Duration, error) {
	start := time.Now()
	server := cfg.DNSServer
	switch {
	case strings.HasPrefix(server, "udp://"):
		server = strings.TrimPrefix(server, "udp://")
		network = "udp"
	case strings.HasPrefix(server, "tcp://"):
		server = strings.TrimPrefix(server, "tcp://")
		network = "tcp"
	}
	if !strings.Contains(server, ":") {
		server = net.JoinHostPort(server, "53")
	}

	query := buildDNSQuery(cfg.DNSTarget)
	conn, err := d.Dial(network, server)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(cfg.Timeout))

	payload := query
	if network == "tcp" {
		payload = make([]byte, 2+len(query))
		binary.BigEndian.PutUint16(payload[0:2], uint16(len(query)))
		copy(payload[2:], query)
	}
	if _, err = conn.Write(payload); err != nil {
		return 0, err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return 0, err
	}
	if network == "tcp" {
		if n < 2 {
			return 0, fmt.Errorf("invalid DNS response")
		}
		length := int(binary.BigEndian.Uint16(buf[0:2]))
		if length+2 > n {
			return 0, fmt.Errorf("invalid DNS response")
		}
		buf = buf[2 : 2+length]
		n = length
	}
	if n < 12 || buf[3]&0x0F != 0 {
		return 0, fmt.Errorf("invalid DNS response rcode=%d", buf[3]&0x0F)
	}
	return time.Since(start), nil
}

func buildDNSQuery(name string) []byte {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[0:2], 0xabcd)
	binary.BigEndian.PutUint16(q[2:4], 0x0100)
	binary.BigEndian.PutUint16(q[4:6], 1)
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			continue
		}
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	return append(q, 0x00, 0x00, 0x01, 0x00, 0x01)
}

// --- HTTP ---

func testHTTP(d dialer, cfg Config) (time.Duration, error) {
	start := time.Now()
	u, err := url.Parse(cfg.HTTPTarget)
	if err != nil {
		return 0, err
	}
	client := httpClientFor(d, u, cfg.Timeout)
	req, _ := http.NewRequest(http.MethodHead, cfg.HTTPTarget, nil)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP %s", resp.Status)
	}
	return time.Since(start), nil
}

func httpClientFor(d dialer, u *url.URL, timeout time.Duration) *http.Client {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	addr := net.JoinHostPort(u.Hostname(), port)
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return d.Dial("tcp", addr)
			},
			TLSClientConfig:   &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12},
			DisableKeepAlives: true,
		},
	}
}

// --- QUIC ---

const h3Proto = "h3"

func testQUIC(d dialer, cfg Config) (time.Duration, error) {
	start := time.Now()
	addr := cfg.QUICTarget
	if !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, "443")
	}
	host, _, _ := net.SplitHostPort(addr)

	conn, err := d.Dial("udp", addr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	pconn := &udpConn{Conn: conn, raddr: conn.RemoteAddr()}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	tr := &quic.Transport{Conn: pconn}
	qconn, err := tr.Dial(ctx, pconn.raddr, &tls.Config{
		ServerName: host,
		NextProtos: []string{h3Proto},
		MinVersion: tls.VersionTLS13,
	}, &quic.Config{HandshakeIdleTimeout: cfg.Timeout, MaxIdleTimeout: cfg.Timeout})
	if err != nil {
		return 0, err
	}
	_ = qconn.CloseWithError(0, "done")
	return time.Since(start), nil
}

type udpConn struct {
	net.Conn
	raddr net.Addr
}

func (c *udpConn) ReadFrom(p []byte) (int, net.Addr, error) { n, err := c.Conn.Read(p); return n, c.raddr, err }
func (c *udpConn) WriteTo(p []byte, _ net.Addr) (int, error) { return c.Conn.Write(p) }
func (c *udpConn) LocalAddr() net.Addr { return &net.UDPAddr{} }
func (c *udpConn) SetReadBuffer(int) error  { return nil }
func (c *udpConn) SetWriteBuffer(int) error { return nil }

// --- ping ---

func testPing(d dialer, cfg Config) ([]time.Duration, error) {
	target := cfg.PingTarget
	if !strings.Contains(target, ":") {
		target = net.JoinHostPort(target, "443")
	}
	out := make([]time.Duration, 0, cfg.PingCount)
	for i := 0; i < cfg.PingCount; i++ {
		start := time.Now()
		conn, err := d.Dial("tcp", target)
		if err != nil {
			return nil, err
		}
		conn.Close()
		out = append(out, time.Since(start))
	}
	return out, nil
}

func summarizePing(samples []time.Duration) (avg, mn, mx, jitter time.Duration) {
	if len(samples) == 0 {
		return
	}
	mn, mx = samples[0], samples[0]
	var sum float64
	for _, s := range samples {
		sum += float64(s)
		if s < mn {
			mn = s
		}
		if s > mx {
			mx = s
		}
	}
	avg = time.Duration(sum / float64(len(samples)))
	if len(samples) < 2 {
		return
	}
	mean := sum / float64(len(samples))
	var variance float64
	for _, s := range samples {
		d := float64(s) - mean
		variance += d * d
	}
	jitter = time.Duration(math.Sqrt(variance / float64(len(samples))))
	return
}

// --- download / upload ---

func testDownload(d dialer, cfg Config) (throughput float64, duration time.Duration, err error) {
	u, err := url.Parse(cfg.DownloadURL)
	if err != nil {
		return 0, 0, err
	}
	start := time.Now()
	resp, err := httpClientFor(d, u, cfg.Timeout).Get(cfg.DownloadURL)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return 0, 0, err
	}
	if n == 0 {
		n = cfg.DownloadBytes
	}
	duration = time.Since(start)
	return float64(n) / duration.Seconds(), duration, nil
}

func testUpload(d dialer, cfg Config) (throughput float64, duration time.Duration, err error) {
	u, err := url.Parse(cfg.UploadURL)
	if err != nil {
		return 0, 0, err
	}
	payload := make([]byte, cfg.UploadBytes)
	start := time.Now()
	resp, err := httpClientFor(d, u, cfg.Timeout).Post(cfg.UploadURL, "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	duration = time.Since(start)
	return float64(len(payload)) / duration.Seconds(), duration, nil
}
