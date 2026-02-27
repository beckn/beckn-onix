package handler

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// HandlerMetrics exposes handler-related metric instruments.
type HandlerMetrics struct {
	SignatureValidationsTotal metric.Int64Counter
	SchemaValidationsTotal    metric.Int64Counter
	RoutingDecisionsTotal     metric.Int64Counter
}

var (
	handlerMetricsInstance *HandlerMetrics
	handlerMetricsOnce     sync.Once
	handlerMetricsErr      error
)

// GetHandlerMetrics lazily initializes handler metric instruments and returns a cached reference.
func GetHandlerMetrics(ctx context.Context) (*HandlerMetrics, error) {
	handlerMetricsOnce.Do(func() {
		handlerMetricsInstance, handlerMetricsErr = newHandlerMetrics()
	})
	return handlerMetricsInstance, handlerMetricsErr
}

func newHandlerMetrics() (*HandlerMetrics, error) {
	meter := otel.GetMeterProvider().Meter(
		"github.com/beckn-one/beckn-onix/handler",
		metric.WithInstrumentationVersion("1.0.0"),
	)

	m := &HandlerMetrics{}
	var err error

	if m.SignatureValidationsTotal, err = meter.Int64Counter(
		"beckn_signature_validations_total",
		metric.WithDescription("Signature validation attempts"),
		metric.WithUnit("{validation}"),
	); err != nil {
		return nil, fmt.Errorf("beckn_signature_validations_total: %w", err)
	}

	if m.SchemaValidationsTotal, err = meter.Int64Counter(
		"beckn_schema_validations_total",
		metric.WithDescription("Schema validation attempts"),
		metric.WithUnit("{validation}"),
	); err != nil {
		return nil, fmt.Errorf("beckn_schema_validations_total: %w", err)
	}

	if m.RoutingDecisionsTotal, err = meter.Int64Counter(
		"onix_routing_decisions_total",
		metric.WithDescription("Routing decisions taken by handler"),
		metric.WithUnit("{decision}"),
	); err != nil {
		return nil, fmt.Errorf("onix_routing_decisions_total: %w", err)
	}

	return m, nil
}
