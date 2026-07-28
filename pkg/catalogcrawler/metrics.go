package catalogcrawler

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metrics is the engine's metrics sink (injected; NopMetrics by default so
// the module stays framework-agnostic). Reasons passed to CatalogFailed are
// low-cardinality categories, never full error strings.
type Metrics interface {
	CatalogPushed()
	CatalogFailed(reason string)
	SetQueueDepth(n int)
	ObservePushSeconds(seconds float64)
	ObserveIndexSeconds(seconds float64)
}

// NopMetrics discards all metrics.
type NopMetrics struct{}

func (NopMetrics) CatalogPushed()              {}
func (NopMetrics) CatalogFailed(string)        {}
func (NopMetrics) SetQueueDepth(int)           {}
func (NopMetrics) ObservePushSeconds(float64)  {}
func (NopMetrics) ObserveIndexSeconds(float64) {}

// otelMetrics implements Metrics over an OpenTelemetry meter. On a no-op
// global meter (e.g. the standalone driver without a provider) the
// instruments are inert, so this is always safe to use.
type otelMetrics struct {
	pushed    metric.Int64Counter
	failed    metric.Int64Counter
	queue     metric.Int64Gauge
	pushHist  metric.Float64Histogram
	indexHist metric.Float64Histogram
}

// NewOTelMetrics builds the crawler's OTel instruments (design §07:
// pushed/failed counters, queue-depth gauge, push + index histograms).
func NewOTelMetrics(m metric.Meter) (Metrics, error) {
	pushed, err := m.Int64Counter("crawler_catalogs_pushed_total")
	if err != nil {
		return nil, err
	}
	failed, err := m.Int64Counter("crawler_catalogs_failed_total")
	if err != nil {
		return nil, err
	}
	queue, err := m.Int64Gauge("crawler_queue_depth")
	if err != nil {
		return nil, err
	}
	pushHist, err := m.Float64Histogram("crawler_push_latency_seconds")
	if err != nil {
		return nil, err
	}
	indexHist, err := m.Float64Histogram("crawler_index_crawl_seconds")
	if err != nil {
		return nil, err
	}
	return &otelMetrics{pushed: pushed, failed: failed, queue: queue, pushHist: pushHist, indexHist: indexHist}, nil
}

func (o *otelMetrics) CatalogPushed() { o.pushed.Add(context.Background(), 1) }
func (o *otelMetrics) CatalogFailed(reason string) {
	o.failed.Add(context.Background(), 1, metric.WithAttributes(attribute.String("reason", reason)))
}
func (o *otelMetrics) SetQueueDepth(n int)           { o.queue.Record(context.Background(), int64(n)) }
func (o *otelMetrics) ObservePushSeconds(s float64)  { o.pushHist.Record(context.Background(), s) }
func (o *otelMetrics) ObserveIndexSeconds(s float64) { o.indexHist.Record(context.Background(), s) }
