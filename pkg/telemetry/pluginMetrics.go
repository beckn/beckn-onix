package telemetry

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metrics exposes strongly typed metric instruments used across the adapter.
// Note: Most metrics have been moved to their respective modules. Only plugin-level
// metrics remain here. See:
// - OTel setup: pkg/plugin/implementation/otelsetup
// - Step metrics: core/module/handler/step_metrics.go
// - Cache metrics: pkg/plugin/implementation/cache/cache_metrics.go
// - Handler metrics: core/module/handler/handlerMetrics.go
type Metrics struct {
	PluginExecutionDuration metric.Float64Histogram
	PluginErrorsTotal       metric.Int64Counter
}

var (
	metricsInstance *Metrics
	metricsOnce     sync.Once
	metricsErr      error
)

// Attribute keys shared across instruments.
var (
	AttrModule               = attribute.Key("module")
	AttrCaller               = attribute.Key("caller") // who is calling bab/bpp with there name
	AttrStep                 = attribute.Key("step")
	AttrRole                 = attribute.Key("role")
	AttrAction               = attribute.Key("action")           // action is context.action
	AttrHTTPStatus           = attribute.Key("http_status_code") // status code is 2xx/3xx/4xx/5xx
	AttrStatus               = attribute.Key("status")
	AttrErrorType            = attribute.Key("error_type")
	AttrPluginID             = attribute.Key("plugin_id")   // id for the plugine
	AttrPluginType           = attribute.Key("plugin_type") // type for the plugine
	AttrOperation            = attribute.Key("operation")
	AttrRouteType            = attribute.Key("route_type") // publish/ uri
	AttrTargetType           = attribute.Key("target_type")
	AttrSchemaVersion        = attribute.Key("schema_version")
	AttrMetricUUID           = attribute.Key("metric_uuid")
	AttrMetricCode           = attribute.Key("metric.code")
	AttrMetricCategory       = attribute.Key("metric.category")
	AttrMetricGranularity    = attribute.Key("metric.granularity")
	AttrMetricFrequency      = attribute.Key("metric.frequency")
	AttrObservedTimeUnixNano = attribute.Key("observedTimeUnixNano")
	AttrMatricLabels         = attribute.Key("metric.labels")
	AttrSenderID             = attribute.Key("sender.id")
	AttrRecipientID          = attribute.Key("recipient.id")
)

var (
	networkMetricsCfgMu       sync.RWMutex
	networkMetricsGranularity = "10mim" // default
	networkMetricsFrequency   = "10mim" // default
)

func SetNetworkMetricsConfig(granularity, frequency string) {
	networkMetricsCfgMu.Lock()
	defer networkMetricsCfgMu.Unlock()
	if granularity != "" {
		networkMetricsGranularity = granularity
	}
	if frequency != "" {
		networkMetricsFrequency = frequency
	}
}

func GetNetworkMetricsConfig() (granularity, frequency string) {
	networkMetricsCfgMu.RLock()
	defer networkMetricsCfgMu.RUnlock()
	return networkMetricsGranularity, networkMetricsFrequency
}

// GetMetrics lazily initializes instruments and returns a cached reference.
func GetMetrics(ctx context.Context) (*Metrics, error) {
	metricsOnce.Do(func() {
		metricsInstance, metricsErr = newMetrics()
	})
	return metricsInstance, metricsErr
}

func newMetrics() (*Metrics, error) {
	meter := otel.GetMeterProvider().Meter(
		ScopeName,
		metric.WithInstrumentationVersion(ScopeVersion),
	)

	m := &Metrics{}
	var err error

	if m.PluginExecutionDuration, err = meter.Float64Histogram(
		"onix_plugin_execution_duration_seconds",
		metric.WithDescription("Plugin execution time"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1),
	); err != nil {
		return nil, fmt.Errorf("onix_plugin_execution_duration_seconds: %w", err)
	}

	if m.PluginErrorsTotal, err = meter.Int64Counter(
		"onix_plugin_errors_total",
		metric.WithDescription("Plugin level errors"),
		metric.WithUnit("{error}"),
	); err != nil {
		return nil, fmt.Errorf("onix_plugin_errors_total: %w", err)
	}

	return m, nil
}
