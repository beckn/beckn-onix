package cache

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// CacheMetrics exposes cache-related metric instruments.
type CacheMetrics struct {
	CacheOperationsTotal metric.Int64Counter
	CacheHitsTotal       metric.Int64Counter
	CacheMissesTotal     metric.Int64Counter
}

// GetCacheMetrics returns fresh CacheMetrics bound to the current global meter
// provider. otel.GetMeterProvider() is safe to call repeatedly; the SDK
// deduplicates instruments by name, so there is no double-registration risk.
func GetCacheMetrics(ctx context.Context) (*CacheMetrics, error) {
	return newCacheMetrics()
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


