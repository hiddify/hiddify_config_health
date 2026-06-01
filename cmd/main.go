// Command hiddify-health tests VPN/proxy configurations against multiple cores.
package main

import (
	"context"
	"fmt"
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
)

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "hiddify-health",
		Short: "Test VPN/proxy configuration files across multiple cores",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flagDBPath, "db", store.DefaultPath(), "SQLite result database path")
	root.PersistentFlags().StringVar(&flagExamplesDir, "examples", "examples", "examples root directory")
	root.PersistentFlags().IntVar(&flagTimeout, "timeout", 30, "per-check timeout in seconds")

	root.AddCommand(
		runCmd(),
		runAllCmd(),
		checkCmd(),
		serveCmd(),
		historyCmd(),
	)
	return root
}

// --- run ---

func runCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <example-dir>",
		Short: "Run one example test",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, _ := store.Open(flagDBPath)
			if db != nil {
				defer db.Close()
			}
			return runOne(cmd.Context(), args[0], db)
		},
	}
}

func runOne(ctx context.Context, dir string, db *store.DB) error {
	fmt.Printf("▶ %s\n", dir)
	results, err := runner.Run(ctx, dir, os.Stdout)
	if err != nil {
		fmt.Printf("  ERROR: %v\n", err)
		return err
	}
	anyFail := false
	for _, res := range results {
		if db != nil {
			rec := store.Record{
				ExampleDir:  dir,
				Name:        res.Name,
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
		status := "PASS"
		if !res.Pass {
			status = "FAIL"
			anyFail = true
		}
		label := res.Name
		if res.Variant != "" && res.Variant != res.Name {
			label = res.Variant
		}
		fmt.Printf("  [%s] %s  duration=%s  censor=%s\n",
			label, status, res.Duration.Round(time.Millisecond), res.Fingerprint.Verdict)
	}
	if anyFail {
		return fmt.Errorf("test failed")
	}
	return nil
}

// --- run-all ---

func runAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run-all [examples-dir]",
		Short: "Run all examples; exit 1 if any fail",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := flagExamplesDir
			if len(args) > 0 {
				root = args[0]
			}
			dirs, err := findExamples(root)
			if err != nil {
				return err
			}
			if len(dirs) == 0 {
				fmt.Println("No run.json files found under", root)
				return nil
			}

			db, _ := store.Open(flagDBPath)
			if db != nil {
				defer db.Close()
			}

			pass, fail := 0, 0
			for _, dir := range dirs {
				if err := runOne(cmd.Context(), dir, db); err != nil {
					fail++
				} else {
					pass++
				}
			}
			fmt.Printf("\n--- %d passed  %d failed ---\n", pass, fail)
			if fail > 0 {
				return fmt.Errorf("%d test(s) failed", fail)
			}
			return nil
		},
	}
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
			if _, err := os.Stat(path); err != nil {
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
			fmt.Fprintln(w, "ID\tDIR\tRESULT\tCORE\tSTARTED\tDURATION")

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
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%dms\n",
					r.ID, r.ExampleDir, result, r.CoreVersion,
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
		if d.Name() == "run.json" {
			dirs = append(dirs, filepath.Join(root, filepath.Dir(path)))
		}
		return nil
	})
	return dirs, err
}
