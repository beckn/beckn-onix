package otelsetup

import (
	"context"
	"fmt"

	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	logsdk "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Setup wires the telemetry provider. This is the concrete implementation
// behind the OtelSetupMetricsProvider interface.
type Setup struct{}

// Config represents OpenTelemetry related configuration.
type Config struct {
	ServiceName       string `yaml:"serviceName"`
	ServiceVersion    string `yaml:"serviceVersion"`
	Environment       string `yaml:"environment"`
	Domain            string `yaml:"domain"`
	DeviceID          string `yaml:"deviceID"`
	EnableMetrics     bool   `yaml:"enableMetrics"`
	EnableTracing     bool   `yaml:"enableTracing"`
	EnableLogs        bool   `yaml:"enableLogs"`
	OtlpEndpoint      string `yaml:"otlpEndpoint"`
	TimeInterval      int64  `yaml:"timeInterval"`
	AuditFieldsConfig string `yaml:"auditFieldsConfig"`
	Producer          string `yaml:"producer"`
	ProducerType      string `yaml:"producerType"`
	CacheTTL          int64  `yaml:"cacheTTL"`
}

// DefaultConfig returns sensible defaults for telemetry configuration.
func DefaultConfig() *Config {
	return &Config{
		ServiceName:    "beckn-onix",
		ServiceVersion: "dev",
		Environment:    "development",
		Domain:         "",
		DeviceID:       "beckn-onix-device",
		OtlpEndpoint:   "localhost:4317",
		TimeInterval:   5,
	}
}

// ToPluginConfig converts Config to plugin.Config format.
func ToPluginConfig(cfg *Config) *plugin.Config {
	return &plugin.Config{
		ID: "otelsetup",
		Config: map[string]string{
			"serviceName":       cfg.ServiceName,
			"serviceVersion":    cfg.ServiceVersion,
			"environment":       cfg.Environment,
			"domain":            cfg.Domain,
			"enableMetrics":     fmt.Sprintf("%t", cfg.EnableMetrics),
			"enableTracing":     fmt.Sprintf("%t", cfg.EnableTracing),
			"enableLogs":        fmt.Sprintf("%t", cfg.EnableLogs),
			"otlpEndpoint":      cfg.OtlpEndpoint,
			"deviceID":          cfg.DeviceID,
			"timeInterval":      fmt.Sprintf("%d", cfg.TimeInterval),
			"auditFieldsConfig": cfg.AuditFieldsConfig,
		},
	}
}

// New initializes the underlying telemetry provider. The returned provider
// exposes the HTTP handler and shutdown hooks that the core application can
// manage directly.
func (Setup) New(ctx context.Context, cfg *Config) (*telemetry.Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("telemetry config cannot be nil")
	}

	// Apply defaults if fields are empty
	if cfg.ServiceName == "" {
		cfg.ServiceName = DefaultConfig().ServiceName
	}
	if cfg.ServiceVersion == "" {
		cfg.ServiceVersion = DefaultConfig().ServiceVersion
	}
	if cfg.Environment == "" {
		cfg.Environment = DefaultConfig().Environment
	}
	if cfg.Domain == "" {
		cfg.Domain = DefaultConfig().Domain
	}
	if cfg.DeviceID == "" {
		cfg.DeviceID = DefaultConfig().DeviceID
	}
	if cfg.TimeInterval == 0 {
		cfg.TimeInterval = DefaultConfig().TimeInterval
	}

	if !cfg.EnableMetrics && !cfg.EnableTracing && !cfg.EnableLogs {
		log.Info(ctx, "OpenTelemetry metrics, tracing, and logs are all disabled")
		return &telemetry.Provider{
			Shutdown: func(context.Context) error { return nil },
		}, nil
	}

	baseAttrs := []attribute.KeyValue{
		attribute.String("service.name", cfg.ServiceName),
		attribute.String("service.version", cfg.ServiceVersion),
		attribute.String("environment", cfg.Environment),
		attribute.String("domain", cfg.Domain),
		attribute.String("device_id", cfg.DeviceID),
		attribute.String("producerType", cfg.ProducerType),
		attribute.String("producer", cfg.Producer),
	}

	var meterProvider *metric.MeterProvider
	if cfg.EnableMetrics {
		resMetric, err := resource.New(ctx, resource.WithAttributes(buildAtts(baseAttrs, "METRIC")...))
		if err != nil {
			return nil, fmt.Errorf("failed to create telemetry resource for metric: %w", err)
		}
		metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint(cfg.OtlpEndpoint),
			otlpmetricgrpc.WithDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
		if err != nil {
			return nil, fmt.Errorf("failed to create OTLP metric exporter: %w", err)
		}
		reader := metric.NewPeriodicReader(metricExporter, metric.WithInterval(time.Second*time.Duration(cfg.TimeInterval)))
		meterProvider = metric.NewMeterProvider(metric.WithReader(reader), metric.WithResource(resMetric))
		otel.SetMeterProvider(meterProvider)
		log.Infof(ctx, "OpenTelemetry metrics initialized for service=%s version=%s env=%s (OTLP endpoint=%s)",
			cfg.ServiceName, cfg.ServiceVersion, cfg.Environment, cfg.OtlpEndpoint)
		if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(runtime.DefaultMinimumReadMemStatsInterval)); err != nil {
			log.Warnf(ctx, "Failed to start Go runtime instrumentation: %v", err)
		}
	}

	var traceProvider *trace.TracerProvider
	if cfg.EnableTracing {
		resTrace, err := resource.New(ctx, resource.WithAttributes(buildAtts(baseAttrs, "API")...))
		if err != nil {
			return nil, fmt.Errorf("failed to create trace resource: %w", err)
		}
		traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(cfg.OtlpEndpoint), otlptracegrpc.WithDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
		if err != nil {
			return nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
		}
		traceProvider = trace.NewTracerProvider(trace.WithBatcher(traceExporter), trace.WithResource(resTrace))
		otel.SetTracerProvider(traceProvider)
		log.Infof(ctx, "OpenTelemetry tracing initialized for service=%s (OTLP endpoint=%s)",
			cfg.ServiceName, cfg.OtlpEndpoint)
	}

	var logProvider *logsdk.LoggerProvider
	if cfg.EnableLogs {
		resAudit, err := resource.New(ctx, resource.WithAttributes(buildAtts(baseAttrs, "AUDIT")...))
		if err != nil {
			return nil, fmt.Errorf("failed to create audit resource: %w", err)
		}
		logExporter, err := otlploggrpc.New(ctx, otlploggrpc.WithEndpoint(cfg.OtlpEndpoint), otlploggrpc.WithDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
		if err != nil {
			return nil, fmt.Errorf("failed to create OTLP logs exporter: %w", err)
		}
		processor := logsdk.NewBatchProcessor(logExporter)
		logProvider = logsdk.NewLoggerProvider(logsdk.WithProcessor(processor), logsdk.WithResource(resAudit))
		global.SetLoggerProvider(logProvider)
	}

	return &telemetry.Provider{
		MeterProvider: meterProvider,
		TraceProvider: traceProvider,
		LogProvider:   logProvider,
		Shutdown: func(shutdownCtx context.Context) error {

			var errs []error
			if traceProvider != nil {
				if err := traceProvider.Shutdown(shutdownCtx); err != nil {
					errs = append(errs, fmt.Errorf("tracer shutdown: %w", err))
				}
			}
			if meterProvider != nil {
				if err := meterProvider.Shutdown(shutdownCtx); err != nil {
					errs = append(errs, fmt.Errorf("meter shutdown: %w", err))
				}
			}

			if logProvider != nil {
				if err := logProvider.Shutdown(shutdownCtx); err != nil {
					errs = append(errs, fmt.Errorf("logs shutdown: %w", err))
				}
			}
			if len(errs) > 0 {
				return fmt.Errorf("shutdown errors: %v", errs)
			}
			return nil
		},
	}, nil
}

func buildAtts(base []attribute.KeyValue, eid string) []attribute.KeyValue {
	atts := make([]attribute.KeyValue, 0, len(base)+1)
	atts = append(atts, base...)
	atts = append(atts, attribute.String("eid", eid))
	return atts
}
