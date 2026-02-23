package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
)

const (
	ScopeName    = "beckn-onix"
	ScopeVersion = "v2.0.0"
)

// Provider holds references to telemetry components that need coordinated shutdown.
type Provider struct {
	MeterProvider *metric.MeterProvider
	TraceProvider *trace.TracerProvider
	LogProvider   *log.LoggerProvider
	Shutdown      func(context.Context) error
}
