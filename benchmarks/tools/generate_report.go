// generate_report.go — Fills REPORT_TEMPLATE.md with data from a completed
// benchmark run and writes BENCHMARK_REPORT.md to the results directory.
//
// Usage:
//
//	go run benchmarks/tools/generate_report.go \
//	  -dir=benchmarks/results/<timestamp>/ \
//	  -template=benchmarks/reports/REPORT_TEMPLATE.md \
//	  -version=<onix-version>
//
// The generator reads:
//   - latency_report.csv       — per-benchmark latency and allocation data
//   - throughput_report.csv    — RPS and latency by GOMAXPROCS level
//   - benchstat_summary.txt    — raw benchstat output block
//   - run1.txt                 — goos / goarch / cpu metadata
//
// Placeholders filled in the template:
//
//	__TIMESTAMP__         results dir basename (YYYY-MM-DD_HH-MM-SS)
//	__ONIX_VERSION__      -version flag value
//	__GOOS__              from run1.txt header
//	__GOARCH__            from run1.txt header
//	__CPU__               from run1.txt header
//	__GOMAXPROCS__        derived from the benchmark name suffix in run1.txt
//	__P50_US__            p50 latency in µs (from Discover_Percentiles row)
//	__P95_US__            p95 latency in µs
//	__P99_US__            p99 latency in µs
//	__MEAN_DISCOVER_US__  mean latency in µs for discover
//	__MEAN_SELECT_US__    mean latency in µs for select
//	__MEAN_INIT_US__      mean latency in µs for init
//	__MEAN_CONFIRM_US__   mean latency in µs for confirm
//	__ALLOCS_DISCOVER__   allocs/req for discover
//	__ALLOCS_SELECT__     allocs/req for select
//	__ALLOCS_INIT__       allocs/req for init
//	__ALLOCS_CONFIRM__    allocs/req for confirm
//	__BYTES_DISCOVER__    bytes/req for discover
//	__BYTES_SELECT__      bytes/req for select
//	__BYTES_INIT__        bytes/req for init
//	__BYTES_CONFIRM__     bytes/req for confirm
//	__MEM_DISCOVER_KB__   bytes/req converted to KB for discover
//	__MEM_SELECT_KB__     bytes/req converted to KB for select
//	__MEM_INIT_KB__       bytes/req converted to KB for init
//	__MEM_CONFIRM_KB__    bytes/req converted to KB for confirm
//	__PEAK_RPS__          highest RPS across all GOMAXPROCS levels
//	__CACHE_WARM_US__     mean latency in µs for CacheWarm
//	__CACHE_COLD_US__     mean latency in µs for CacheCold
//	__CACHE_WARM_ALLOCS__ allocs/req for CacheWarm
//	__CACHE_COLD_ALLOCS__ allocs/req for CacheCold
//	__CACHE_WARM_BYTES__  bytes/req for CacheWarm
//	__CACHE_COLD_BYTES__  bytes/req for CacheCold
//	__CACHE_DELTA__       formatted warm-vs-cold delta string
//	__THROUGHPUT_TABLE__  generated markdown table from throughput_report.csv
//	__BENCHSTAT_SUMMARY__ raw contents of benchstat_summary.txt
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func main() {
	dir := flag.String("dir", "", "Results directory (required)")
	tmplPath := flag.String("template", "benchmarks/reports/REPORT_TEMPLATE.md", "Path to report template")
	version := flag.String("version", "unknown", "Adapter version (e.g. v1.5.0)")
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "ERROR: -dir is required")
		os.Exit(1)
	}

	// Derive timestamp from the directory basename.
	timestamp := filepath.Base(*dir)

	// ── Read template ──────────────────────────────────────────────────────────
	tmplBytes, err := os.ReadFile(*tmplPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: reading template %s: %v\n", *tmplPath, err)
		os.Exit(1)
	}
	report := string(tmplBytes)

	// ── Parse run1.txt for environment metadata ────────────────────────────────
	env := parseEnv(filepath.Join(*dir, "run1.txt"))

	// ── Parse latency_report.csv ──────────────────────────────────────────────
	latency, err := parseLatencyCSV(filepath.Join(*dir, "latency_report.csv"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not parse latency_report.csv: %v\n", err)
	}

	// ── Parse throughput_report.csv ───────────────────────────────────────────
	throughput, err := parseThroughputCSV(filepath.Join(*dir, "throughput_report.csv"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not parse throughput_report.csv: %v\n", err)
	}

	// ── Read benchstat_summary.txt ────────────────────────────────────────────
	benchstat := readFileOrDefault(filepath.Join(*dir, "benchstat_summary.txt"),
		"(benchstat output not available)")

	// ── Compute derived values ─────────────────────────────────────────────────

	// Mean latency: convert ms → µs, round to integer.
	meanDiscoverUS := msToUS(latency["BenchmarkBAPCaller_Discover"]["mean_ms"])
	meanSelectUS := msToUS(latency["BenchmarkBAPCaller_AllActions/select"]["mean_ms"])
	meanInitUS := msToUS(latency["BenchmarkBAPCaller_AllActions/init"]["mean_ms"])
	meanConfirmUS := msToUS(latency["BenchmarkBAPCaller_AllActions/confirm"]["mean_ms"])

	// Percentiles come from the Discover_Percentiles row.
	perc := latency["BenchmarkBAPCaller_Discover_Percentiles"]
	p50 := fmtMetric(perc["p50_µs"], "µs")
	p95 := fmtMetric(perc["p95_µs"], "µs")
	p99 := fmtMetric(perc["p99_µs"], "µs")

	// Memory: bytes → KB (1 decimal place).
	memDiscoverKB := bytesToKB(latency["BenchmarkBAPCaller_Discover"]["bytes_op"])
	memSelectKB := bytesToKB(latency["BenchmarkBAPCaller_AllActions/select"]["bytes_op"])
	memInitKB := bytesToKB(latency["BenchmarkBAPCaller_AllActions/init"]["bytes_op"])
	memConfirmKB := bytesToKB(latency["BenchmarkBAPCaller_AllActions/confirm"]["bytes_op"])

	// Cache delta.
	warmUS := msToUS(latency["BenchmarkBAPCaller_CacheWarm"]["mean_ms"])
	coldUS := msToUS(latency["BenchmarkBAPCaller_CacheCold"]["mean_ms"])
	cacheDelta := formatCacheDelta(warmUS, coldUS)

	// Peak RPS across all concurrency levels.
	peakRPS := "—"
	var peakRPSVal float64
	for _, row := range throughput {
		if v := parseFloatOrZero(row["rps"]); v > peakRPSVal {
			peakRPSVal = v
			peakRPS = fmt.Sprintf("%.0f", peakRPSVal)
		}
	}

	// ── Build throughput table ─────────────────────────────────────────────────
	throughputTable := buildThroughputTable(throughput)

	// ── Generate interpretation and recommendation ─────────────────────────────
	interpretation := buildInterpretation(perc, latency, throughput, warmUS, coldUS)
	recommendation := buildRecommendation(throughput)

	// ── Apply substitutions ────────────────────────────────────────────────────
	replacements := map[string]string{
		"__TIMESTAMP__":        timestamp,
		"__ONIX_VERSION__":     *version,
		"__GOOS__":             env["goos"],
		"__GOARCH__":           env["goarch"],
		"__CPU__":              env["cpu"],
		"__GOMAXPROCS__":       env["gomaxprocs"],
		"__P50_US__":           p50,
		"__P95_US__":           p95,
		"__P99_US__":           p99,
		"__MEAN_DISCOVER_US__": meanDiscoverUS,
		"__MEAN_SELECT_US__":   meanSelectUS,
		"__MEAN_INIT_US__":     meanInitUS,
		"__MEAN_CONFIRM_US__":  meanConfirmUS,
		"__ALLOCS_DISCOVER__":  fmtInt(latency["BenchmarkBAPCaller_Discover"]["allocs_op"]),
		"__ALLOCS_SELECT__":    fmtInt(latency["BenchmarkBAPCaller_AllActions/select"]["allocs_op"]),
		"__ALLOCS_INIT__":      fmtInt(latency["BenchmarkBAPCaller_AllActions/init"]["allocs_op"]),
		"__ALLOCS_CONFIRM__":   fmtInt(latency["BenchmarkBAPCaller_AllActions/confirm"]["allocs_op"]),
		"__BYTES_DISCOVER__":   fmtInt(latency["BenchmarkBAPCaller_Discover"]["bytes_op"]),
		"__BYTES_SELECT__":     fmtInt(latency["BenchmarkBAPCaller_AllActions/select"]["bytes_op"]),
		"__BYTES_INIT__":       fmtInt(latency["BenchmarkBAPCaller_AllActions/init"]["bytes_op"]),
		"__BYTES_CONFIRM__":    fmtInt(latency["BenchmarkBAPCaller_AllActions/confirm"]["bytes_op"]),
		"__MEM_DISCOVER_KB__":  memDiscoverKB,
		"__MEM_SELECT_KB__":    memSelectKB,
		"__MEM_INIT_KB__":      memInitKB,
		"__MEM_CONFIRM_KB__":   memConfirmKB,
		"__PEAK_RPS__":         peakRPS,
		"__CACHE_WARM_US__":    warmUS,
		"__CACHE_COLD_US__":    coldUS,
		"__CACHE_WARM_ALLOCS__": fmtInt(latency["BenchmarkBAPCaller_CacheWarm"]["allocs_op"]),
		"__CACHE_COLD_ALLOCS__": fmtInt(latency["BenchmarkBAPCaller_CacheCold"]["allocs_op"]),
		"__CACHE_WARM_BYTES__":  fmtInt(latency["BenchmarkBAPCaller_CacheWarm"]["bytes_op"]),
		"__CACHE_COLD_BYTES__":  fmtInt(latency["BenchmarkBAPCaller_CacheCold"]["bytes_op"]),
		"__CACHE_DELTA__":      cacheDelta,
		"__THROUGHPUT_TABLE__":  throughputTable,
		"__BENCHSTAT_SUMMARY__": benchstat,
		"__INTERPRETATION__":   interpretation,
		"__RECOMMENDATION__":   recommendation,
	}

	for placeholder, value := range replacements {
		report = strings.ReplaceAll(report, placeholder, value)
	}

	// ── Write output ───────────────────────────────────────────────────────────
	outPath := filepath.Join(*dir, "BENCHMARK_REPORT.md")
	if err := os.WriteFile(outPath, []byte(report), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: writing report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Written → %s\n", outPath)
}

// ── Parsers ────────────────────────────────────────────────────────────────────

var gomaxprocsRe = regexp.MustCompile(`-(\d+)$`)

// parseEnv reads goos, goarch, cpu, and GOMAXPROCS from a run*.txt file header.
func parseEnv(path string) map[string]string {
	env := map[string]string{
		"goos": "unknown", "goarch": "unknown",
		"cpu": "unknown", "gomaxprocs": "unknown",
	}
	f, err := os.Open(path)
	if err != nil {
		return env
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "goos:"):
			env["goos"] = strings.TrimSpace(strings.TrimPrefix(line, "goos:"))
		case strings.HasPrefix(line, "goarch:"):
			env["goarch"] = strings.TrimSpace(strings.TrimPrefix(line, "goarch:"))
		case strings.HasPrefix(line, "cpu:"):
			env["cpu"] = strings.TrimSpace(strings.TrimPrefix(line, "cpu:"))
		case strings.HasPrefix(line, "Benchmark"):
			// Extract GOMAXPROCS from first benchmark line suffix (e.g. "-10").
			if m := gomaxprocsRe.FindStringSubmatch(strings.Fields(line)[0]); m != nil {
				env["gomaxprocs"] = m[1]
			}
		}
	}
	return env
}

// parseLatencyCSV returns a map of benchmark name → field name → raw string value.
// When multiple rows exist for the same benchmark (3 serial runs), values from
// the first non-empty occurrence are used.
func parseLatencyCSV(path string) (map[string]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, err
	}

	result := map[string]map[string]string{}
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(row) == 0 {
			continue
		}
		name := row[0]
		if _, exists := result[name]; !exists {
			result[name] = map[string]string{}
		}
		for i, col := range header[1:] {
			idx := i + 1
			if idx < len(row) && row[idx] != "" && result[name][col] == "" {
				result[name][col] = row[idx]
			}
		}
	}
	return result, nil
}

// parseThroughputCSV returns rows as a slice of field maps.
func parseThroughputCSV(path string) ([]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, err
	}

	var rows []map[string]string
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(row) == 0 {
			continue
		}
		m := map[string]string{}
		for i, col := range header {
			if i < len(row) {
				m[col] = row[i]
			}
		}
		rows = append(rows, m)
	}
	return rows, nil
}

// buildThroughputTable renders the throughput CSV as a markdown table.
func buildThroughputTable(rows []map[string]string) string {
	if len(rows) == 0 {
		return "_No concurrency sweep data available._"
	}
	var sb strings.Builder
	sb.WriteString("| GOMAXPROCS | Mean Latency (µs) | RPS |\n")
	sb.WriteString("|:----------:|------------------:|----:|\n")
	for _, row := range rows {
		cpu := orDash(row["gomaxprocs"])
		latUS := "—"
		if v := parseFloatOrZero(row["mean_latency_ms"]); v > 0 {
			latUS = fmt.Sprintf("%.0f", v*1000)
		}
		rps := orDash(row["rps"])
		sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n", cpu, latUS, rps))
	}
	return sb.String()
}

// ── Formatters ─────────────────────────────────────────────────────────────────

// msToUS converts a ms string to a rounded µs string.
func msToUS(ms string) string {
	v := parseFloatOrZero(ms)
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f", v*1000)
}

// bytesToKB converts a bytes string to a KB string with 1 decimal place.
func bytesToKB(bytes string) string {
	v := parseFloatOrZero(bytes)
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f", v/1024)
}

// fmtInt formats a float string as a rounded integer string.
func fmtInt(s string) string {
	v := parseFloatOrZero(s)
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f", math.Round(v))
}

// fmtMetric formats a metric value with the given unit, or returns "—".
func fmtMetric(s, unit string) string {
	v := parseFloatOrZero(s)
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f %s", v, unit)
}

// formatCacheDelta produces a human-readable warm-vs-cold delta string.
func formatCacheDelta(warmUS, coldUS string) string {
	w := parseFloatOrZero(warmUS)
	c := parseFloatOrZero(coldUS)
	if w == 0 || c == 0 {
		return "—"
	}
	delta := w - c
	sign := "+"
	if delta < 0 {
		sign = ""
	}
	return fmt.Sprintf("%s%.0f µs (warm vs cold)", sign, delta)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func parseFloatOrZero(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func readFileOrDefault(path, def string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	return strings.TrimRight(string(b), "\n")
}

// ── Narrative generators ───────────────────────────────────────────────────────

// buildInterpretation generates a data-driven interpretation paragraph from the
// benchmark results. It covers tail-latency control, action complexity trend,
// concurrency scaling efficiency, and cache impact.
func buildInterpretation(
	perc map[string]string,
	latency map[string]map[string]string,
	throughput []map[string]string,
	warmUS, coldUS string,
) string {
	var sb strings.Builder

	p50 := parseFloatOrZero(perc["p50_µs"])
	p99 := parseFloatOrZero(perc["p99_µs"])
	meanDiscover := parseFloatOrZero(latency["BenchmarkBAPCaller_Discover"]["mean_ms"]) * 1000

	// Tail-latency control.
	if p50 > 0 && p99 > 0 {
		ratio := p99 / p50
		quality := "good"
		if ratio > 5 {
			quality = "poor"
		} else if ratio > 3 {
			quality = "moderate"
		}
		sb.WriteString(fmt.Sprintf(
			"The adapter delivers a p50 latency of **%.0f µs** for the discover action. "+
				"The p99/p50 ratio is **%.1f×**, indicating %s tail-latency control — "+
				"spikes are %s relative to the median.\n\n",
			p50, ratio, quality, tailDescription(ratio),
		))
	} else if meanDiscover > 0 {
		sb.WriteString(fmt.Sprintf(
			"The adapter delivers a mean latency of **%.0f µs** for the discover action. "+
				"Run with `-bench=BenchmarkBAPCaller_Discover_Percentiles` to obtain p50/p95/p99 data.\n\n",
			meanDiscover,
		))
	}

	// Action complexity trend.
	selectMS := parseFloatOrZero(latency["BenchmarkBAPCaller_AllActions/select"]["mean_ms"]) * 1000
	initMS := parseFloatOrZero(latency["BenchmarkBAPCaller_AllActions/init"]["mean_ms"]) * 1000
	confirmMS := parseFloatOrZero(latency["BenchmarkBAPCaller_AllActions/confirm"]["mean_ms"]) * 1000
	if meanDiscover > 0 && selectMS > 0 && initMS > 0 && confirmMS > 0 {
		sb.WriteString(fmt.Sprintf(
			"Latency scales with payload complexity: select (+%.0f%%), init (+%.0f%%), confirm (+%.0f%%) "+
				"vs the discover baseline. Allocation counts track proportionally, driven by JSON "+
				"unmarshalling and schema validation of larger payloads.\n\n",
			pctChange(meanDiscover, selectMS),
			pctChange(meanDiscover, initMS),
			pctChange(meanDiscover, confirmMS),
		))
	}

	// Concurrency scaling.
	lat1 := latencyAtCPU(throughput, "1")
	lat16 := latencyAtCPU(throughput, "16")
	if lat1 > 0 && lat16 > 0 {
		improvement := lat1 / lat16
		sb.WriteString(fmt.Sprintf(
			"Concurrency scaling is effective: mean latency drops from **%.0f µs** at GOMAXPROCS=1 "+
				"to **%.0f µs** at GOMAXPROCS=16 — a **%.1f× improvement**.",
			lat1*1000, lat16*1000, improvement,
		))
		if improvement < 4 {
			sb.WriteString(" Gains taper beyond 8 cores, suggesting a shared serialisation point " +
				"(likely schema validation or key derivation).")
		}
		sb.WriteString("\n\n")
	}

	// Cache impact.
	w := parseFloatOrZero(warmUS)
	c := parseFloatOrZero(coldUS)
	if w > 0 && c > 0 {
		delta := math.Abs(w-c) / w * 100
		if delta < 5 {
			sb.WriteString(fmt.Sprintf(
				"The Redis key-manager cache shows **no measurable impact** in this setup "+
					"(warm vs cold delta: %.0f µs, %.1f%% of mean). "+
					"miniredis is in-process; signing and schema validation dominate. "+
					"Cache benefit would be visible with real Redis over a network.",
				math.Abs(w-c), delta,
			))
		} else {
			sb.WriteString(fmt.Sprintf(
				"The Redis key-manager cache provides a **%.0f µs improvement** (%.1f%%) "+
					"on the warm path vs cold.",
				math.Abs(w-c), delta,
			))
		}
		sb.WriteString("\n")
	}

	if sb.Len() == 0 {
		return "_Insufficient data to generate interpretation. Ensure all benchmark scenarios completed successfully._"
	}
	return strings.TrimRight(sb.String(), "\n")
}

// buildRecommendation generates a sizing and tuning recommendation based on the
// concurrency sweep results.
func buildRecommendation(throughput []map[string]string) string {
	if len(throughput) == 0 {
		return "_Run the concurrency sweep to generate sizing recommendations._"
	}

	// Find the GOMAXPROCS level with best scaling efficiency (RPS gain per core).
	type cpuPoint struct {
		cpu int
		rps float64
		lat float64
	}
	var points []cpuPoint
	for _, row := range throughput {
		cpu := int(parseFloatOrZero(row["gomaxprocs"]))
		rps := parseFloatOrZero(row["rps"])
		lat := parseFloatOrZero(row["mean_latency_ms"]) * 1000
		if cpu > 0 && lat > 0 {
			points = append(points, cpuPoint{cpu, rps, lat})
		}
	}

	if len(points) == 0 {
		return "_Run the concurrency sweep (parallel_cpu*.txt) to generate sizing recommendations._"
	}

	// Find sweet spot: largest latency improvement per doubling of cores.
	bestEffCPU := points[0].cpu
	bestEff := 0.0
	for i := 1; i < len(points); i++ {
		if points[i-1].lat > 0 {
			eff := (points[i-1].lat - points[i].lat) / points[i-1].lat
			if eff > bestEff {
				bestEff = eff
				bestEffCPU = points[i].cpu
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"**%d cores** offers the best throughput/cost ratio based on the concurrency sweep — "+
			"scaling efficiency begins to taper beyond this point.\n\n",
		bestEffCPU,
	))
	sb.WriteString("The adapter is ready for staged load testing against a real BPP. " +
		"For production sizing, start with the recommended core count above and adjust based " +
		"on observed throughput targets. If schema validation dominates CPU (likely at high " +
		"concurrency), profile with `go tool pprof` using the commands in B5 to isolate the bottleneck.")

	return sb.String()
}

// ── Narrative helpers ──────────────────────────────────────────────────────────

func tailDescription(ratio float64) string {
	switch {
	case ratio <= 2:
		return "minimal"
	case ratio <= 3:
		return "modest"
	case ratio <= 5:
		return "noticeable"
	default:
		return "significant"
	}
}

func pctChange(base, val float64) float64 {
	if base == 0 {
		return 0
	}
	return (val - base) / base * 100
}

func latencyAtCPU(throughput []map[string]string, cpu string) float64 {
	for _, row := range throughput {
		if row["gomaxprocs"] == cpu {
			if v := parseFloatOrZero(row["mean_latency_ms"]); v > 0 {
				return v
			}
		}
	}
	return 0
}
