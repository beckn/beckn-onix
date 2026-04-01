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
//	throughput_report.csv — RPS at each GOMAXPROCS level from the parallel sweep
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
	// Matches standard go bench output:
	// BenchmarkFoo-8   1000   1234567 ns/op   1234 B/op   56 allocs/op
	benchLineRe = regexp.MustCompile(
		`^(Benchmark\S+)\s+\d+\s+([\d.]+)\s+ns/op` +
			`(?:\s+([\d.]+)\s+B/op)?` +
			`(?:\s+([\d.]+)\s+allocs/op)?` +
			`(?:\s+([\d.]+)\s+p50_µs)?` +
			`(?:\s+([\d.]+)\s+p95_µs)?` +
			`(?:\s+([\d.]+)\s+p99_µs)?` +
			`(?:\s+([\d.]+)\s+req/s)?`,
	)

	// Matches custom metric lines in percentile output.
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
	currentBench := ""

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Main benchmark line.
		if m := benchLineRe.FindStringSubmatch(line); m != nil {
			r := benchResult{name: stripCPUSuffix(m[1])}
			r.nsPerOp = parseFloat(m[2])
			r.bytesOp = parseFloat(m[3])
			r.allocsOp = parseFloat(m[4])
			r.p50 = parseFloat(m[5])
			r.p95 = parseFloat(m[6])
			r.p99 = parseFloat(m[7])
			r.rps = parseFloat(m[8])
			results = append(results, r)
			currentBench = r.name
			continue
		}

		// Custom metric lines (e.g., "123.4 p50_µs").
		if currentBench != "" {
			for _, mm := range metricRe.FindAllStringSubmatch(line, -1) {
				val := parseFloat(mm[1])
				metric := mm[2]
				for i := range results {
					if results[i].name == currentBench {
						switch metric {
						case "p50_µs":
							results[i].p50 = val
						case "p95_µs":
							results[i].p95 = val
						case "p99_µs":
							results[i].p99 = val
						case "req/s":
							results[i].rps = val
						}
					}
				}
			}
		}
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

	header := []string{"gomaxprocs", "benchmark", "rps", "mean_latency_ms", "p95_latency_ms"}
	if err := w.Write(header); err != nil {
		return err
	}

	for _, row := range rows {
		r := []string{
			strconv.Itoa(row.cpu),
			row.res.name,
			fmtFloat(row.res.rps),
			fmtFloat(row.res.nsPerOp / 1e6),
			fmtFloat(row.res.p95),
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
