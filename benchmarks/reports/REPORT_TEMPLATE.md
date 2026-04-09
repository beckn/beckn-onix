# beckn-onix Adapter — Benchmark Report

> **Run:** `__TIMESTAMP__`
> **Platform:** __CPU__ · __GOOS__/__GOARCH__ · GOMAXPROCS=__GOMAXPROCS__ (default)
> **Adapter version:** __ONIX_VERSION__
> **Beckn Protocol:** v2.0.0

---

## Part A — Executive Summary

### What Was Tested

The beckn-onix ONIX adapter was benchmarked end-to-end using Go's native `testing.B`
framework and `net/http/httptest`. Requests flowed through a real compiled adapter —
with all production plugins active — against in-process mock servers, isolating
adapter-internal latency from network variables.

**Pipeline tested (bapTxnCaller):** `addRoute → sign → validateSchema`

**Plugins active:** `router`, `signer`, `simplekeymanager`, `cache` (miniredis), `schemav2validator`

**Actions benchmarked:** `discover`, `select`, `init`, `confirm`

### Key Results

| Metric | Value |
|--------|-------|
| Serial p50 latency (discover) | **__P50_US__ µs** |
| Serial p95 latency (discover) | **__P95_US__ µs** |
| Serial p99 latency (discover) | **__P99_US__ µs** |
| Serial mean latency (discover) | **__MEAN_DISCOVER_US__ µs** |
| Peak parallel throughput | **__PEAK_RPS__ req/s** |
| Cache warm vs cold delta | **__CACHE_DELTA__** |
| Memory per request (discover) | **~__MEM_DISCOVER_KB__ KB · __ALLOCS_DISCOVER__ allocs** |

### Interpretation

__INTERPRETATION__

### Recommendation

__RECOMMENDATION__

---

## Part B — Technical Detail

### B0 — Test Environment

| Parameter | Value |
|-----------|-------|
| CPU | __CPU__ (__GOARCH__) |
| OS | __GOOS__/__GOARCH__ |
| Go package | `github.com/beckn-one/beckn-onix/benchmarks/e2e` |
| Default GOMAXPROCS | __GOMAXPROCS__ |
| Benchmark timeout | 30 minutes |
| Serial run duration | 10s per benchmark × 3 runs |
| Parallel sweep duration | 30s per GOMAXPROCS level |
| GOMAXPROCS sweep | 1, 2, 4, 8, 16 |
| Redis | miniredis (in-process, no network) |
| BPP | httptest mock (instant ACK) |
| Registry | httptest mock (dev key pair) |
| Schema spec | Beckn v2.0.0 OpenAPI (`beckn.yaml`, local file) |

**Plugins and steps (bapTxnCaller):**

| Step | Plugin | Role |
|------|--------|------|
| 1 | `router` | Resolves BPP URL from routing config |
| 2 | `signer` + `simplekeymanager` | Signs request body (Ed25519/BLAKE-512) |
| 3 | `schemav2validator` | Validates Beckn v2.0 API schema |

---

### B1 — Latency by Action

Averages from `run1.txt` (10s, GOMAXPROCS=__GOMAXPROCS__). Percentile values from `percentiles.txt`.

| Action | Mean (µs) | p50 (µs) | p95 (µs) | p99 (µs) | Allocs/req | Bytes/req |
|--------|----------:|--------:|--------:|--------:|----------:|----------:|
| discover (serial) | __MEAN_DISCOVER_US__ | __P50_US__ | __P95_US__ | __P99_US__ | __ALLOCS_DISCOVER__ | __BYTES_DISCOVER__ (~__MEM_DISCOVER_KB__ KB) |
| select | __MEAN_SELECT_US__ | — | — | — | __ALLOCS_SELECT__ | __BYTES_SELECT__ (~__MEM_SELECT_KB__ KB) |
| init | __MEAN_INIT_US__ | — | — | — | __ALLOCS_INIT__ | __BYTES_INIT__ (~__MEM_INIT_KB__ KB) |
| confirm | __MEAN_CONFIRM_US__ | — | — | — | __ALLOCS_CONFIRM__ | __BYTES_CONFIRM__ (~__MEM_CONFIRM_KB__ KB) |

---

### B2 — Throughput vs Concurrency

Results from the concurrency sweep (`parallel_cpu*.txt`, 30s per level).

__THROUGHPUT_TABLE__

---

### B3 — Cache Impact (Redis warm vs cold)

Results from `cache_comparison.txt` (10s each, GOMAXPROCS=__GOMAXPROCS__).

| Scenario | Mean (µs) | Allocs/req | Bytes/req |
|----------|----------:|-----------:|----------:|
| CacheWarm | __CACHE_WARM_US__ | __CACHE_WARM_ALLOCS__ | __CACHE_WARM_BYTES__ |
| CacheCold | __CACHE_COLD_US__ | __CACHE_COLD_ALLOCS__ | __CACHE_COLD_BYTES__ |
| **Delta** | **__CACHE_DELTA__** | — | — |

---

### B4 — benchstat Statistical Summary (3 Runs)

```
__BENCHSTAT_SUMMARY__
```

---

### B5 — Bottleneck Analysis

> Populate after reviewing the numbers above and profiling with `go tool pprof`.

| Rank | Plugin / Step | Estimated contribution | Evidence |
|:----:|---------------|------------------------|---------|
| 1 | | | |
| 2 | | | |
| 3 | | | |

**Profiling commands:**

```bash
# CPU profile
go test ./benchmarks/e2e/... -bench=BenchmarkBAPCaller_Discover \
  -benchtime=30s -cpuprofile=benchmarks/results/cpu.prof -timeout=5m
go tool pprof -http=:6060 benchmarks/results/cpu.prof

# Memory profile
go test ./benchmarks/e2e/... -bench=BenchmarkBAPCaller_Discover \
  -benchtime=30s -memprofile=benchmarks/results/mem.prof -timeout=5m
go tool pprof -http=:6060 benchmarks/results/mem.prof

# Lock contention (find serialisation under parallel load)
go test ./benchmarks/e2e/... -bench=BenchmarkBAPCaller_Discover_Parallel \
  -benchtime=30s -mutexprofile=benchmarks/results/mutex.prof -timeout=5m
go tool pprof -http=:6060 benchmarks/results/mutex.prof
```

---

*Generated from run `__TIMESTAMP__` · beckn-onix __ONIX_VERSION__ · Beckn Protocol v2.0.0*
