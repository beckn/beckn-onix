package cache

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// CacheMetrics exposes cache-related metric instruments.
type CacheMetrics struct {
	CacheOperationsTotal metric.Int64Counter
	CacheHitsTotal       metric.Int64Counter
	CacheMissesTotal     metric.Int64Counter
}

// cacheMetricsCache caches the CacheMetrics for the current global MeterProvider.
// Instruments are rebound only when otel.SetMeterProvider changes the provider pointer.
var cacheMetricsCache struct {
	mu       sync.RWMutex
	provider metric.MeterProvider
	m        *CacheMetrics
}

// GetCacheMetrics returns CacheMetrics bound to the current global MeterProvider,
// rebuilding only when the provider has been replaced since the last call.
func GetCacheMetrics(_ context.Context) (*CacheMetrics, error) {
	current := otel.GetMeterProvider()

	cacheMetricsCache.mu.RLock()
	if cacheMetricsCache.provider == current && cacheMetricsCache.m != nil {
		m := cacheMetricsCache.m
		cacheMetricsCache.mu.RUnlock()
		return m, nil
	}
	cacheMetricsCache.mu.RUnlock()

	cacheMetricsCache.mu.Lock()
	defer cacheMetricsCache.mu.Unlock()
	// Double-check after acquiring the write lock.
	if cacheMetricsCache.provider == current && cacheMetricsCache.m != nil {
		return cacheMetricsCache.m, nil
	}
	m, err := newCacheMetrics()
	if err != nil {
		return nil, err
	}
	cacheMetricsCache.provider = current
	cacheMetricsCache.m = m
	return m, nil
}

func newCacheMetrics() (*CacheMetrics, error) {
	meter := otel.GetMeterProvider().Meter(
		"github.com/beckn-one/beckn-onix/cache",
		metric.WithInstrumentationVersion("1.0.0"),
	)

	m := &CacheMetrics{}
	var err error

	if m.CacheOperationsTotal, err = meter.Int64Counter(
		"onix_cache_operations_total",
		metric.WithDescription("Redis cache operations"),
		metric.WithUnit("{operation}"),
	); err != nil {
		return nil, fmt.Errorf("onix_cache_operations_total: %w", err)
	}

	if m.CacheHitsTotal, err = meter.Int64Counter(
		"onix_cache_hits_total",
		metric.WithDescription("Redis cache hits"),
		metric.WithUnit("{hit}"),
	); err != nil {
		return nil, fmt.Errorf("onix_cache_hits_total: %w", err)
	}

	if m.CacheMissesTotal, err = meter.Int64Counter(
		"onix_cache_misses_total",
		metric.WithDescription("Redis cache misses"),
		metric.WithUnit("{miss}"),
	); err != nil {
		return nil, fmt.Errorf("onix_cache_misses_total: %w", err)
	}

	return m, nil
}


