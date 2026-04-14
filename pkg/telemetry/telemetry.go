package telemetry

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
)

const (
	ScopeName    = "beckn-onix"
	ScopeVersion = "v2.0.0"
)

// logEnabled is set to true by otelsetup when a real log provider is registered.
// global.GetLoggerProvider() always returns a no-op provider (never nil), so we
// use this flag to distinguish "logs configured" from "logs disabled".
var logEnabled atomic.Bool

// SetLogsEnabled marks whether a real OTel log provider has been registered.
// It must be called by otelsetup after calling global.SetLoggerProvider.
func SetLogsEnabled(enabled bool) {
	logEnabled.Store(enabled)
}

// LogsEnabled reports whether a real OTel log provider is active.
func LogsEnabled() bool {
	return logEnabled.Load()
}

// Provider holds references to telemetry components that need coordinated shutdown.
type Provider struct {
	MeterProvider *metric.MeterProvider
	TraceProvider *trace.TracerProvider
	LogProvider   *log.LoggerProvider
	Shutdown      func(context.Context) error
}
