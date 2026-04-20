package telemetry

import (
	"context"
	"sync"

	clientprom "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/log/global"
	logsdk "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// NewTestProvider creates a minimal telemetry provider for testing purposes.
// This avoids import cycles by not depending on the otelsetup package.
func NewTestProvider(ctx context.Context) (*Provider, error) {
	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			attribute.String("service.name", "test-service"),
			attribute.String("service.version", "test"),
			attribute.String("deployment.environment", "test"),
		),
	)
	if err != nil {
		return nil, err
	}

	registry := clientprom.NewRegistry()
	exporter, err := otelprom.New(
		otelprom.WithRegisterer(registry),
		otelprom.WithoutUnits(),
		otelprom.WithoutScopeInfo(),
	)
	if err != nil {
		return nil, err
	}

	meterProvider := metric.NewMeterProvider(
		metric.WithReader(exporter),
		metric.WithResource(res),
	)

	otel.SetMeterProvider(meterProvider)

	return &Provider{
		MeterProvider: meterProvider,
		Shutdown: func(ctx context.Context) error {
			return meterProvider.Shutdown(ctx)
		},
	}, nil
}

// RecordingLogExporter is a simple in-memory log exporter for testing.
// It collects emitted log records so test assertions can inspect them.
type RecordingLogExporter struct {
	mu      sync.Mutex
	records []logsdk.Record
}

func (e *RecordingLogExporter) Export(_ context.Context, records []logsdk.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range records {
		e.records = append(e.records, r.Clone())
	}
	return nil
}

func (e *RecordingLogExporter) Shutdown(_ context.Context) error  { return nil }
func (e *RecordingLogExporter) ForceFlush(_ context.Context) error { return nil }

// Records returns a snapshot of all collected log records.
func (e *RecordingLogExporter) Records() []logsdk.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]logsdk.Record, len(e.records))
	copy(out, e.records)
	return out
}

// NewTestProviderWithLogs creates a telemetry provider with metrics and an
// in-memory log recorder. It sets SetLogsEnabled(true) so EmitAuditLogs
// routes through the provider. The returned RecordingLogExporter lets tests
// assert on the emitted log records. Shutdown resets the logs-enabled flag.
func NewTestProviderWithLogs(ctx context.Context) (*Provider, *RecordingLogExporter, error) {
	provider, err := NewTestProvider(ctx)
	if err != nil {
		return nil, nil, err
	}

	exporter := &RecordingLogExporter{}
	processor := logsdk.NewSimpleProcessor(exporter)
	logProvider := logsdk.NewLoggerProvider(logsdk.WithProcessor(processor))
	global.SetLoggerProvider(logProvider)
	SetLogsEnabled(true)

	return &Provider{
		MeterProvider: provider.MeterProvider,
		LogProvider:   logProvider,
		Shutdown: func(shutdownCtx context.Context) error {
			SetLogsEnabled(false)
			var errs []error
			if err := logProvider.Shutdown(shutdownCtx); err != nil {
				errs = append(errs, err)
			}
			if err := provider.MeterProvider.Shutdown(shutdownCtx); err != nil {
				errs = append(errs, err)
			}
			if len(errs) > 0 {
				return errs[0]
			}
			return nil
		},
	}, exporter, nil
}

// NewTestProviderWithTrace creates a telemetry provider with both metrics and
// tracing enabled, using an in-memory span recorder. It returns the provider
// and the SpanRecorder so tests can assert on recorded spans.
func NewTestProviderWithTrace(ctx context.Context) (*Provider, *tracetest.SpanRecorder, error) {
	provider, err := NewTestProvider(ctx)
	if err != nil {
		return nil, nil, err
	}

	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			attribute.String("service.name", "test-service"),
			attribute.String("service.version", "test"),
			attribute.String("deployment.environment", "test"),
		),
	)
	if err != nil {
		return nil, nil, err
	}

	sr := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(
		trace.WithSpanProcessor(sr),
		trace.WithResource(res),
	)
	otel.SetTracerProvider(traceProvider)

	return &Provider{
		MeterProvider: provider.MeterProvider,
		TraceProvider: traceProvider,
		Shutdown: func(ctx context.Context) error {
			var errs []error
			if err := traceProvider.Shutdown(ctx); err != nil {
				errs = append(errs, err)
			}
			if err := provider.MeterProvider.Shutdown(ctx); err != nil {
				errs = append(errs, err)
			}
			if len(errs) > 0 {
				return errs[0]
			}
			return nil
		},
	}, sr, nil
}
