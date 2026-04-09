#!/usr/bin/env bash
# =============================================================================
# run_benchmarks.sh — beckn-onix adapter benchmark runner
#
# Usage:
#   cd beckn-onix
#   bash benchmarks/run_benchmarks.sh
#
# Requirements:
#   - Go 1.24+ installed
#   - benchstat is declared as a tool in go.mod; invoked via "go tool benchstat"
#
# Output:
#   benchmarks/results/<YYYY-MM-DD_HH-MM-SS>/
#     run1.txt, run2.txt, run3.txt   — raw go test -bench output
#     parallel_cpu1.txt ... cpu16.txt — concurrency sweep
#     benchstat_summary.txt           — statistical aggregation
# =============================================================================
set -euo pipefail

SCRIPT_START=$(date +%s)
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RESULTS_DIR="$REPO_ROOT/benchmarks/results/$(date +%Y-%m-%d_%H-%M-%S)"
BENCH_PKG="./benchmarks/e2e/..."
BENCH_TIMEOUT="10m"
# ── Smoke-test values (small): swap to the "full run" values below once stable ─
BENCH_TIME_SERIAL="10s"    # full run: 10s
BENCH_TIME_PARALLEL="30s"  # full run: 30s
BENCH_COUNT=1             # full run: keep at 1; benchstat uses the 3 serial files

cd "$REPO_ROOT"

# ── benchstat is declared as a go tool in go.mod; no separate install needed ──
# Use: go tool benchstat  (works anywhere without PATH changes)

# bench_filter: tee full output to the .log file for debugging, and write a
# clean copy (only benchstat-parseable lines) to the .txt file.
# The adapter logger is silenced via zerolog.SetGlobalLevel(zerolog.Disabled)
# in TestMain, so stdout should already be clean; the grep is a safety net for
# any stray lines from go test itself (build output, redis warnings, etc.).
bench_filter() {
  local txt="$1" log="$2"
  tee "$log" | grep -E "^(Benchmark|goos:|goarch:|pkg:|cpu:|ok |PASS|FAIL|--- )" > "$txt" || true
}

# ── Create results directory ──────────────────────────────────────────────────
mkdir -p "$RESULTS_DIR"
echo "=== beckn-onix Benchmark Runner ==="
echo "Results dir : $RESULTS_DIR"
echo "Package     : $BENCH_PKG"
echo ""

# ── Serial runs (3x for benchstat stability) ──────────────────────────────────
echo "Running serial benchmarks (3 runs × ${BENCH_TIME_SERIAL})..."
for run in 1 2 3; do
  echo "  Run $run/3..."
  go test \
    -timeout="$BENCH_TIMEOUT" \
    -run=^$ \
    -bench="." \
    -benchtime="$BENCH_TIME_SERIAL" \
    -benchmem \
    -count="$BENCH_COUNT" \
    "$BENCH_PKG" 2>&1 | bench_filter "$RESULTS_DIR/run${run}.txt" "$RESULTS_DIR/run${run}.log"
  echo "    Saved → $RESULTS_DIR/run${run}.txt (full log → run${run}.log)"
done
echo ""

# ── Concurrency sweep ─────────────────────────────────────────────────────────
echo "Running parallel concurrency sweep (cpu=1,2,4,8,16; ${BENCH_TIME_PARALLEL} each)..."
for cpu in 1 2 4 8 16; do
  echo "  GOMAXPROCS=$cpu..."
  go test \
    -timeout="$BENCH_TIMEOUT" \
    -run=^$ \
    -bench="BenchmarkBAPCaller_Discover_Parallel|BenchmarkBAPCaller_RPS" \
    -benchtime="$BENCH_TIME_PARALLEL" \
    -benchmem \
    -cpu="$cpu" \
    -count=1 \
    "$BENCH_PKG" 2>&1 | bench_filter "$RESULTS_DIR/parallel_cpu${cpu}.txt" "$RESULTS_DIR/parallel_cpu${cpu}.log"
  echo "    Saved → $RESULTS_DIR/parallel_cpu${cpu}.txt (full log → parallel_cpu${cpu}.log)"
done
echo ""

# ── Percentile benchmark ──────────────────────────────────────────────────────
echo "Running percentile benchmark (${BENCH_TIME_SERIAL})..."
go test \
  -timeout="$BENCH_TIMEOUT" \
  -run=^$ \
  -bench="BenchmarkBAPCaller_Discover_Percentiles" \
  -benchtime="$BENCH_TIME_SERIAL" \
  -benchmem \
  -count=1 \
  "$BENCH_PKG" 2>&1 | bench_filter "$RESULTS_DIR/percentiles.txt" "$RESULTS_DIR/percentiles.log"
echo "  Saved → $RESULTS_DIR/percentiles.txt (full log → percentiles.log)"
echo ""

# ── Cache comparison ──────────────────────────────────────────────────────────
echo "Running cache warm vs cold comparison..."
go test \
  -timeout="$BENCH_TIMEOUT" \
  -run=^$ \
  -bench="BenchmarkBAPCaller_Cache" \
  -benchtime="$BENCH_TIME_SERIAL" \
  -benchmem \
  -count=1 \
  "$BENCH_PKG" 2>&1 | bench_filter "$RESULTS_DIR/cache_comparison.txt" "$RESULTS_DIR/cache_comparison.log"
echo "  Saved → $RESULTS_DIR/cache_comparison.txt (full log → cache_comparison.log)"
echo ""

# ── benchstat statistical summary ─────────────────────────────────────────────
echo "Running benchstat statistical analysis..."
go tool benchstat \
  "$RESULTS_DIR/run1.txt" \
  "$RESULTS_DIR/run2.txt" \
  "$RESULTS_DIR/run3.txt" \
  > "$RESULTS_DIR/benchstat_summary.txt" 2>&1
echo "  Saved → $RESULTS_DIR/benchstat_summary.txt"
echo ""

# ── Parse results to CSV ──────────────────────────────────────────────────────
if command -v go &>/dev/null; then
  echo "Parsing results to CSV..."
  go run benchmarks/tools/parse_results.go \
    -dir="$RESULTS_DIR" \
    -out="$RESULTS_DIR" 2>&1 || echo "  (parse_results.go: optional step, skipping if errors)"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
SCRIPT_END=$(date +%s)
ELAPSED_SECS=$(( SCRIPT_END - SCRIPT_START ))
ELAPSED_MIN=$(( ELAPSED_SECS / 60 ))
ELAPSED_SEC_REM=$(( ELAPSED_SECS % 60 ))

echo ""
echo "========================================"
echo "✅ Benchmark run complete!"
echo ""
echo "Total runtime : ${ELAPSED_MIN}m ${ELAPSED_SEC_REM}s"
echo ""
echo "Results written to:"
echo "  $RESULTS_DIR"
echo ""
echo "Key files:"
echo "  benchstat_summary.txt — statistical analysis of 3 serial runs"
echo "  parallel_cpu*.txt     — concurrency sweep results"
echo "  percentiles.txt       — p50/p95/p99 latency data"
echo "  cache_comparison.txt  — warm vs cold Redis cache comparison"
echo ""
echo "To view benchstat summary:"
echo "  cat $RESULTS_DIR/benchstat_summary.txt"
echo "========================================"
