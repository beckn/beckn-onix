package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type HTTPMetrics struct {
	HttpRequestCount metric.Int64Counter
}

var (
	httpMetricsInstance *HTTPMetrics
	httpMetricsOnce     sync.Once
	httpMetricsErr      error
)

func newHTTPMetrics() (*HTTPMetrics, error) {

	meter := otel.GetMeterProvider().Meter(telemetry.ScopeName,
		metric.WithInstrumentationVersion(telemetry.ScopeVersion))
	m := &HTTPMetrics{}
	var err error

	if m.HttpRequestCount, err = meter.Int64Counter(
		"onix_http_request_count",
		metric.WithDescription("Total HTTP requests by status, route, method, role and caller"),
		metric.WithUnit("1"),
	); err != nil {
		return nil, fmt.Errorf("onix_http_request_count: %w", err)
	}

	return m, nil
}

func GetHTTPMetrics(ctx context.Context) (*HTTPMetrics, error) {
	httpMetricsOnce.Do(func() {
		httpMetricsInstance, httpMetricsErr = newHTTPMetrics()
	})
	return httpMetricsInstance, httpMetricsErr
}

// StatusClass returns the HTTP status class string (e.g. 200 -> "2xx").
func StatusClass(statusCode int) string {
	switch {
	case statusCode >= 100 && statusCode < 200:
		return "1xx"
	case statusCode >= 200 && statusCode < 300:
		return "2xx"
	case statusCode >= 300 && statusCode < 400:
		return "3xx"
	case statusCode >= 400 && statusCode < 500:
		return "4xx"
	default:
		return "5xx"
	}
}

func RecordHTTPRequest(ctx context.Context, statusCode int, action, role, senderID, recipientID string) {
	m, err := GetHTTPMetrics(ctx)
	if err != nil || m == nil {
		return
	}
	status := StatusClass(statusCode)
	attributes := []attribute.KeyValue{
		telemetry.AttrHTTPStatus.String(status),
		telemetry.AttrAction.String(action),
		telemetry.AttrRole.String(role),
		telemetry.AttrSenderID.String(senderID),
		telemetry.AttrRecipientID.String(recipientID),
	}

	metric_code := action + "_api_total_count"
	category := "NetworkHealth"
	if strings.HasSuffix(action, "/search") || strings.HasSuffix(action, "/discovery") {
		category = "Discovery"
	}
	attributes = append(attributes, specHttpMetricAttr(metric_code, category)...)
	m.HttpRequestCount.Add(ctx, 1, metric.WithAttributes(attributes...))
}

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	written    bool
	record     func()
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	if !r.written {
		r.written = true
		r.statusCode = statusCode
		if r.record != nil {
			r.record()
		}
	}
	r.ResponseWriter.WriteHeader(statusCode)
}

func specHttpMetricAttr(metricCode, category string) []attribute.KeyValue {

	granularity, frequency := telemetry.GetNetworkMetricsConfig()
	return []attribute.KeyValue{
		telemetry.AttrMetricCode.String(metricCode),
		telemetry.AttrMetricCategory.String(category),
		telemetry.AttrMetricGranularity.String(granularity),
		telemetry.AttrMetricFrequency.String(frequency),
	}
}
