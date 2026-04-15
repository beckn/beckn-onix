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

// handlerMetricsCache caches HandlerMetrics for the current global MeterProvider.
// Instruments are rebound only when otel.SetMeterProvider changes the provider pointer.
var handlerMetricsCache struct {
	mu       sync.RWMutex
	provider metric.MeterProvider
	m        *HandlerMetrics
}

// GetHandlerMetrics returns HandlerMetrics bound to the current global MeterProvider,
// rebuilding only when the provider has been replaced since the last call.
func GetHandlerMetrics(_ context.Context) (*HandlerMetrics, error) {
	current := otel.GetMeterProvider()

	handlerMetricsCache.mu.RLock()
	if handlerMetricsCache.provider == current && handlerMetricsCache.m != nil {
		m := handlerMetricsCache.m
		handlerMetricsCache.mu.RUnlock()
		return m, nil
	}
	handlerMetricsCache.mu.RUnlock()

	handlerMetricsCache.mu.Lock()
	defer handlerMetricsCache.mu.Unlock()
	if handlerMetricsCache.provider == current && handlerMetricsCache.m != nil {
		return handlerMetricsCache.m, nil
	}
	m, err := newHandlerMetrics()
	if err != nil {
		return nil, err
	}
	handlerMetricsCache.provider = current
	handlerMetricsCache.m = m
	return m, nil
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
