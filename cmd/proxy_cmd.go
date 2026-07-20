package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hiddify/hiddify_config_health/internal/probe"
	"github.com/hiddify/hiddify_config_health/internal/score"
	"github.com/hiddify/hiddify_config_health/internal/store"
	"github.com/hiddify/hiddify_config_health/internal/telemetry"
	"github.com/hiddify/hiddify_config_health/pkg/proxytest"
)

// scoreCore computes the 0–100 score for one core result (geoFlagged from the
// owning proxy's offline ASN lookup).
func scoreCore(cr proxytest.CoreResult, geoFlagged bool) int {
	return score.Score(score.Input{
		Pass:        cr.Pass,
		Supported:   cr.Supported,
		Fingerprint: cr.Fingerprint,
		Checks:      cr.Checks,
		GeoFlagged:  geoFlagged,
	}).Total
}

var (
	flagFull      bool
	flagProxyFile string
	flagStdin     bool
	flagSubmit    bool
	flagEndpoint  string
)

// optionsFromFlags builds proxytest.Options from the shared CLI flags.
func optionsFromFlags() proxytest.Options {
	o := proxytest.Options{
		Full:    flagFull,
		Timeout: time.Duration(flagTimeout) * time.Second,
	}
	if flagCore != "" {
		o.Cores = []string{flagCore}
	}
	// Honour explicit binary paths via env (core.New already reads SINGBOX_BIN
	// / XRAY_BIN); also accept XRAY_CLIENT_PATH used elsewhere in this repo.
	bins := map[string]string{}
	if p := os.Getenv("SINGBOX_BIN"); p != "" {
		bins["sing-box"] = p
	}
	if p := firstEnv("XRAY_BIN", "XRAY_CLIENT_PATH"); p != "" {
		bins["xray"] = p
	}
	if len(bins) > 0 {
		o.Binaries = bins
	}
	return o
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func proxyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "proxy <uri>",
		Short: "Test a single proxy share-link (vless/vmess/ss/trojan/hysteria2/tuic) on each core",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := proxytest.TestProxy(cmd.Context(), args[0], optionsFromFlags())
			if err != nil {
				return err
			}
			return reportProxies(cmd.Context(), []proxytest.ProxyResult{res})
		},
	}
	addProxyFlags(c)
	return c
}

func proxiesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "proxies",
		Short: "Test a list of proxy share-links from a file (-f) or stdin (--stdin)",
		RunE: func(cmd *cobra.Command, args []string) error {
			var text string
			switch {
			case flagProxyFile != "":
				b, err := os.ReadFile(flagProxyFile)
				if err != nil {
					return err
				}
				text = string(b)
			case flagStdin:
				b, _ := readAll(os.Stdin)
				text = string(b)
			default:
				return fmt.Errorf("provide -f <file> or --stdin")
			}
			uris := splitLines(text)
			results := proxytest.TestProxies(cmd.Context(), uris, optionsFromFlags())
			return reportProxies(cmd.Context(), results)
		},
	}
	c.Flags().StringVarP(&flagProxyFile, "file", "f", "", "file with one proxy URI per line")
	c.Flags().BoolVar(&flagStdin, "stdin", false, "read proxy URIs from stdin")
	addProxyFlags(c)
	return c
}

func subCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sub <subscription-url>",
		Short: "Fetch a subscription link and test every proxy in it on each core",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			results, errs := proxytest.TestSubscription(cmd.Context(), args[0], optionsFromFlags())
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, "warn:", e)
			}
			if len(results) == 0 {
				return fmt.Errorf("no proxies parsed from subscription")
			}
			return reportProxies(cmd.Context(), results)
		},
	}
	addProxyFlags(c)
	return c
}

// probeCmdCLI runs a long-lived probe session against a single proxy from the
// terminal: starts one core, ticks health checks on an interval, and prints
// one line per sample until interrupted (Ctrl-C) or the proxy is detected
// blocked. This is the CLI counterpart to the web UI's "Probe Time" panel —
// same internal/probe.Runner, same single-session semantics.
func probeCmdCLI() *cobra.Command {
	var (
		probeCore        string
		probeInterval    int
		probeActions     []string
		probeBlockAfterN int
		probeDownloadURL string
		probeUploadURL   string
		probePageloadURL string
	)
	c := &cobra.Command{
		Use:   "probe <proxy-uri>",
		Short: "Repeatedly probe ONE proxy over time to measure speed and detect blocking",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := probe.Config{
				ExampleDir:         "cli-probe",
				ProxyURI:           args[0],
				Core:               probeCore,
				Interval:           time.Duration(probeInterval) * time.Second,
				Actions:            probeActions,
				BlockAfterFailures: probeBlockAfterN,
				DownloadURL:        probeDownloadURL,
				UploadURL:          probeUploadURL,
				PageloadURL:        probePageloadURL,
				Seed:               time.Now().UnixNano(),
			}
			r, err := probe.New(cfg)
			if err != nil {
				return err
			}

			db, _ := store.Open(flagDBPath)
			if db != nil {
				defer db.Close()
			}
			var sessionID int64
			if db != nil {
				sessionID, _ = db.StartProbeSession(cfg.ExampleDir, "", cfg.Interval, cfg.Actions)
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if err := r.Start(ctx); err != nil {
				return err
			}
			fmt.Printf("Probing %s every %s — Ctrl-C to stop.\n"+
				"Only ONE proxy can be probed at a time; don't route other traffic through it while this runs.\n\n",
				args[0], cfg.Interval)

			samples, events := r.Samples(), r.Events()
			for samples != nil || events != nil {
				select {
				case s, ok := <-samples:
					if !ok {
						samples = nil
						continue
					}
					printProbeSample(s)
					if db != nil {
						_ = db.SaveProbeSample(sessionID, store.ProbeSample{
							Timestamp: s.Timestamp, OK: s.OK, DownloadBPS: s.DownloadBPS,
							UploadBPS: s.UploadBPS, PageloadMs: s.PageloadMs, Err: s.Err,
							ConsecutiveFailures: s.ConsecutiveFailures, BaselineFailed: s.BaselineFailed,
							CoreRestarted: s.CoreRestarted,
						})
					}
				case e, ok := <-events:
					if !ok {
						events = nil
						continue
					}
					if e.Blocked {
						fmt.Printf(">>> BLOCKED at %s <<<\n", e.Timestamp.Format(time.RFC3339))
						if db != nil {
							_ = db.MarkProbeBlocked(sessionID, e.Timestamp)
						}
					} else {
						fmt.Printf(">>> recovered at %s <<<\n", e.Timestamp.Format(time.RFC3339))
					}
				case <-ctx.Done():
					r.Stop()
					if db != nil {
						_ = db.StopProbeSession(sessionID)
					}
					fmt.Println("\nstopped.")
					return nil
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&probeCore, "core", "sing-box", "core to run the proxy on (sing-box | xray)")
	c.Flags().IntVar(&probeInterval, "interval", 300, "seconds between probes")
	c.Flags().StringSliceVar(&probeActions, "actions", []string{"download"}, "comma-separated: pageload,download,upload,mix")
	c.Flags().IntVar(&probeBlockAfterN, "block-after", 3, "consecutive proxy failures (with healthy baseline) before reporting blocked")
	c.Flags().StringVar(&probeDownloadURL, "download-url", "", "override the download-test URL")
	c.Flags().StringVar(&probeUploadURL, "upload-url", "", "override the upload-test URL")
	c.Flags().StringVar(&probePageloadURL, "pageload-url", "", "override the pageload-test URL")
	return c
}

func printProbeSample(s probe.Sample) {
	t := s.Timestamp.Format("15:04:05")
	status := "OK"
	if !s.OK {
		status = "FAIL"
	}
	extra := ""
	if s.OK && s.DownloadBPS > 0 {
		extra += fmt.Sprintf(" ↓%s", fmtBPS(s.DownloadBPS))
	}
	if s.OK && s.UploadBPS > 0 {
		extra += fmt.Sprintf(" ↑%s", fmtBPS(s.UploadBPS))
	}
	if s.CoreRestarted {
		extra += " [core restarted]"
	}
	if s.BaselineFailed {
		extra += " [your network is down — not counted toward block]"
	}
	if !s.OK && s.ConsecutiveFailures > 0 {
		extra += fmt.Sprintf(" (failure #%d)", s.ConsecutiveFailures)
	}
	if !s.OK && s.Err != "" {
		extra += " — " + s.Err
	}
	fmt.Printf("%s  %-4s%s\n", t, status, extra)
}

func addProxyFlags(c *cobra.Command) {
	c.Flags().BoolVar(&flagJSON, "json", false, "emit machine-readable JSON report (for CI)")
	c.Flags().BoolVar(&flagFull, "full", false, "run the full advanced suite (load/entropy/probe/tls-fingerprint)")
	c.Flags().StringVar(&flagCore, "core", "", "only test this core (sing-box | xray)")
	c.Flags().BoolVar(&flagSubmit, "submit", false, "opt-in: submit ANONYMOUS, PII-stripped results to the central server")
	c.Flags().StringVar(&flagEndpoint, "endpoint", telemetry.DefaultEndpoint, "central health-check server (with --submit)")
}

// proxyJSON is the CI-friendly per-(proxy,core) row.
type proxyJSON struct {
	Tag       string  `json:"tag"`
	Protocol  string  `json:"protocol"`
	Server    string  `json:"server"`
	Port      int     `json:"port"`
	Core      string  `json:"core"`
	Supported bool    `json:"supported"`
	Pass      bool    `json:"pass"`
	Censor    string  `json:"censor,omitempty"`
	LatencyMs float64 `json:"latency_ms,omitempty"`
	Score     int     `json:"score"`
	Error     string  `json:"error,omitempty"`
}

// reportProxies prints (console or JSON), and persists each result to the DB
// (privacy: only tag/server/port/metrics — never the raw URI/credentials).
func reportProxies(ctx context.Context, results []proxytest.ProxyResult) error {
	db, _ := store.Open(flagDBPath)
	if db != nil {
		defer db.Close()
	}

	var rows []proxyJSON
	anyConnected := false
	for _, pr := range results {
		for _, cr := range pr.PerCore {
			sc := scoreCore(cr, pr.Geo.Flagged)
			rows = append(rows, proxyJSON{
				Tag: pr.Tag, Protocol: pr.Protocol, Server: pr.Server, Port: pr.Port,
				Core: cr.Core, Supported: cr.Supported, Pass: cr.Pass,
				Censor: cr.Fingerprint.Verdict, LatencyMs: latencyOf(cr), Score: sc, Error: cr.Err,
			})
			if cr.Pass {
				anyConnected = true
			}
			if db != nil && cr.Supported {
				_, _ = db.Save(store.Record{
					ExampleDir:  "sub:" + safeTag(pr.Tag, pr.Server),
					Name:        pr.Protocol + " " + safeTag(pr.Tag, pr.Server),
					Variant:     cr.Core,
					Pass:        cr.Pass,
					Checks:      cr.Checks,
					Fingerprint: cr.Fingerprint,
					StartedAt:   time.Now(),
				})
			}
		}
	}

	// Baseline (no-proxy) line speed — always measured so proxy overhead is
	// visible. Runs once per invocation.
	baseline := proxytest.MeasureBaseline(ctx, 15*time.Second)

	if flagJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"baseline": baseline, "proxies": rows})
	} else {
		printBaseline(baseline)
		printProxyTable(rows)
	}

	// Opt-in anonymous submission to the central server.
	if flagSubmit {
		if err := submitResults(ctx, results, baseline); err != nil {
			fmt.Fprintln(os.Stderr, "submit failed:", err)
		}
	}

	if !anyConnected {
		return fmt.Errorf("no proxy connected on any core")
	}
	return nil
}

// submitResults scrubs every result to its anonymous structural Shape and
// submits the batch (opt-in). NO credentials, server IP/host, SNI, or tags
// leave the machine — only protocol/transport/security shape + metrics.
func submitResults(ctx context.Context, results []proxytest.ProxyResult, baseline proxytest.Baseline) error {
	anon := telemetry.AnonID()
	batch := telemetry.Batch{
		AnonID: anon,
		Baseline: &telemetry.Baseline{
			LatencyMs:   baseline.LatencyMs,
			DownloadBPS: baseline.DownloadBPS,
			UploadBPS:   baseline.UploadBPS,
		},
	}
	for _, pr := range results {
		shape := telemetry.Scrub(pr.Proxy())
		for _, cr := range pr.PerCore {
			if !cr.Supported {
				continue
			}
			batch.Reports = append(batch.Reports, telemetry.Report{
				AnonID:    anon,
				Shape:     shape,
				Core:      cr.Core,
				Pass:      cr.Pass,
				Censor:    cr.Fingerprint.Verdict,
				Score:     scoreCore(cr, pr.Geo.Flagged),
				LatencyMs: latencyOf(cr),
				Country:   pr.Geo.Country,
				ASN:       pr.Geo.ASN,
				ClientVer: Version,
				Ts:        time.Now().Unix(),
			})
		}
	}
	if len(batch.Reports) == 0 {
		return fmt.Errorf("nothing to submit")
	}
	resp, err := telemetry.Submit(ctx, flagEndpoint, batch)
	if err != nil {
		return err
	}
	fmt.Printf("\n✓ Submitted %d anonymous results. Your private link:\n  %s\n", len(batch.Reports), resp.Link)
	return nil
}

func printBaseline(b proxytest.Baseline) {
	fmt.Printf("baseline (no proxy): ↓%s ↑%s latency=%.1fms %s\n\n",
		fmtBPS(b.DownloadBPS), fmtBPS(b.UploadBPS), b.LatencyMs, b.Err)
}

func fmtBPS(bps float64) string {
	switch {
	case bps >= 1<<20:
		return fmt.Sprintf("%.1fMB/s", bps/(1<<20))
	case bps >= 1<<10:
		return fmt.Sprintf("%.0fKB/s", bps/(1<<10))
	default:
		return "-"
	}
}

func printProxyTable(rows []proxyJSON) {
	fmt.Printf("%-22s %-10s %-9s %-7s %-12s %-9s %5s  %s\n",
		"TAG", "PROTOCOL", "CORE", "PASS", "CENSOR", "LATENCY", "SCORE", "NOTE")
	for _, r := range rows {
		pass := "FAIL"
		if !r.Supported {
			pass = "n/a"
		} else if r.Pass {
			pass = "PASS"
		}
		lat := "-"
		if r.LatencyMs > 0 {
			lat = fmt.Sprintf("%.1fms", r.LatencyMs)
		}
		note := r.Error
		fmt.Printf("%-22.22s %-10s %-9s %-7s %-12s %-9s %5d  %s\n",
			r.Tag, r.Protocol, r.Core, pass, r.Censor, lat, r.Score, note)
	}
}

func latencyOf(cr proxytest.CoreResult) float64 {
	for _, c := range cr.Checks {
		if c.Name == "ping" && c.PingAvg > 0 {
			return float64(c.PingAvg.Microseconds()) / 1000.0
		}
	}
	return 0
}

// safeTag returns a label that never leaks credentials (tag or host fallback).
func safeTag(tag, server string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return server
	}
	return tag
}

func splitLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

func readAll(f *os.File) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := f.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}
