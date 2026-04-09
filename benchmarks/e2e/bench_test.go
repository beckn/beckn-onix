package e2e_bench_test

import (
	"net/http"
	"sort"
	"testing"
	"time"
)

// ── BenchmarkBAPCaller_Discover ───────────────────────────────────────────────
// Baseline single-goroutine throughput and latency for the discover endpoint.
// Exercises the full bapTxnCaller pipeline: addRoute → sign → validateSchema.
func BenchmarkBAPCaller_Discover(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req := buildSignedRequest(b, "discover")
		if err := sendRequest(req); err != nil {
			b.Errorf("iteration %d: %v", i, err)
		}
	}
}

// ── BenchmarkBAPCaller_Discover_Parallel ─────────────────────────────────────
// Measures throughput under concurrent load. Run with -cpu=1,2,4,8,16 to
// produce a concurrency sweep. Each goroutine runs its own request loop.
func BenchmarkBAPCaller_Discover_Parallel(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := buildSignedRequest(b, "discover")
			if err := sendRequest(req); err != nil {
				b.Errorf("parallel: %v", err)
			}
		}
	})
}

// ── BenchmarkBAPCaller_AllActions ────────────────────────────────────────────
// Measures per-action latency for discover, select, init, and confirm in a
// single benchmark run. Each sub-benchmark is independent.
func BenchmarkBAPCaller_AllActions(b *testing.B) {
	actions := []string{"discover", "select", "init", "confirm"}

	for _, action := range actions {
		action := action // capture for sub-benchmark closure
		b.Run(action, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req := buildSignedRequest(b, action)
				if err := sendRequest(req); err != nil {
					b.Errorf("action %s iteration %d: %v", action, i, err)
				}
			}
		})
	}
}

// ── BenchmarkBAPCaller_Discover_Percentiles ───────────────────────────────────
// Collects individual request durations and reports p50, p95, and p99 latency
// in microseconds via b.ReportMetric. The percentile data is only meaningful
// when -benchtime is at least 5s (default used in run_benchmarks.sh).
func BenchmarkBAPCaller_Discover_Percentiles(b *testing.B) {
	durations := make([]time.Duration, 0, b.N)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req := buildSignedRequest(b, "discover")
		start := time.Now()
		if err := sendRequest(req); err != nil {
			b.Errorf("iteration %d: %v", i, err)
			continue
		}
		durations = append(durations, time.Since(start))
	}

	// Compute and report percentiles.
	if len(durations) == 0 {
		return
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	p50 := durations[len(durations)*50/100]
	p95 := durations[len(durations)*95/100]
	p99 := durations[len(durations)*99/100]

	b.ReportMetric(float64(p50.Microseconds()), "p50_µs")
	b.ReportMetric(float64(p95.Microseconds()), "p95_µs")
	b.ReportMetric(float64(p99.Microseconds()), "p99_µs")
}

// ── BenchmarkBAPCaller_CacheWarm / CacheCold ─────────────────────────────────
// Compares latency when the Redis cache holds a pre-warmed key set (CacheWarm)
// vs. when each iteration has a fresh message_id that the cache has never seen
// (CacheCold). The delta reveals the key-lookup overhead on a cold path.

// BenchmarkBAPCaller_CacheWarm sends a fixed body (constant message_id) so the
// simplekeymanager's Redis cache is hit on every iteration after the first.
func BenchmarkBAPCaller_CacheWarm(b *testing.B) {
	body := warmFixtureBody(b, "discover")

	// Warm-up: send once to populate the cache before the timer starts.
	warmReq := buildSignedRequestFixed(b, "discover", body)
	if err := sendRequest(warmReq); err != nil {
		b.Fatalf("cache warm-up request failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req := buildSignedRequestFixed(b, "discover", body)
		if err := sendRequest(req); err != nil {
			b.Errorf("CacheWarm iteration %d: %v", i, err)
		}
	}
}

// BenchmarkBAPCaller_CacheCold uses a fresh message_id per iteration, so every
// request experiences a cache miss and a full key-derivation round-trip.
func BenchmarkBAPCaller_CacheCold(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req := buildSignedRequest(b, "discover") // fresh IDs each time
		if err := sendRequest(req); err != nil {
			b.Errorf("CacheCold iteration %d: %v", i, err)
		}
	}
}

// ── BenchmarkBAPCaller_RPS ────────────────────────────────────────────────────
// Reports requests-per-second as a custom metric alongside the default ns/op.
// Run with -benchtime=30s for a stable RPS reading.
func BenchmarkBAPCaller_RPS(b *testing.B) {
	b.ReportAllocs()

	var count int64
	start := time.Now()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var local int64
		for pb.Next() {
			req := buildSignedRequest(b, "discover")
			if err := sendRequest(req); err == nil {
				local++
			}
		}
		// Accumulate without atomic for simplicity — final value only read after
		// RunParallel returns and all goroutines have exited.
		count += local
	})

	elapsed := time.Since(start).Seconds()
	if elapsed > 0 {
		b.ReportMetric(float64(count)/elapsed, "req/s")
	}
}

// ── helper: one-shot HTTP client ─────────────────────────────────────────────

// benchHTTPClient is a shared client for all benchmark goroutines.
// MaxConnsPerHost caps the total active connections to localhost so we don't
// exhaust the OS ephemeral port range. MaxIdleConnsPerHost keeps that many
// connections warm in the pool so parallel goroutines reuse them rather than
// opening fresh TCP connections on every request.
var benchHTTPClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 200,
		MaxConnsPerHost:     200,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true, // no benefit compressing localhost traffic
	},
}
