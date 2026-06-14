package main

import (
	"fmt"
	"html"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hiddify/hiddify_config_health/internal/health"
	"github.com/hiddify/hiddify_config_health/internal/store"
)

// reportCmd writes a standalone HTML summary of the latest result per
// (example, variant) from the database, so results can be viewed/shared
// without running the web server (e.g. as a CI artifact).
func reportCmd() *cobra.Command {
	var out string
	c := &cobra.Command{
		Use:   "report",
		Short: "Write a standalone HTML report of the latest results from the database",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.Open(flagDBPath)
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()
			recs, err := db.AllLatestVariants()
			if err != nil {
				return err
			}
			htmlOut := renderReport(recs)
			if out == "" || out == "-" {
				fmt.Print(htmlOut)
				return nil
			}
			if err := os.WriteFile(out, []byte(htmlOut), 0o644); err != nil {
				return err
			}
			fmt.Printf("wrote %s (%d rows)\n", out, len(recs))
			return nil
		},
	}
	c.Flags().StringVar(&out, "html", "", "output HTML file (default: stdout)")
	return c
}

func renderReport(recs []store.Record) string {
	// Drop legacy empty-variant rows for a dir that also has named variants
	// (those are pre-variant historical records that duplicate the dir).
	hasNamed := map[string]bool{}
	for _, r := range recs {
		if r.Variant != "" && r.Variant != r.Name {
			hasNamed[r.ExampleDir] = true
		}
	}
	filtered := recs[:0]
	for _, r := range recs {
		if (r.Variant == "" || r.Variant == r.Name) && hasNamed[r.ExampleDir] {
			continue
		}
		filtered = append(filtered, r)
	}
	recs = filtered

	sort.Slice(recs, func(i, j int) bool {
		if recs[i].ExampleDir != recs[j].ExampleDir {
			return recs[i].ExampleDir < recs[j].ExampleDir
		}
		return recs[i].Variant < recs[j].Variant
	})

	var rows strings.Builder
	pass, fail := 0, 0
	for _, r := range recs {
		if r.Pass {
			pass++
		} else {
			fail++
		}
		m := map[string]health.Result{}
		for _, c := range r.Checks {
			m[c.Name] = c
		}
		chk := func(name string) string {
			c, ok := m[name]
			if !ok {
				return `<td class="muted">—</td>`
			}
			if c.OK {
				return `<td class="ok">✓</td>`
			}
			if c.Optional {
				return `<td class="warn">!</td>`
			}
			return `<td class="fail">✗</td>`
		}
		dl := speedCell(m, "download", "speedtest")
		ul := speedCell(m, "upload")
		ld := speedCell(m, "load")
		lat := durCell(m["ping"].PingAvg)
		jit := durCell(m["ping"].Jitter)
		ent := "—"
		if e := m["entropy"]; e.EntropyScore > 0 {
			ent = fmt.Sprintf("%.3f", e.EntropyScore)
		}
		probe := valOr(m["active-probe"].ProbeVerdict)
		tls := valOr(m["tls-fingerprint"].TLSMatch)
		status := `<span class="badge pass">PASS</span>`
		if !r.Pass {
			status = `<span class="badge fail">FAIL</span>`
		}
		variant := r.Variant
		if variant == r.Name {
			variant = ""
		}
		rows.WriteString("<tr>" +
			td(r.Name) + tdc(variant, "variant") +
			chk("dns") + chk("http") + chk("quic") + chk("download") + chk("upload") + chk("ping") +
			td(lat) + td(jit) + td(dl) + td(ul) + td(ld) + td(ent) +
			probeCell(probe) + tlsCell(tls) +
			td(html.EscapeString(r.Fingerprint.Verdict)) +
			td(fmt.Sprintf("%dms", r.DurationMs)) +
			"<td>" + status + "</td>" +
			"</tr>\n")
	}

	return strings.NewReplacer(
		"{{ROWS}}", rows.String(),
		"{{PASS}}", fmt.Sprint(pass),
		"{{FAIL}}", fmt.Sprint(fail),
		"{{TIME}}", time.Now().Format("2006-01-02 15:04:05"),
	).Replace(reportTemplate)
}

func speedCell(m map[string]health.Result, names ...string) string {
	for _, n := range names {
		if c, ok := m[n]; ok && c.Throughput > 0 {
			return health.FormatThroughput(c.Throughput)
		}
	}
	return "—"
}

func durCell(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return health.FormatDuration(d)
}

func valOr(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func td(s string) string  { return "<td>" + s + "</td>" }
func tdc(s, c string) string {
	if s == "" {
		return `<td class="muted">—</td>`
	}
	return `<td class="` + c + `">` + html.EscapeString(s) + "</td>"
}
func probeCell(v string) string {
	if v == "—" {
		return `<td class="muted">—</td>`
	}
	cls := "warn"
	if v == "resistant" {
		cls = "ok"
	}
	return `<td class="` + cls + `">` + html.EscapeString(v) + "</td>"
}
func tlsCell(v string) string {
	if v == "—" {
		return `<td class="muted">—</td>`
	}
	cls := "ok"
	if v == "none" {
		cls = "warn"
	}
	return `<td class="` + cls + `">` + html.EscapeString(v) + "</td>"
}

const reportTemplate = `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8">
<title>Hiddify Health Report</title>
<style>
 body{font-family:system-ui,sans-serif;background:#0f1117;color:#e2e8f0;margin:0;padding:24px}
 h1{font-size:1.1rem;color:#a78bfa}
 .meta{color:#718096;font-size:.85rem;margin-bottom:16px}
 table{border-collapse:collapse;width:100%;font-size:.82rem}
 th,td{padding:7px 10px;text-align:left;border-bottom:1px solid #1a202c;white-space:nowrap}
 th{color:#718096;font-size:.72rem;text-transform:uppercase}
 .ok{color:#6ee7b7}.warn{color:#fbbf24}.fail{color:#fca5a5}.muted{color:#4a5568}
 .variant{color:#a78bfa}
 .badge{padding:2px 8px;border-radius:9px;font-size:.7rem;font-weight:700}
 .badge.pass{background:#065f46;color:#6ee7b7}.badge.fail{background:#7f1d1d;color:#fca5a5}
</style></head><body>
<h1>🛡 Hiddify Health — Report</h1>
<div class="meta">{{PASS}} pass · {{FAIL}} fail · generated {{TIME}}</div>
<table><thead><tr>
 <th>Example</th><th>Variant</th><th>DNS</th><th>HTTP</th><th>QUIC</th>
 <th>DL</th><th>UL</th><th>Ping</th><th>Latency</th><th>Jitter</th>
 <th>↓DL</th><th>↑UL</th><th>Load</th><th>Entropy</th><th>Probe</th><th>TLS-FP</th>
 <th>Censor</th><th>Time</th><th>Status</th>
</tr></thead><tbody>
{{ROWS}}
</tbody></table>
</body></html>`
