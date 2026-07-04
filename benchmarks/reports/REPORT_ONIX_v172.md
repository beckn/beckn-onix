# beckn-onix Adapter — Benchmark Report

> **Run:** `2026-07-02_14-25-21`
> **Platform:** Apple M5 (10 cores) · darwin/arm64 · GOMAXPROCS=10 (default)
> **Adapter version:** v1.7.2
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
| Serial p50 latency (discover) | **131 µs µs** |
| Serial p95 latency (discover) | **147 µs µs** |
| Serial p99 latency (discover) | **347 µs µs** |
| Serial mean latency (discover) | **166 µs** |
| Peak parallel throughput | **19373 req/s** |
| Cache warm vs cold delta | **+2 µs (warm vs cold)** |
| Memory per request (discover) | **~83.8 KB · 722 allocs** |

### Interpretation

The adapter delivers a p50 latency of **131 µs** for the discover action. The p99/p50 ratio is **2.6×**, indicating good tail-latency control — spikes are modest relative to the median.

Latency scales with payload complexity: select (+20%), init (+40%), confirm (+41%) vs the discover baseline. Allocation counts track proportionally, driven by JSON unmarshalling and schema validation of larger payloads.

Concurrency scaling is effective: mean latency drops from **164 µs** at GOMAXPROCS=1 to **48 µs** at GOMAXPROCS=16 — a **3.4× improvement**. Gains taper beyond 8 cores, suggesting a shared serialisation point (likely schema validation or key derivation).

The Redis key-manager cache shows **no measurable impact** in this setup (warm vs cold delta: 2 µs, 1.2% of mean). miniredis is in-process; signing and schema validation dominate. Cache benefit would be visible with real Redis over a network.

### Recommendation

**2 cores** offers the best throughput/cost ratio based on the concurrency sweep — scaling efficiency begins to taper beyond this point.

The adapter is ready for staged load testing against a real BPP. For production sizing, start with the recommended core count above and adjust based on observed throughput targets. If schema validation dominates CPU (likely at high concurrency), profile with `go tool pprof` using the commands in B5 to isolate the bottleneck.

---

## Part B — Technical Detail

### B0 — Test Environment

| Parameter | Value |
|-----------|-------|
| CPU | Apple M5 (arm64) |
| Cores | 10 |
| OS | darwin/arm64 |
| Go package | `github.com/beckn-one/beckn-onix/benchmarks/e2e` |
| Default GOMAXPROCS | 10 |
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

Averages from `run1.txt` (10s, GOMAXPROCS=10). Percentile values from `percentiles.txt`.

| Action | Mean (µs) | p50 (µs) | p95 (µs) | p99 (µs) | Allocs/req | Bytes/req |
|--------|----------:|--------:|--------:|--------:|----------:|----------:|
| discover (serial) | 166 | 131 µs | 147 µs | 347 µs | 722 | 85786 (~83.8 KB) |
| select | 200 | — | — | — | 1027 | 106245 (~103.8 KB) |
| init | 232 | — | — | — | 1363 | 125973 (~123.0 KB) |
| confirm | 234 | — | — | — | 1418 | 127806 (~124.8 KB) |

---

### B2 — Throughput vs Concurrency

Results from the concurrency sweep (`parallel_cpu*.txt`, 30s per level).

| GOMAXPROCS | Mean Latency (µs) | RPS |
|:----------:|------------------:|----:|
| 1 | 164 | — |
| 1 | 164 | 6085.000 |
| 2 | 87 | — |
| 2 | 86 | 11649.000 |
| 4 | 59 | — |
| 4 | 61 | 16280.000 |
| 8 | 49 | — |
| 8 | 52 | 19373.000 |
| 16 | 48 | — |
| 16 | 55 | 18025.000 |


---

### B3 — Cache Impact (Redis warm vs cold)

Results from `cache_comparison.txt` (10s each, GOMAXPROCS=10).

| Scenario | Mean (µs) | Allocs/req | Bytes/req |
|----------|----------:|-----------:|----------:|
| CacheWarm | 166 | 712 | 84113 |
| CacheCold | 164 | 721 | 85463 |
| **Delta** | **+2 µs (warm vs cold)** | — | — |

---

### B4 — benchstat Statistical Summary (3 Runs)

```
goos: darwin
goarch: arm64
pkg: github.com/beckn-one/beckn-onix/benchmarks/e2e
cpu: Apple M5
                                  │ benchmarks/results/2026-07-02_14-25-21/run1.txt │ benchmarks/results/2026-07-02_14-25-21/run2.txt │ benchmarks/results/2026-07-02_14-25-21/run3.txt │
                                  │                     sec/op                      │         sec/op           vs base                │         sec/op           vs base                │
BAPCaller_Discover-10                                                  166.5µ ± ∞ ¹              165.0µ ± ∞ ¹       ~ (p=1.000 n=1) ²              164.8µ ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_Discover_Parallel-10                                         39.96µ ± ∞ ¹              41.77µ ± ∞ ¹       ~ (p=1.000 n=1) ²              40.99µ ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/discover-10                                       164.1µ ± ∞ ¹              166.0µ ± ∞ ¹       ~ (p=1.000 n=1) ²              164.4µ ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/select-10                                         200.2µ ± ∞ ¹              199.8µ ± ∞ ¹       ~ (p=1.000 n=1) ²              198.7µ ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/init-10                                           232.2µ ± ∞ ¹              229.0µ ± ∞ ¹       ~ (p=1.000 n=1) ²              230.1µ ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/confirm-10                                        234.3µ ± ∞ ¹              233.7µ ± ∞ ¹       ~ (p=1.000 n=1) ²              231.6µ ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_Discover_Percentiles-10                                      165.7µ ± ∞ ¹              164.8µ ± ∞ ¹       ~ (p=1.000 n=1) ²              164.3µ ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_CacheWarm-10                                                 166.2µ ± ∞ ¹              164.1µ ± ∞ ¹       ~ (p=1.000 n=1) ²              164.2µ ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_CacheCold-10                                                 164.0µ ± ∞ ¹              165.5µ ± ∞ ¹       ~ (p=1.000 n=1) ²              165.1µ ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_RPS-10                                                       43.73µ ± ∞ ¹              41.10µ ± ∞ ¹       ~ (p=1.000 n=1) ²              41.04µ ± ∞ ¹       ~ (p=1.000 n=1) ²
geomean                                                                137.1µ                    136.5µ        -0.43%                              135.9µ        -0.89%
¹ need >= 6 samples for confidence interval at level 0.95
² need >= 4 samples to detect a difference at alpha level 0.05

                                  │ benchmarks/results/2026-07-02_14-25-21/run1.txt │ benchmarks/results/2026-07-02_14-25-21/run2.txt │ benchmarks/results/2026-07-02_14-25-21/run3.txt │
                                  │                      B/op                       │          B/op            vs base                │          B/op            vs base                │
BAPCaller_Discover-10                                                 83.78Ki ± ∞ ¹             83.75Ki ± ∞ ¹       ~ (p=1.000 n=1) ²             83.72Ki ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_Discover_Parallel-10                                        81.64Ki ± ∞ ¹             81.65Ki ± ∞ ¹       ~ (p=1.000 n=1) ²             81.66Ki ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/discover-10                                      83.45Ki ± ∞ ¹             83.46Ki ± ∞ ¹       ~ (p=1.000 n=1) ²             83.51Ki ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/select-10                                        103.8Ki ± ∞ ¹             103.8Ki ± ∞ ¹       ~ (p=1.000 n=1) ²             103.8Ki ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/init-10                                          123.0Ki ± ∞ ¹             122.9Ki ± ∞ ¹       ~ (p=1.000 n=1) ²             123.0Ki ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/confirm-10                                       124.8Ki ± ∞ ¹             124.9Ki ± ∞ ¹       ~ (p=1.000 n=1) ²             124.9Ki ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_Discover_Percentiles-10                                     83.37Ki ± ∞ ¹             83.23Ki ± ∞ ¹       ~ (p=1.000 n=1) ²             83.25Ki ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_CacheWarm-10                                                82.14Ki ± ∞ ¹             82.20Ki ± ∞ ¹       ~ (p=1.000 n=1) ²             82.15Ki ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_CacheCold-10                                                83.46Ki ± ∞ ¹             83.64Ki ± ∞ ¹       ~ (p=1.000 n=1) ²             83.63Ki ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_RPS-10                                                      81.68Ki ± ∞ ¹             81.64Ki ± ∞ ¹       ~ (p=1.000 n=1) ²             81.64Ki ± ∞ ¹       ~ (p=1.000 n=1) ²
geomean                                                               91.79Ki                   91.79Ki        +0.01%                             91.80Ki        +0.01%
¹ need >= 6 samples for confidence interval at level 0.95
² need >= 4 samples to detect a difference at alpha level 0.05

                                  │ benchmarks/results/2026-07-02_14-25-21/run1.txt │ benchmarks/results/2026-07-02_14-25-21/run2.txt │ benchmarks/results/2026-07-02_14-25-21/run3.txt │
                                  │                    allocs/op                    │        allocs/op         vs base                │        allocs/op         vs base                │
BAPCaller_Discover-10                                                   722.0 ± ∞ ¹               722.0 ± ∞ ¹       ~ (p=1.000 n=1) ²               722.0 ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_Discover_Parallel-10                                          720.0 ± ∞ ¹               720.0 ± ∞ ¹       ~ (p=1.000 n=1) ²               720.0 ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/discover-10                                        720.0 ± ∞ ¹               720.0 ± ∞ ¹       ~ (p=1.000 n=1) ²               720.0 ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/select-10                                         1.027k ± ∞ ¹              1.027k ± ∞ ¹       ~ (p=1.000 n=1) ²              1.027k ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/init-10                                           1.363k ± ∞ ¹              1.363k ± ∞ ¹       ~ (p=1.000 n=1) ²              1.363k ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_AllActions/confirm-10                                        1.418k ± ∞ ¹              1.418k ± ∞ ¹       ~ (p=1.000 n=1) ²              1.418k ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_Discover_Percentiles-10                                       720.0 ± ∞ ¹               720.0 ± ∞ ¹       ~ (p=1.000 n=1) ²               720.0 ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_CacheWarm-10                                                  712.0 ± ∞ ¹               712.0 ± ∞ ¹       ~ (p=1.000 n=1) ²               712.0 ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_CacheCold-10                                                  721.0 ± ∞ ¹               721.0 ± ∞ ¹       ~ (p=1.000 n=1) ²               721.0 ± ∞ ¹       ~ (p=1.000 n=1) ²
BAPCaller_RPS-10                                                        720.0 ± ∞ ¹               720.0 ± ∞ ¹       ~ (p=1.000 n=1) ²               720.0 ± ∞ ¹       ~ (p=1.000 n=1) ²
geomean                                                                 850.4                     850.4        +0.00%                               850.4        +0.00%
¹ need >= 6 samples for confidence interval at level 0.95
² all samples are equal

                                  │ benchmarks/results/2026-07-02_14-25-21/run1.txt │ benchmarks/results/2026-07-02_14-25-21/run2.txt │ benchmarks/results/2026-07-02_14-25-21/run3.txt │
                                  │                     p50_µs                      │            p50_µs             vs base           │            p50_µs             vs base           │
BAPCaller_Discover_Percentiles-10                                       131.0 ± ∞ ¹                    131.0 ± ∞ ¹  ~ (p=1.000 n=1) ²                    131.0 ± ∞ ¹  ~ (p=1.000 n=1) ²
¹ need >= 6 samples for confidence interval at level 0.95
² all samples are equal

                                  │ benchmarks/results/2026-07-02_14-25-21/run1.txt │ benchmarks/results/2026-07-02_14-25-21/run2.txt │ benchmarks/results/2026-07-02_14-25-21/run3.txt │
                                  │                     p95_µs                      │            p95_µs             vs base           │            p95_µs             vs base           │
BAPCaller_Discover_Percentiles-10                                       147.0 ± ∞ ¹                    145.0 ± ∞ ¹  ~ (p=1.000 n=1) ²                    145.0 ± ∞ ¹  ~ (p=1.000 n=1) ²
¹ need >= 6 samples for confidence interval at level 0.95
² need >= 4 samples to detect a difference at alpha level 0.05

                                  │ benchmarks/results/2026-07-02_14-25-21/run1.txt │ benchmarks/results/2026-07-02_14-25-21/run2.txt │ benchmarks/results/2026-07-02_14-25-21/run3.txt │
                                  │                     p99_µs                      │            p99_µs             vs base           │            p99_µs             vs base           │
BAPCaller_Discover_Percentiles-10                                       347.0 ± ∞ ¹                    333.0 ± ∞ ¹  ~ (p=1.000 n=1) ²                    327.0 ± ∞ ¹  ~ (p=1.000 n=1) ²
¹ need >= 6 samples for confidence interval at level 0.95
² need >= 4 samples to detect a difference at alpha level 0.05

                 │ benchmarks/results/2026-07-02_14-25-21/run1.txt │ benchmarks/results/2026-07-02_14-25-21/run2.txt │ benchmarks/results/2026-07-02_14-25-21/run3.txt │
                 │                      req/s                      │            req/s              vs base           │            req/s              vs base           │
BAPCaller_RPS-10                                      22.86k ± ∞ ¹                   24.33k ± ∞ ¹  ~ (p=1.000 n=1) ²                   24.36k ± ∞ ¹  ~ (p=1.000 n=1) ²
¹ need >= 6 samples for confidence interval at level 0.95
² need >= 4 samples to detect a difference at alpha level 0.05
```

---

### B5 — Bottleneck Analysis

> Note on the CPU profile: >80% of raw samples (`syscall.rawsyscalln` 53.5%, `runtime.pthread_cond_signal` 13.1%, `runtime.kevent` 6.0%, `runtime.usleep` 5.1%, `runtime.pthread_cond_wait` 4.3%) are OS-level syscall and goroutine-scheduling overhead inherent to `httptest`'s real loopback TCP sockets — not adapter business logic. The ranking below excludes this harness noise and focuses on the largest *adapter-attributable* contributors from the CPU and memory profiles.

| Rank | Plugin / Step | Estimated contribution | Evidence |
|:----:|---------------|------------------------|---------|
| 1 | `router` (`net/http/httputil.ReverseProxy`) | **38.7% of all allocated memory** (42.83 GB / 110.66 GB total) via `copyBuffer`; this allocation pressure also plausibly explains the mutex profile, which is dominated by `runtime.unlock`/`_LostContendedRuntimeLock` (97.5%) — GC/allocator lock contention, not application-level locking | `mem.prof`: `net/http/httputil.(*ReverseProxy).copyBuffer` 42.83GB/38.70%; `mutex.prof`: `runtime.unlock` 81.71% + `runtime._LostContendedRuntimeLock` 15.83% |
| 2 | `schemav2validator` (JSON decode of request payloads) | ~6% of allocated memory in `encoding/json.Unmarshal` and related reflection calls | `mem.prof`: `encoding/json.Unmarshal` 1.52GB/6.00% cum, `encoding/json.(*decodeState).objectInterface` 1.81GB/2.40% cum, `reflect.unsafe_New` 2.26GB/2.04% |
| 3 | `signer` + `simplekeymanager` (Ed25519/BLAKE-512 signing) | Dominant *application* CPU cost once harness syscall overhead is excluded — `edwards25519` field arithmetic (`feMulGeneric`, `feSquareGeneric`, `addMul`) | `cpu.prof`: `crypto/internal/fips140/edwards25519/field.feMulGeneric` 8.23s cum (2.15%), `feSquareGeneric` 5.53s cum (1.44%), `addMul`/`addMul19`/`Element.Select` ~11.8s combined flat |

**Also notable (outside the 3 declared pipeline plugins):** OpenTelemetry instrumentation (metrics attributes, span creation, and the audit-log/signature-header capture added in [#827](https://github.com/beckn/beckn-onix/issues/827)/[#828](https://github.com/beckn/beckn-onix/issues/828)) accounts for roughly 7.8% of allocated memory — `otel/metric.WithAttributes` 2.88GB, `otel/attribute.computeDataFixed` 2.61GB, `otel/internal/global.(*tracer).newSpan` 1.52GB, `core/module/handler.setBecknAttr` 1.68GB. Worth watching as telemetry coverage expands.

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

*Generated from run `2026-07-02_14-25-21` · beckn-onix v1.7.2 · Beckn Protocol v2.0.0*
