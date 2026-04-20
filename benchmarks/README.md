# beckn-onix Adapter Benchmarks

End-to-end performance benchmarks for the beckn-onix ONIX adapter, using Go's native `testing.B` framework and `net/http/httptest`. No Docker, no external services — everything runs in-process.

---

## Quick Start

```bash
# From the repo root — install benchstat first (one-time, see below)
go install golang.org/x/perf/cmd/benchstat@latest
bash benchmarks/run_benchmarks.sh  # compile plugins, run all scenarios, generate report
```

Runtime output lands in `benchmarks/results/<timestamp>/` (gitignored). Committed reports live in `benchmarks/reports/`.

---

## What Is Being Benchmarked

The benchmarks target the **`bapTxnCaller`** handler — the primary outbound path a BAP takes when initiating a Beckn transaction. Every request travels through the full production pipeline:

```
Benchmark goroutine(s)
        │  HTTP POST /bap/caller/<action>
        ▼
httptest.Server  ←  ONIX adapter (real compiled .so plugins)
        │
        ├── addRoute      router plugin      resolve BPP URL from routing config
        ├── sign          signer + simplekeymanager  Ed25519 / BLAKE-512 signing
        └── validateSchema  schemav2validator  Beckn OpenAPI spec validation
        │
        └──▶ httptest mock BPP  (instant ACK — no network)
```

Mock services replace all external dependencies so results reflect **adapter-internal latency only**:

| Dependency | Replaced by |
|------------|-------------|
| Redis | `miniredis` (in-process) |
| BPP backend | `httptest` mock — returns `{"message":{"ack":{"status":"ACK"}}}` |
| Beckn registry | `httptest` mock — returns the dev key pair for signature verification |

---

## Benchmark Scenarios

| Benchmark | What it measures |
|-----------|-----------------|
| `BenchmarkBAPCaller_Discover` | Baseline single-goroutine latency for `/discover` |
| `BenchmarkBAPCaller_Discover_Parallel` | Throughput under concurrent load; run with `-cpu=1,2,4,8,16` |
| `BenchmarkBAPCaller_AllActions` | Per-action latency: `discover`, `select`, `init`, `confirm` |
| `BenchmarkBAPCaller_Discover_Percentiles` | p50 / p95 / p99 latency via `b.ReportMetric` |
| `BenchmarkBAPCaller_CacheWarm` | Latency when the Redis key cache is already populated |
| `BenchmarkBAPCaller_CacheCold` | Latency on a cold cache — full key-derivation round-trip |
| `BenchmarkBAPCaller_RPS` | Requests-per-second under parallel load (`req/s` custom metric) |

---

## How It Works

### Startup (`TestMain`)

Before any benchmark runs, `TestMain` in `e2e/setup_test.go`:

1. **Compiles all required plugins** to a temporary directory using `go build -buildmode=plugin`. The first run takes 60–90 s (cold Go build cache); subsequent runs are near-instant.
2. **Starts miniredis** — an in-process Redis server used by the `cache` plugin (no external Redis needed).
3. **Starts mock servers** — an instant-ACK BPP and a registry mock that returns the dev signing public key.
4. **Starts the adapter** — wires all plugins programmatically (no YAML parsing) and wraps it in an `httptest.Server`.

### Per-iteration (`buildSignedRequest`)

Each benchmark iteration:
1. Loads the JSON fixture for the requested Beckn action (`testdata/<action>_request.json`).
2. Substitutes sentinel values (`BENCH_TIMESTAMP`, `BENCH_MESSAGE_ID`, `BENCH_TRANSACTION_ID`) with fresh values, ensuring unique message IDs per iteration.
3. Signs the body using the Beckn Ed25519/BLAKE-512 spec (same algorithm as the production `signer` plugin).
4. Sends the signed `POST` to the adapter and validates a `200 OK` response.

### Validation test (`TestSignBecknPayload`)

A plain `Test*` function runs before the benchmarks and sends one signed request end-to-end. If the signing helper is mis-implemented, this fails fast before any benchmark time is wasted.

---

## Directory Layout

```
benchmarks/
├── README.md                        ← you are here
├── run_benchmarks.sh                ← one-shot runner script
├── e2e/
│   ├── bench_test.go                ← benchmark functions
│   ├── setup_test.go                ← TestMain, startAdapter, signing helper
│   ├── mocks_test.go                ← mock BPP and registry servers
│   ├── keys_test.go                 ← dev key pair constants
│   └── testdata/
│       ├── routing-BAPCaller.yaml   ← routing config (BENCH_BPP_URL placeholder)
│       ├── discover_request.json    ← Beckn search payload fixture
│       ├── select_request.json
│       ├── init_request.json
│       └── confirm_request.json
├── tools/
│   ├── parse_results.go             ← CSV exporter for latency + throughput data
│   └── generate_report.go           ← fills REPORT_TEMPLATE.md with run data
├── reports/                         ← committed benchmark reports and template
│   ├── REPORT_TEMPLATE.md           ← template used to generate each run's report
│   └── REPORT_ONIX_v150.md          ← baseline report (Apple M5, Beckn v2.0.0)
└── results/                         ← gitignored; created by run_benchmarks.sh
    └── <timestamp>/
        ├── BENCHMARK_REPORT.md            — generated human-readable report
        ├── run1.txt, run2.txt, run3.txt   — raw go test -bench output
        ├── parallel_cpu*.txt              — concurrency sweep
        ├── benchstat_summary.txt          — statistical aggregation
        ├── latency_report.csv             — per-benchmark latency (from parse_results.go)
        └── throughput_report.csv          — RPS vs GOMAXPROCS (from parse_results.go)
```

---

## Reports

Committed reports are stored in `benchmarks/reports/`. Each report documents the environment, raw numbers, and analysis for a specific run and adapter version.

| File | Platform | Adapter version |
|------|----------|-----------------|
| `REPORT_ONIX_v150.md` | Apple M5 · darwin/arm64 · GOMAXPROCS=10 | beckn-onix v1.5.0 |

The script auto-generates `BENCHMARK_REPORT.md` in each results directory using `REPORT_TEMPLATE.md`. To permanently record a run:
1. Run `bash benchmarks/run_benchmarks.sh` — `BENCHMARK_REPORT.md` is generated automatically.
2. Review it, fill in the B5 bottleneck analysis section.
3. Copy it to `benchmarks/reports/REPORT_<tag>.md` and commit.
4. `benchmarks/results/` stays gitignored; only the curated report goes in.

---

## Running Individual Benchmarks

```bash
# Single benchmark, 10 s
go test ./benchmarks/e2e/... \
  -bench=BenchmarkBAPCaller_Discover \
  -benchtime=10s -benchmem -timeout=30m

# All actions in one shot
go test ./benchmarks/e2e/... \
  -bench=BenchmarkBAPCaller_AllActions \
  -benchtime=5s -benchmem -timeout=30m

# Concurrency sweep at 1, 4, and 16 goroutines
go test ./benchmarks/e2e/... \
  -bench=BenchmarkBAPCaller_Discover_Parallel \
  -benchtime=30s -cpu=1,4,16 -timeout=30m

# Race detector check (no data races)
go test ./benchmarks/e2e/... \
  -bench=BenchmarkBAPCaller_Discover_Parallel \
  -benchtime=5s -race -timeout=30m

# Percentile metrics (p50/p95/p99 in µs)
go test ./benchmarks/e2e/... \
  -bench=BenchmarkBAPCaller_Discover_Percentiles \
  -benchtime=10s -benchmem -timeout=30m
```

## Comparing Two Runs with benchstat

`benchstat` is a dev tool and is **not** tracked in `go.mod` (see [issue #659](https://github.com/beckn/beckn-onix/issues/659) for context — adding it as a module tool forced a Go 1.25 dependency on the main module). Install it once in your local Go bin:

```bash
go install golang.org/x/perf/cmd/benchstat@latest
```

Then compare two runs:

```bash
go test ./benchmarks/e2e/... -bench=. -benchtime=10s -count=6 > before.txt
# ... make your change ...
go test ./benchmarks/e2e/... -bench=. -benchtime=10s -count=6 > after.txt
benchstat before.txt after.txt
```

---

## Dependencies

| Package | Purpose | Where declared |
|---------|---------|----------------|
| `github.com/alicebob/miniredis/v2` | In-process Redis for the `cache` plugin | `go.mod` |
| `golang.org/x/perf/cmd/benchstat` | Statistical benchmark comparison (CLI tool) | **not in `go.mod`** — install manually (see above) |

`benchstat` is intentionally kept out of `go.mod`. Its transitive dependencies (`x/net`, `x/crypto`) require Go ≥ 1.25, which would force the entire module onto a newer toolchain. Install it once with `go install golang.org/x/perf/cmd/benchstat@latest`.
