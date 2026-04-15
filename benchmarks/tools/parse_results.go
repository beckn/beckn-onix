// parse_results.go — Parses raw go test -bench output from the benchmark results
// directory and produces two CSV files for analysis and reporting.
//
// Usage:
//
//	go run benchmarks/tools/parse_results.go \
//	  -dir=benchmarks/results/<timestamp>/ \
//	  -out=benchmarks/results/<timestamp>/
//
// Output files:
//
//	latency_report.csv    — per-benchmark mean, p50, p95, p99 latency, allocs
//	throughput_report.csv — RPS and mean latency at each GOMAXPROCS level from the parallel sweep
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	// Matches the benchmark name and ns/op from a standard go test -bench output line.
	// Go outputs custom metrics (p50_µs, req/s, …) BEFORE B/op and allocs/op, so we
	// extract those fields with dedicated regexps rather than relying on positional groups.
	//
	// Example lines:
	//   BenchmarkBAPCaller_Discover-10        73542  164193 ns/op  82913 B/op  662 allocs/op
	//   BenchmarkBAPCaller_Discover_Percentiles-10  72849  164518 ns/op  130.0 p50_µs  144.0 p95_µs  317.0 p99_µs  82528 B/op  660 allocs/op
	//   BenchmarkBAPCaller_RPS-4              700465  73466 ns/op  14356.0 req/s  80375 B/op  660 allocs/op
	benchLineRe = regexp.MustCompile(`^(Benchmark\S+)\s+\d+\s+([\d.]+)\s+ns/op`)
	bytesRe     = regexp.MustCompile(`([\d.]+)\s+B/op`)
	allocsRe    = regexp.MustCompile(`([\d.]+)\s+allocs/op`)

	// Extracts any custom metric value from a benchmark line.
	metricRe = regexp.MustCompile(`([\d.]+)\s+(p50_µs|p95_µs|p99_µs|req/s)`)
)

type benchResult struct {
	name     string
	nsPerOp  float64
	bytesOp  float64
	allocsOp float64
	p50      float64
	p95      float64
	p99      float64
	rps      float64
}

// cpuResult pairs a GOMAXPROCS value with a benchmark result from the parallel sweep.
type cpuResult struct {
	cpu int
	res benchResult
}

func main() {
	dir := flag.String("dir", ".", "Directory containing benchmark result files")
	out := flag.String("out", ".", "Output directory for CSV files")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR creating output dir: %v\n", err)
		os.Exit(1)
	}

	// ── Parse serial runs (run1.txt, run2.txt, run3.txt) ─────────────────────
	var latencyResults []benchResult
	for _, runFile := range []string{"run1.txt", "run2.txt", "run3.txt"} {
		path := filepath.Join(*dir, runFile)
		results, err := parseRunFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: could not parse %s: %v\n", runFile, err)
			continue
		}
		latencyResults = append(latencyResults, results...)
	}

	// Also parse percentiles file for p50/p95/p99.
	percPath := filepath.Join(*dir, "percentiles.txt")
	if percResults, err := parseRunFile(percPath); err == nil {
		latencyResults = append(latencyResults, percResults...)
	}

	if err := writeLatencyCSV(filepath.Join(*out, "latency_report.csv"), latencyResults); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR writing latency CSV: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Written: %s\n", filepath.Join(*out, "latency_report.csv"))

	// ── Parse parallel sweep (parallel_cpu*.txt) ──────────────────────────────
	var throughputRows []cpuResult

	for _, cpu := range []int{1, 2, 4, 8, 16} {
		path := filepath.Join(*dir, fmt.Sprintf("parallel_cpu%d.txt", cpu))
		results, err := parseRunFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: could not parse parallel_cpu%d.txt: %v\n", cpu, err)
			continue
		}
		for _, r := range results {
			throughputRows = append(throughputRows, cpuResult{cpu: cpu, res: r})
		}
	}

	if err := writeThroughputCSV(filepath.Join(*out, "throughput_report.csv"), throughputRows); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR writing throughput CSV: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Written: %s\n", filepath.Join(*out, "throughput_report.csv"))
}

// parseRunFile reads a go test -bench output file and returns all benchmark results.
func parseRunFile(path string) ([]benchResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var results []benchResult

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		m := benchLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		r := benchResult{name: stripCPUSuffix(m[1])}
		r.nsPerOp = parseFloat(m[2])

		// B/op and allocs/op — extracted independently because Go places custom
		// metrics (p50_µs, req/s, …) between ns/op and B/op on the same line.
		if bm := bytesRe.FindStringSubmatch(line); bm != nil {
			r.bytesOp = parseFloat(bm[1])
		}
		if am := allocsRe.FindStringSubmatch(line); am != nil {
			r.allocsOp = parseFloat(am[1])
		}

		// Custom metrics — scan the whole line regardless of position.
		for _, mm := range metricRe.FindAllStringSubmatch(line, -1) {
			switch mm[2] {
			case "p50_µs":
				r.p50 = parseFloat(mm[1])
			case "p95_µs":
				r.p95 = parseFloat(mm[1])
			case "p99_µs":
				r.p99 = parseFloat(mm[1])
			case "req/s":
				r.rps = parseFloat(mm[1])
			}
		}

		results = append(results, r)
	}
	return results, scanner.Err()
}

func writeLatencyCSV(path string, results []benchResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{"benchmark", "mean_ms", "p50_µs", "p95_µs", "p99_µs", "allocs_op", "bytes_op"}
	if err := w.Write(header); err != nil {
		return err
	}

	for _, r := range results {
		row := []string{
			r.name,
			fmtFloat(r.nsPerOp / 1e6), // ns/op → ms
			fmtFloat(r.p50),
			fmtFloat(r.p95),
			fmtFloat(r.p99),
			fmtFloat(r.allocsOp),
			fmtFloat(r.bytesOp),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

func writeThroughputCSV(path string, rows []cpuResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	// p95 latency is not available from the parallel sweep files — those benchmarks
	// only emit ns/op and req/s. p95 data comes exclusively from
	// BenchmarkBAPCaller_Discover_Percentiles, which runs at a single GOMAXPROCS
	// setting and is not part of the concurrency sweep.
	header := []string{"gomaxprocs", "benchmark", "rps", "mean_latency_ms"}
	if err := w.Write(header); err != nil {
		return err
	}

	for _, row := range rows {
		r := []string{
			strconv.Itoa(row.cpu),
			row.res.name,
			fmtFloat(row.res.rps),
			fmtFloat(row.res.nsPerOp / 1e6),
		}
		if err := w.Write(r); err != nil {
			return err
		}
	}
	return nil
}

// stripCPUSuffix removes trailing "-N" goroutine count suffixes from benchmark names.
func stripCPUSuffix(name string) string {
	if idx := strings.LastIndex(name, "-"); idx > 0 {
		if _, err := strconv.Atoi(name[idx+1:]); err == nil {
			return name[:idx]
		}
	}
	return name
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func fmtFloat(v float64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', 3, 64)
}
