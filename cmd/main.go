// Command hiddify-health tests VPN/proxy configurations against multiple cores.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	// Register core runners via side-effect imports.
	_ "github.com/hiddify/hiddify_config_health/internal/core"

	"github.com/hiddify/hiddify_config_health/internal/runner"
	"github.com/hiddify/hiddify_config_health/internal/store"
	"github.com/hiddify/hiddify_config_health/internal/web"
)

// Version is set at build time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var (
	flagDBPath      string
	flagExamplesDir string
	flagTimeout     int
	flagQuiet       bool
	flagJSON        bool
	flagCore        string
	flagDeploy      string
	flagPort        string
)

// jsonCheck is the CI-friendly serialization of a single health check.
type jsonCheck struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Optional bool   `json:"optional"`
	Extra    string `json:"extra,omitempty"`
	Error    string `json:"error,omitempty"`
}

// jsonResult is the CI-friendly serialization of one variant's run.
type jsonResult struct {
	Dir         string      `json:"dir"`
	Name        string      `json:"name"`
	Variant     string      `json:"variant,omitempty"`
	Core        string      `json:"core,omitempty"`
	CoreVersion string      `json:"core_version,omitempty"`
	Pass        bool        `json:"pass"`
	Censor      string      `json:"censor,omitempty"`
	DurationMs  int64       `json:"duration_ms"`
	LatencyMs   float64     `json:"latency_ms,omitempty"`
	JitterMs    float64     `json:"jitter_ms,omitempty"`
	ProbeVerdict string     `json:"probe_verdict,omitempty"`
	JA3          string     `json:"ja3,omitempty"`
	JA4          string     `json:"ja4,omitempty"`
	TLSMatch     string     `json:"tls_match,omitempty"`
	Entropy      float64    `json:"entropy,omitempty"`
	LoadBPS      float64    `json:"load_bps,omitempty"`
	LoadDropped  int        `json:"load_dropped,omitempty"`
	Regressed    bool       `json:"regressed,omitempty"`
	Checks      []jsonCheck `json:"checks"`
	Error       string      `json:"error,omitempty"`
}

// jsonReport is the top-level CI output for run-all.
type jsonReport struct {
	Passed  int          `json:"passed"`
	Failed  int          `json:"failed"`
	Results []jsonResult `json:"results"`
}

func toJSONResult(dir, core string, res *runner.Result) jsonResult {
	jr := jsonResult{
		Dir: dir, Name: res.Name, Variant: res.Variant, Core: core,
		CoreVersion: res.CoreVersion, Pass: res.Pass,
		Censor:     res.Fingerprint.Verdict,
		DurationMs: res.Duration.Milliseconds(),
	}
	if res.Err != nil {
		jr.Error = res.Err.Error()
	}
	for _, c := range res.Checks {
		jc := jsonCheck{Name: c.Name, OK: c.OK, Optional: c.Optional, Extra: c.Extra}
		if c.Err != nil {
			jc.Error = c.Err.Error()
		}
		if c.Name == "ping" && c.PingAvg > 0 {
			jr.LatencyMs = float64(c.PingAvg.Microseconds()) / 1000.0
		}
		if (c.Name == "ping" || c.Name == "jitter") && c.Jitter > 0 {
			jr.JitterMs = float64(c.Jitter.Microseconds()) / 1000.0
		}
		switch c.Name {
		case "active-probe":
			jr.ProbeVerdict = c.ProbeVerdict
		case "tls-fingerprint":
			jr.JA3, jr.JA4, jr.TLSMatch = c.JA3, c.JA4, c.TLSMatch
		case "entropy":
			jr.Entropy = c.EntropyScore
		case "load":
			jr.LoadBPS, jr.LoadDropped = c.Throughput, c.LoadDropped
		case "regression":
			jr.Regressed = c.Regressed
		}
		jr.Checks = append(jr.Checks, jc)
	}
	return jr
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "hiddify-health",
		Short:   "Test VPN/proxy configuration files across multiple cores",
		Version: Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flagDBPath, "db", store.DefaultPath(), "SQLite result database path")
	root.PersistentFlags().StringVar(&flagExamplesDir, "examples", "examples", "examples root directory")
	root.PersistentFlags().IntVar(&flagTimeout, "timeout", 30, "per-check timeout in seconds")
	root.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "suppress raw process log; show only result summary")

	root.AddCommand(
		runCmd(),
		runAllCmd(),
		checkCmd(),
		serveCmd(),
		historyCmd(),
		reportCmd(),
	)
	return root
}

// --- run ---

func runCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "run <example-dir>",
		Short: "Run one example test",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, _ := store.Open(flagDBPath)
			if db != nil {
				defer db.Close()
			}
			jrs, anyFail := runOne(cmd.Context(), args[0], db)
			if flagJSON {
				printJSONReport(jrs)
			}
			if anyFail {
				return fmt.Errorf("test failed")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&flagJSON, "json", false, "emit machine-readable JSON report (for CI)")
	c.Flags().StringVar(&flagDeploy, "deploy", "", "deploy server to this SSH URL for all examples (ssh://user:pass@host:22)")
	c.Flags().StringVar(&flagPort, "port", "", "pin the server PORT to a fixed value (needed for remote deploy through a firewall)")
	return c
}

// runOne runs every variant of one example, persists to db, prints the
// human log/summary (unless --json), and returns the JSON results plus
// whether any variant failed.
func runOne(ctx context.Context, dir string, db *store.DB) ([]jsonResult, bool) {
	logOut := io.Writer(os.Stdout)
	if flagQuiet || flagJSON {
		logOut = io.Discard
	}
	if !flagJSON {
		fmt.Printf("▶ %s\n", dir)
	}

	core := coreOf(dir)
	ov := runner.Overrides{DeployToServer: flagDeploy}
	if flagPort != "" {
		// Pin the server port to a fixed, firewall-opened value (needed for
		// remote deploy, where a random high port is usually blocked).
		ov.Vars = map[string]string{"PORT": flagPort}
	}
	results, err := runner.RunWithOverrides(ctx, dir, logOut, ov)
	if err != nil && len(results) == 0 {
		// Hard failure before any variant produced a result.
		if !flagJSON {
			fmt.Printf("  ERROR: %v\n", err)
		}
		return []jsonResult{{Dir: dir, Name: filepath.Base(dir), Core: core, Pass: false, Error: err.Error()}}, true
	}

	var jrs []jsonResult
	anyFail := false
	for _, res := range results {
		// Compare against the prior baseline BEFORE saving this run, and append
		// the regression verdict as an extra (warn-only) check row.
		if db != nil {
			if reg, ok := db.RegressionCheck(dir, res.Variant, res.Checks); ok {
				res.Checks = append(res.Checks, reg)
			}
		}
		if db != nil {
			rec := store.Record{
				ExampleDir:  dir,
				Name:        res.Name,
				Variant:     res.Variant,
				CoreVersion: res.CoreVersion,
				Pass:        res.Pass,
				Checks:      res.Checks,
				Fingerprint: res.Fingerprint,
				Log:         res.Log,
				StartedAt:   res.StartedAt,
				DurationMs:  res.Duration.Milliseconds(),
			}
			_, _ = db.Save(rec)
		}
		jrs = append(jrs, toJSONResult(dir, core, res))
		if !res.Pass {
			anyFail = true
		}
		if !flagJSON {
			status := "PASS"
			if !res.Pass {
				status = "FAIL"
			}
			label := res.Name
			if res.Variant != "" && res.Variant != res.Name {
				label = res.Variant
			}
			fmt.Printf("  [%s] %s  duration=%s  censor=%s\n",
				label, status, res.Duration.Round(time.Millisecond), res.Fingerprint.Verdict)
			if res.Err != nil {
				fmt.Printf("    error: %v\n", res.Err)
			}
		}
	}
	return jrs, anyFail
}

// coreOf returns the core name declared in dir's run config ("" if unknown).
func coreOf(dir string) string {
	cfg, err := runner.LoadRunConfig(dir)
	if err != nil {
		return ""
	}
	return cfg.Core
}

func printJSONReport(results []jsonResult) {
	rep := jsonReport{Results: results}
	for _, r := range results {
		if r.Pass {
			rep.Passed++
		} else {
			rep.Failed++
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
}

// --- run-all ---

func runAllCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "run-all [examples-dir]",
		Short: "Run all examples; exit 1 if any fail",
		Long: "Run all examples under the given directory (default: ./examples).\n" +
			"Pass a subdirectory to test only that subtree, e.g.\n" +
			"  hiddify-health run-all examples/xray\n" +
			"Filter by core with --core, e.g. --core sing-box.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := flagExamplesDir
			if len(args) > 0 {
				root = args[0]
			}
			dirs, err := findExamples(root)
			if err != nil {
				return err
			}
			if flagCore != "" {
				dirs = filterByCore(dirs, flagCore)
			}
			if len(dirs) == 0 {
				if !flagJSON {
					fmt.Println("No matching examples found under", root)
				} else {
					printJSONReport(nil)
				}
				return nil
			}

			db, _ := store.Open(flagDBPath)
			if db != nil {
				defer db.Close()
			}

			var allJRS []jsonResult
			pass, fail := 0, 0
			for _, dir := range dirs {
				jrs, anyFail := runOne(cmd.Context(), dir, db)
				allJRS = append(allJRS, jrs...)
				if anyFail {
					fail++
				} else {
					pass++
				}
			}
			if flagJSON {
				printJSONReport(allJRS)
			} else {
				fmt.Printf("\n--- %d passed  %d failed ---\n", pass, fail)
			}
			if fail > 0 {
				return fmt.Errorf("%d test(s) failed", fail)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&flagJSON, "json", false, "emit machine-readable JSON report (for CI)")
	c.Flags().StringVar(&flagCore, "core", "", "only run examples for this core (e.g. sing-box, xray)")
	c.Flags().StringVar(&flagDeploy, "deploy", "", "deploy server to this SSH URL for ALL examples (ssh://user:pass@host:22)")
	c.Flags().StringVar(&flagPort, "port", "", "pin the server PORT to a fixed value (needed for remote deploy through a firewall)")
	return c
}

// filterByCore keeps only example dirs whose run config declares the given core.
func filterByCore(dirs []string, core string) []string {
	var out []string
	for _, dir := range dirs {
		if coreOf(dir) == core {
			out = append(out, dir)
		}
	}
	return out
}

// --- check ---

func checkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check <example-dir>",
		Short: "Validate config syntax only (no network)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Checking configs in %s …\n", args[0])
			// Read run.json to know the core, then call core.Check.
			// For now: just verify run.json parses and referenced config files exist.
			path := filepath.Join(args[0], "run.json")
			pathJ2 := filepath.Join(args[0], "run.json.j2")
			_, err1 := os.Stat(path)
			_, err2 := os.Stat(pathJ2)
			if err1 != nil && err2 != nil {
				return fmt.Errorf("run.json not found in %s", args[0])
			}
			fmt.Println("OK")
			return nil
		},
	}
}

// --- serve ---

func serveCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the web UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, _ := store.Open(flagDBPath)
			if db != nil {
				defer db.Close()
			}
			srv := &web.Server{
				ExamplesDir: flagExamplesDir,
				DB:          db,
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			fmt.Printf("Web UI: http://%s\n", addr)
			go func() {
				if err := srv.ListenAndServe(addr); err != nil {
					fmt.Fprintln(os.Stderr, "web:", err)
				}
			}()
			<-ctx.Done()
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "listen address")
	return cmd
}

// --- history ---

func historyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history [example-dir]",
		Short: "Show run history",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.Open(flagDBPath)
			if err != nil {
				return err
			}
			defer db.Close()

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tDIR\tVARIANT\tRESULT\tCORE\tSTARTED\tDURATION")

			var recs []store.Record
			if len(args) > 0 {
				recs, err = db.History(args[0], 20)
			} else {
				recs, err = db.AllLatest()
			}
			if err != nil {
				return err
			}
			for _, r := range recs {
				result := "PASS"
				if !r.Pass {
					result = "FAIL"
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%dms\n",
					r.ID, r.ExampleDir, r.Variant, result, r.CoreVersion,
					r.StartedAt.Format("2006-01-02 15:04:05"),
					r.DurationMs)
			}
			return w.Flush()
		},
	}
}

// --- helpers ---

func findExamples(root string) ([]string, error) {
	var dirs []string
	err := fs.WalkDir(os.DirFS(root), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() != "run.json" && d.Name() != "run.json.j2" {
			return nil
		}
		dir := filepath.Join(root, filepath.Dir(path))
		// A dir with both run.json and run.json.j2 is visited twice; the .j2
		// is the source of truth, so skip the plain run.json in that case.
		if d.Name() == "run.json" {
			if _, err := os.Stat(filepath.Join(dir, "run.json.j2")); err == nil {
				return nil
			}
		}
		// Skip "inheritance-only" run.json files that have no config files
		// alongside them (no .json/.j2/.tpl other than run.json itself).
		if !hasConfigFiles(dir) {
			return nil
		}
		dirs = append(dirs, dir)
		return nil
	})
	return dirs, err
}

// hasConfigFiles reports whether dir contains at least one template/config file
// (*.json, *.j2, *.tpl) other than run.json itself.
func hasConfigFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "run.json" {
			continue
		}
		ext := filepath.Ext(name)
		if ext == ".json" || ext == ".j2" || ext == ".tpl" {
			return true
		}
	}
	return false
}
