package catalogcrawler

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/runner"
)

// NewSlogLogger adapts a *slog.Logger to the runner's Logger port, so drivers
// get structured JSON events without the module importing a logger globally.
// Passing nil uses slog.Default().
func NewSlogLogger(l *slog.Logger) runner.Logger {
	if l == nil {
		l = slog.Default()
	}
	return slogLogger{l: l}
}

type slogLogger struct{ l *slog.Logger }

func (s slogLogger) Debug(event string, kv ...any) { s.l.Debug(event, kv...) }
func (s slogLogger) Info(event string, kv ...any)  { s.l.Info(event, kv...) }
func (s slogLogger) Warn(event string, kv ...any)  { s.l.Warn(event, kv...) }
func (s slogLogger) Error(event string, kv ...any) { s.l.Error(event, kv...) }

// otelMetrics implements the runner's Metrics port over an OpenTelemetry meter.
// On a no-op global meter (e.g. the standalone driver without a provider) the
// instruments are inert, so this is always safe to use.
type otelMetrics struct {
	pushed    metric.Int64Counter
	failed    metric.Int64Counter
	queue     metric.Int64Gauge
	pushHist  metric.Float64Histogram
	indexHist metric.Float64Histogram
}

// NewOTelMetrics builds the crawler's OTel instruments (pushed/failed counters,
// queue-depth gauge, push + index histograms).
func NewOTelMetrics(m metric.Meter) (runner.Metrics, error) {
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
