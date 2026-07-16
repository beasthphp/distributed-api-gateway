package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/beasthphp/distributed-api-gateway/internal/benchmark"
)

func main() {
	input := flag.String("input", "results/raw", "directory containing raw benchmark JSON")
	output := flag.String("output", "results/analysis", "report output directory")
	flag.Parse()
	if flag.NArg() != 0 {
		fatal(fmt.Errorf("unexpected positional arguments"))
	}
	results, err := readResults(*input)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*output, 0o755); err != nil {
		fatal(fmt.Errorf("create report directory: %w", err))
	}
	if err := writeCSV(filepath.Join(*output, "summary.csv"), results); err != nil {
		fatal(err)
	}
	if err := writeMarkdown(filepath.Join(*output, "report.md"), results); err != nil {
		fatal(err)
	}
	if err := writeSVG(filepath.Join(*output, "comparison.svg"), results); err != nil {
		fatal(err)
	}
	fmt.Printf("generated report from %d raw result files in %s\n", len(results), *output)
}

func readResults(directory string) ([]benchmark.Result, error) {
	paths, err := filepath.Glob(filepath.Join(directory, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("list raw benchmark results: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no JSON benchmark results found in %s", directory)
	}
	results := make([]benchmark.Result, 0, len(paths))
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		result, decodeErr := benchmark.Decode(file)
		closeErr := file.Close()
		if err := errors.Join(decodeErr, closeErr); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Scenario == results[j].Scenario {
			return results[i].Tool < results[j].Tool
		}
		return results[i].Scenario < results[j].Scenario
	})
	return results, nil
}

func writeCSV(path string, results []benchmark.Result) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create summary CSV: %w", err)
	}
	writer := csv.NewWriter(file)
	rows := [][]string{{
		"scenario", "tool", "gateway_instances", "requests", "concurrency", "duration_ms",
		"throughput_rps", "p50_ms", "p95_ms", "p99_ms", "error_rate_percent",
		"rate_limited", "observed_upstreams", "verification_passed",
	}}
	for _, result := range results {
		verified := ""
		if result.Verification != nil {
			verified = strconv.FormatBool(result.Verification.Passed)
		}
		rows = append(rows, []string{
			result.Scenario, result.Tool, strconv.Itoa(result.GatewayInstances), strconv.Itoa(result.Requests),
			strconv.Itoa(result.Concurrency), decimal(result.DurationMS), decimal(result.ThroughputRPS),
			decimal(result.LatencyMS.P50), decimal(result.LatencyMS.P95), decimal(result.LatencyMS.P99),
			decimal(result.ErrorRatePercent), strconv.Itoa(result.RateLimited), strconv.Itoa(len(result.UpstreamCounts)), verified,
		})
	}
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			_ = file.Close()
			return fmt.Errorf("write summary CSV: %w", err)
		}
	}
	writer.Flush()
	return errors.Join(writer.Error(), file.Close())
}

func writeMarkdown(path string, results []benchmark.Result) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create benchmark report: %w", err)
	}
	defer file.Close()
	fmt.Fprintln(file, "# Measured benchmark report")
	fmt.Fprintln(file)
	fmt.Fprintln(file, "> Every number below is derived from committed raw request samples. These results describe one recorded environment; they are not universal capacity claims.")
	fmt.Fprintln(file)
	fmt.Fprintln(file, "## Measured facts")
	fmt.Fprintln(file)
	fmt.Fprintln(file, "| Scenario | Client | Instances | Requests / concurrency | Throughput | p50 | p95 | p99 | Errors | Upstreams |")
	fmt.Fprintln(file, "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|")
	for _, result := range results {
		fmt.Fprintf(file, "| %s | %s | %d | %d / %d | %.2f req/s | %.2f ms | %.2f ms | %.2f ms | %.2f%% | %d |\n",
			result.Scenario, result.Tool, result.GatewayInstances, result.Requests, result.Concurrency,
			result.ThroughputRPS, result.LatencyMS.P50, result.LatencyMS.P95, result.LatencyMS.P99,
			result.ErrorRatePercent, len(result.UpstreamCounts))
	}

	fmt.Fprintln(file)
	fmt.Fprintln(file, "## Rate-limit correctness")
	fmt.Fprintln(file)
	quotaFound := false
	for _, result := range results {
		if result.Verification == nil || !strings.Contains(result.Scenario, "rate-limit") {
			continue
		}
		quotaFound = true
		fmt.Fprintf(file, "- `%s`: **passed=%t**, accepted=%d, denied=%d, theoretical maximum accepted=%d; required headers and allowed statuses are recorded in the raw result.\n",
			result.Scenario, result.Verification.Passed, result.Verification.Accepted,
			result.Verification.Denied, result.Verification.TheoreticalMaximumAccepted)
	}
	if !quotaFound {
		fmt.Fprintln(file, "- No rate-limit verification result was present.")
	}

	fmt.Fprintln(file)
	fmt.Fprintln(file, "## Interpretation (not measured fact)")
	fmt.Fprintln(file)
	single, singleOK := findResult(results, "single-go", "go-loadgen")
	multi, multiOK := findResult(results, "multi-go", "go-loadgen")
	if singleOK && multiOK {
		throughputChange := percentChange(single.ThroughputRPS, multi.ThroughputRPS)
		p95Change := percentChange(single.LatencyMS.P95, multi.LatencyMS.P95)
		fmt.Fprintf(file, "- In this run, moving from %d to %d gateway instances changed Go-client throughput by %+.2f%% and p95 latency by %+.2f%%. Shared runner CPU, PostgreSQL, Redis, Nginx, and the client can all influence that observation; the data alone does not identify a cause.\n",
			single.GatewayInstances, multi.GatewayInstances, throughputChange, p95Change)
	}
	fmt.Fprintln(file, "- Go and C++ results are independent client implementations and a reproducibility cross-check. Differences between them must not be described as gateway improvements because their connection handling and runtime overhead differ.")
	fmt.Fprintln(file, "- Percentiles use the nearest-rank method over end-to-end client latency. Warmup requests are excluded.")

	fmt.Fprintln(file)
	fmt.Fprintln(file, "## Evidence")
	fmt.Fprintln(file)
	fmt.Fprintln(file, "- `../raw/*.json`: per-request latency, status, transport outcome, quota-header validity, selected upstream, timing, and tool environment")
	fmt.Fprintln(file, "- `summary.csv`: machine-readable aggregate comparison")
	fmt.Fprintln(file, "- `comparison.svg`: visualization of the measured aggregate values")
	fmt.Fprintln(file, "- `../../docs/benchmarking.md`: exact reproduction commands, workload, and limitations")
	return nil
}

func writeSVG(path string, results []benchmark.Result) error {
	measured := make([]benchmark.Result, 0, len(results))
	for _, result := range results {
		if !strings.Contains(result.Scenario, "rate-limit") {
			measured = append(measured, result)
		}
	}
	if len(measured) == 0 {
		return fmt.Errorf("no throughput results available for chart")
	}
	const width, height = 1200, 650
	const top, bottom = 105.0, 535.0
	maxThroughput, maxLatency := 0.0, 0.0
	for _, result := range measured {
		maxThroughput = max(maxThroughput, result.ThroughputRPS)
		maxLatency = max(maxLatency, result.LatencyMS.P99)
	}
	if maxThroughput == 0 {
		maxThroughput = 1
	}
	if maxLatency == 0 {
		maxLatency = 1
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create benchmark chart: %w", err)
	}
	defer file.Close()
	fmt.Fprintf(file, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, width, height, width, height)
	fmt.Fprint(file, `<rect width="100%" height="100%" fill="#0b1220"/><style>text{font-family:Inter,Arial,sans-serif;fill:#e5e7eb}.muted{fill:#94a3b8}.axis{stroke:#64748b;stroke-width:1}.throughput{fill:#38bdf8}.p95{fill:#a78bfa}.p99{fill:#f472b6}</style>`)
	fmt.Fprint(file, `<text x="48" y="42" font-size="25" font-weight="700">Measured gateway benchmark</text>`)
	fmt.Fprint(file, `<text x="48" y="72" font-size="14" class="muted">Recorded values, not causal attribution • exact samples are committed as JSON</text>`)
	drawPanel := func(x, panelWidth float64, title, unit string) {
		fmt.Fprintf(file, `<text x="%.0f" y="98" font-size="17" font-weight="600">%s</text>`, x, html.EscapeString(title))
		fmt.Fprintf(file, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" class="axis"/>`, x, bottom, x+panelWidth, bottom)
		fmt.Fprintf(file, `<text x="%.0f" y="%.0f" font-size="12" class="muted">%s</text>`, x, bottom+92, html.EscapeString(unit))
	}
	drawPanel(55, 500, "Throughput", "requests / second")
	drawPanel(655, 490, "Tail latency", "milliseconds")

	barGroup := 500.0 / float64(len(measured))
	for index, result := range measured {
		x := 65 + float64(index)*barGroup
		barWidth := barGroup * 0.58
		barHeight := (result.ThroughputRPS / maxThroughput) * (bottom - top)
		fmt.Fprintf(file, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="4" class="throughput"/>`, x, bottom-barHeight, barWidth, barHeight)
		fmt.Fprintf(file, `<text x="%.1f" y="%.1f" font-size="12" text-anchor="middle">%.1f</text>`, x+barWidth/2, bottom-barHeight-8, result.ThroughputRPS)
		fmt.Fprintf(file, `<text x="%.1f" y="%.1f" font-size="11" text-anchor="middle" transform="rotate(25 %.1f %.1f)">%s</text>`,
			x+barWidth/2, bottom+24, x+barWidth/2, bottom+24, html.EscapeString(result.Scenario))
	}

	latencyGroup := 490.0 / float64(len(measured))
	for index, result := range measured {
		x := 665 + float64(index)*latencyGroup
		barWidth := latencyGroup * 0.28
		p95Height := (result.LatencyMS.P95 / maxLatency) * (bottom - top)
		p99Height := (result.LatencyMS.P99 / maxLatency) * (bottom - top)
		fmt.Fprintf(file, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="3" class="p95"/>`, x, bottom-p95Height, barWidth, p95Height)
		fmt.Fprintf(file, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="3" class="p99"/>`, x+barWidth+3, bottom-p99Height, barWidth, p99Height)
		fmt.Fprintf(file, `<text x="%.1f" y="%.1f" font-size="10" text-anchor="middle">%.1f</text>`, x+barWidth/2, bottom-p95Height-6, result.LatencyMS.P95)
		fmt.Fprintf(file, `<text x="%.1f" y="%.1f" font-size="10" text-anchor="middle">%.1f</text>`, x+barWidth*1.5+3, bottom-p99Height-6, result.LatencyMS.P99)
		fmt.Fprintf(file, `<text x="%.1f" y="%.1f" font-size="11" text-anchor="middle" transform="rotate(25 %.1f %.1f)">%s</text>`,
			x+barWidth+1, bottom+24, x+barWidth+1, bottom+24, html.EscapeString(result.Scenario))
	}
	fmt.Fprint(file, `<rect x="895" y="86" width="12" height="12" class="p95"/><text x="913" y="97" font-size="12">p95</text><rect x="950" y="86" width="12" height="12" class="p99"/><text x="968" y="97" font-size="12">p99</text></svg>`)
	return nil
}

func findResult(results []benchmark.Result, scenario, tool string) (benchmark.Result, bool) {
	for _, result := range results {
		if result.Scenario == scenario && result.Tool == tool {
			return result, true
		}
	}
	return benchmark.Result{}, false
}

func percentChange(before, after float64) float64 {
	if before == 0 {
		return 0
	}
	return 100 * (after - before) / before
}

func decimal(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
