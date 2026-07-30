package crawler

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/runner"
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

// ParseLogLevel maps CRAWLER_LOG_LEVEL to a slog.Level: the runner's own
// per-index/per-catalog trace lines (logPolled, logQueued, logSyncing, ...)
// are minted at Debug, which slog's default handler level (Info) drops — so
// without this, no deployment can ever see them regardless of the onix
// core logger's own log.level (that setting governs a separate sink; see the
// "Logs" section of the plugin README, pkg/plugin/implementation/crawler/README.md).
// Unrecognized or empty defaults to Info.
func ParseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
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
	outcome  metric.Int64Counter
	queue    metric.Int64Gauge
	parked   metric.Int64Gauge
	tracked  metric.Int64Gauge
	pushHist metric.Float64Histogram
	indexHst metric.Float64Histogram
	lagHist  metric.Float64Histogram
	poll     metric.Int64Counter

	mu          sync.Mutex
	lastSuccess map[string]time.Time
}

// NewOTelMetrics builds the crawler's OTel instruments. The
// seconds-since-last-success liveness gauge is an observable gauge driven by a
// per-job last-success timestamp updated by MarkPassSuccess.
func NewOTelMetrics(m metric.Meter) (runner.Metrics, error) {
	outcome, err := m.Int64Counter("crawler_sync_outcome_total")
	if err != nil {
		return nil, err
	}
	queue, err := m.Int64Gauge("crawler_queue_depth")
	if err != nil {
		return nil, err
	}
	parked, err := m.Int64Gauge("crawler_catalogs_parked")
	if err != nil {
		return nil, err
	}
	tracked, err := m.Int64Gauge("crawler_catalogs_tracked")
	if err != nil {
		return nil, err
	}
	pushHist, err := m.Float64Histogram("crawler_push_latency_seconds")
	if err != nil {
		return nil, err
	}
	indexHst, err := m.Float64Histogram("crawler_index_crawl_seconds")
	if err != nil {
		return nil, err
	}
	lagHist, err := m.Float64Histogram("crawler_sync_lag_seconds")
	if err != nil {
		return nil, err
	}
	poll, err := m.Int64Counter("crawler_index_poll_total")
	if err != nil {
		return nil, err
	}
	o := &otelMetrics{
		outcome: outcome, queue: queue, parked: parked, tracked: tracked,
		pushHist: pushHist, indexHst: indexHst, lagHist: lagHist, poll: poll,
		lastSuccess: make(map[string]time.Time),
	}
	if _, err := m.Int64ObservableGauge("crawler_seconds_since_last_success",
		metric.WithInt64Callback(func(_ context.Context, obs metric.Int64Observer) error {
			o.mu.Lock()
			defer o.mu.Unlock()
			for job, t := range o.lastSuccess {
				obs.Observe(int64(time.Since(t).Seconds()), metric.WithAttributes(attribute.String("job", job)))
			}
			return nil
		})); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *otelMetrics) RecordSyncOutcome(outcome, fault string) {
	o.outcome.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("outcome", outcome), attribute.String("fault", fault)))
}
func (o *otelMetrics) MarkPassSuccess(job string) {
	o.mu.Lock()
	o.lastSuccess[job] = time.Now()
	o.mu.Unlock()
}
func (o *otelMetrics) SetQueueDepth(n int)     { o.queue.Record(context.Background(), int64(n)) }
func (o *otelMetrics) SetCatalogsParked(n int) { o.parked.Record(context.Background(), int64(n)) }
func (o *otelMetrics) SetCatalogsTracked(n int) {
	o.tracked.Record(context.Background(), int64(n))
}
func (o *otelMetrics) ObservePushSeconds(s float64)    { o.pushHist.Record(context.Background(), s) }
func (o *otelMetrics) ObserveIndexSeconds(s float64)   { o.indexHst.Record(context.Background(), s) }
func (o *otelMetrics) ObserveSyncLagSeconds(s float64) { o.lagHist.Record(context.Background(), s) }
func (o *otelMetrics) RecordIndexPoll(result string) {
	o.poll.Add(context.Background(), 1, metric.WithAttributes(attribute.String("result", result)))
}
