package handler

import (
	"context"
	"fmt"
	"sync"

	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// StepMetrics exposes step execution metric instruments.
type StepMetrics struct {
	StepExecutionDuration metric.Float64Histogram
	StepExecutionTotal    metric.Int64Counter
	StepErrorsTotal       metric.Int64Counter
}

// stepMetricsCache caches StepMetrics for the current global MeterProvider.
// Instruments are rebound only when otel.SetMeterProvider changes the provider pointer.
var stepMetricsCache struct {
	mu       sync.RWMutex
	provider metric.MeterProvider
	m        *StepMetrics
}

// GetStepMetrics returns StepMetrics bound to the current global MeterProvider,
// rebuilding only when the provider has been replaced since the last call.
func GetStepMetrics(_ context.Context) (*StepMetrics, error) {
	current := otel.GetMeterProvider()

	stepMetricsCache.mu.RLock()
	if stepMetricsCache.provider == current && stepMetricsCache.m != nil {
		m := stepMetricsCache.m
		stepMetricsCache.mu.RUnlock()
		return m, nil
	}
	stepMetricsCache.mu.RUnlock()

	stepMetricsCache.mu.Lock()
	defer stepMetricsCache.mu.Unlock()
	if stepMetricsCache.provider == current && stepMetricsCache.m != nil {
		return stepMetricsCache.m, nil
	}
	m, err := newStepMetrics()
	if err != nil {
		return nil, err
	}
	stepMetricsCache.provider = current
	stepMetricsCache.m = m
	return m, nil
}

func newStepMetrics() (*StepMetrics, error) {
	meter := otel.GetMeterProvider().Meter(
		telemetry.ScopeName,
		metric.WithInstrumentationVersion(telemetry.ScopeVersion),
	)

	m := &StepMetrics{}
	var err error

	if m.StepExecutionDuration, err = meter.Float64Histogram(
		"onix_step_execution_duration_seconds",
		metric.WithDescription("Duration of individual processing steps"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5),
	); err != nil {
		return nil, fmt.Errorf("onix_step_execution_duration_seconds: %w", err)
	}

	if m.StepExecutionTotal, err = meter.Int64Counter(
		"onix_step_executions_total",
		metric.WithDescription("Total processing step executions"),
		metric.WithUnit("{execution}"),
	); err != nil {
		return nil, fmt.Errorf("onix_step_executions_total: %w", err)
	}

	if m.StepErrorsTotal, err = meter.Int64Counter(
		"onix_step_errors_total",
		metric.WithDescription("Processing step errors"),
		metric.WithUnit("{error}"),
	); err != nil {
		return nil, fmt.Errorf("onix_step_errors_total: %w", err)
	}

	return m, nil
}
