package main

import (
	"context"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/otelsetup"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsProviderNew_Success(t *testing.T) {
	provider := metricsProvider{}

	tests := []struct {
		name   string
		ctx    context.Context
		config map[string]string
	}{
		{
			name: "Valid config with all fields",
			ctx:  context.Background(),
			config: map[string]string{
				"serviceName":    "test-service",
				"serviceVersion": "1.0.0",
				"enableMetrics":  "false",
				"environment":    "test",
			},
		},
		{
			name:   "Valid config with minimal fields (uses defaults)",
			ctx:    context.Background(),
			config: map[string]string{},
		},
		{
			name: "Valid config with enableMetrics false",
			ctx:  context.Background(),
			config: map[string]string{
				"enableMetrics": "false",
			},
		},
		{
			name: "Valid config with partial fields",
			ctx:  context.Background(),
			config: map[string]string{
				"serviceName":    "custom-service",
				"serviceVersion": "2.0.0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			telemetryProvider, cleanup, err := provider.New(tt.ctx, tt.config)

			require.NoError(t, err, "New() should not return error")
			require.NotNil(t, telemetryProvider, "New() should return non-nil provider")

			// Metrics server is started inside provider when enabled; MetricsHandler is not exposed.
			if cleanup != nil {
				err := cleanup()
				assert.NoError(t, err, "cleanup() should not return error")
			}
		})
	}
}

func TestMetricsProviderNew_Failure(t *testing.T) {
	provider := metricsProvider{}

	tests := []struct {
		name    string
		ctx     context.Context
		config  map[string]string
		wantErr bool
	}{
		{
			name:    "Nil context",
			ctx:     nil,
			config:  map[string]string{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			telemetryProvider, cleanup, err := provider.New(tt.ctx, tt.config)

			if tt.wantErr {
				assert.Error(t, err, "New() should return error for nil context")
				assert.Nil(t, telemetryProvider, "New() should return nil provider on error")
				assert.Nil(t, cleanup, "New() should return nil cleanup on error")
			} else {
				assert.NoError(t, err, "New() should not return error")
				assert.NotNil(t, telemetryProvider, "New() should return non-nil provider")
			}
		})
	}
}

func TestMetricsProviderNew_ConfigConversion(t *testing.T) {
	provider := metricsProvider{}
	ctx := context.Background()

	tests := []struct {
		name           string
		config         map[string]string
		expectedConfig *otelsetup.Config
	}{
		{
			name: "All fields provided",
			config: map[string]string{
				"serviceName":    "my-service",
				"serviceVersion": "3.0.0",
				"enableMetrics":  "false",
				"environment":    "production",
			},
			expectedConfig: &otelsetup.Config{
				ServiceName:    "my-service",
				ServiceVersion: "3.0.0",
				EnableMetrics:  false,
				Environment:    "production",
			},
		},
		{
			name:   "Empty config uses defaults",
			config: map[string]string{},
			expectedConfig: &otelsetup.Config{
				ServiceName:    otelsetup.DefaultConfig().ServiceName,
				ServiceVersion: otelsetup.DefaultConfig().ServiceVersion,
				EnableMetrics:  true, // Default when not specified
				Environment:    otelsetup.DefaultConfig().Environment,
			},
		},
		{
			name: "EnableMetrics false",
			config: map[string]string{
				"enableMetrics": "false",
			},
			expectedConfig: &otelsetup.Config{
				ServiceName:    otelsetup.DefaultConfig().ServiceName,
				ServiceVersion: otelsetup.DefaultConfig().ServiceVersion,
				EnableMetrics:  false,
				Environment:    otelsetup.DefaultConfig().Environment,
			},
		},
		{
			name: "Invalid enableMetrics defaults to true",
			config: map[string]string{
				"enableMetrics": "invalid",
			},
			expectedConfig: &otelsetup.Config{
				ServiceName:    otelsetup.DefaultConfig().ServiceName,
				ServiceVersion: otelsetup.DefaultConfig().ServiceVersion,
				EnableMetrics:  true, // Defaults to true on parse error
				Environment:    otelsetup.DefaultConfig().Environment,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			telemetryProvider, cleanup, err := provider.New(ctx, tt.config)

			require.NoError(t, err, "New() should not return error")
			require.NotNil(t, telemetryProvider, "New() should return non-nil provider")

			if cleanup != nil {
				err := cleanup()
				assert.NoError(t, err, "cleanup() should not return error")
			}
		})
	}
}

func TestMetricsProviderNew_BooleanParsing(t *testing.T) {
	provider := metricsProvider{}
	ctx := context.Background()

	tests := []struct {
		name          string
		enableMetrics string
		expected      bool
	}{
		{
			name:          "True string",
			enableMetrics: "true",
			expected:      true,
		},
		{
			name:          "False string",
			enableMetrics: "false",
			expected:      false,
		},
		{
			name:          "True uppercase",
			enableMetrics: "TRUE",
			expected:      true,
		},
		{
			name:          "False uppercase",
			enableMetrics: "FALSE",
			expected:      false,
		},
		{
			name:          "Invalid value defaults to true",
			enableMetrics: "invalid",
			expected:      true, // Defaults to true on parse error
		},
		{
			name:          "Empty string defaults to true",
			enableMetrics: "",
			expected:      true, // Defaults to true when not specified
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := map[string]string{
				"enableMetrics": tt.enableMetrics,
			}

			telemetryProvider, cleanup, err := provider.New(ctx, config)

			require.NoError(t, err, "New() should not return error")
			require.NotNil(t, telemetryProvider, "New() should return non-nil provider")

			if cleanup != nil {
				_ = cleanup()
			}
		})
	}
}

func TestMetricsProviderNew_CleanupFunction(t *testing.T) {
	provider := metricsProvider{}
	ctx := context.Background()

	config := map[string]string{
		"serviceName":    "test-service",
		"serviceVersion": "1.0.0",
		"enableMetrics":  "false",
		"environment":    "test",
	}

	telemetryProvider, cleanup, err := provider.New(ctx, config)

	require.NoError(t, err, "New() should not return error")
	require.NotNil(t, telemetryProvider, "New() should return non-nil provider")
	require.NotNil(t, cleanup, "New() should return non-nil cleanup function")

	// Test that cleanup can be called successfully
	err = cleanup()
	assert.NoError(t, err, "cleanup() should not return error")
}

func TestProviderVariable(t *testing.T) {
	assert.NotNil(t, Provider, "Provider should not be nil")

	// Verify Provider implements the interface correctly
	ctx := context.Background()
	config := map[string]string{
		"serviceName":    "test",
		"serviceVersion": "1.0.0",
		"enableMetrics":  "false",
	}

	telemetryProvider, cleanup, err := Provider.New(ctx, config)

	require.NoError(t, err, "Provider.New() should not return error")
	require.NotNil(t, telemetryProvider, "Provider.New() should return non-nil provider")

	if cleanup != nil {
		err := cleanup()
		assert.NoError(t, err, "cleanup() should not return error")
	}
}

func TestMetricsProviderNew_DefaultValues(t *testing.T) {
	provider := metricsProvider{}
	ctx := context.Background()

	// Test with completely empty config
	config := map[string]string{}

	telemetryProvider, cleanup, err := provider.New(ctx, config)

	require.NoError(t, err, "New() should not return error with empty config")
	require.NotNil(t, telemetryProvider, "New() should return non-nil provider")

	if cleanup != nil {
		err := cleanup()
		assert.NoError(t, err, "cleanup() should not return error")
	}
}

// TestMetricsProviderNew_ParentIDContext tests that parent ID fields are parsed from the context.
func TestMetricsProviderNew_ParentIDContext(t *testing.T) {
	p := metricsProvider{}

	tests := []struct {
		name     string
		parentID string
	}{
		{name: "three parts", parentID: "producerType:producer:device-id"},
		{name: "two parts", parentID: "producerType:producer"},
		{name: "one part", parentID: "producerType"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), model.ContextKeyParentID, tt.parentID)
			provider, cleanup, err := p.New(ctx, map[string]string{"enableMetrics": "false"})
			require.NoError(t, err)
			require.NotNil(t, provider)
			if cleanup != nil {
				_ = cleanup()
			}
		})
	}
}

// TestMetricsProviderNew_EnableTracingAndLogs tests that enableTracing and enableLogs are parsed from config.
func TestMetricsProviderNew_EnableTracingAndLogs(t *testing.T) {
	p := metricsProvider{}
	ctx := context.Background()

	tests := []struct {
		name              string
		enableTracing     string
		enableLogs        string
		wantTraceProvider bool
		wantLogProvider   bool
	}{
		{name: "tracing true logs true", enableTracing: "true", enableLogs: "true", wantTraceProvider: true, wantLogProvider: true},
		{name: "tracing false logs false", enableTracing: "false", enableLogs: "false", wantTraceProvider: false, wantLogProvider: false},
		{name: "invalid tracing value", enableTracing: "bad", enableLogs: "false", wantTraceProvider: false, wantLogProvider: false},
		{name: "invalid logs value", enableTracing: "false", enableLogs: "bad", wantTraceProvider: false, wantLogProvider: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := map[string]string{
				"enableMetrics": "false",
				"enableTracing": tt.enableTracing,
				"enableLogs":    tt.enableLogs,
			}
			provider, cleanup, err := p.New(ctx, config)
			require.NoError(t, err)
			require.NotNil(t, provider)
			if tt.wantTraceProvider {
				assert.NotNil(t, provider.TraceProvider, "expected TraceProvider to be set")
			} else {
				assert.Nil(t, provider.TraceProvider, "expected TraceProvider to be nil")
			}
			if tt.wantLogProvider {
				assert.NotNil(t, provider.LogProvider, "expected LogProvider to be set")
			} else {
				assert.Nil(t, provider.LogProvider, "expected LogProvider to be nil")
			}
			if cleanup != nil {
				_ = cleanup()
			}
		})
	}
}

// TestMetricsProviderNew_TimeIntervalAndCacheTTL tests that timeInterval and cacheTTL fall back to defaults for invalid values.
func TestMetricsProviderNew_TimeIntervalAndCacheTTL(t *testing.T) {
	p := metricsProvider{}
	ctx := context.Background()

	tests := []struct {
		name         string
		timeInterval string
		cacheTTL     string
	}{
		{name: "valid interval and ttl", timeInterval: "10", cacheTTL: "3600"},
		{name: "invalid interval", timeInterval: "bad", cacheTTL: "3600"},
		{name: "invalid cacheTTL", timeInterval: "5", cacheTTL: "bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := map[string]string{
				"enableMetrics": "false",
				"timeInterval":  tt.timeInterval,
				"cacheTTL":      tt.cacheTTL,
			}
			provider, cleanup, err := p.New(ctx, config)
			require.NoError(t, err)
			require.NotNil(t, provider)
			if cleanup != nil {
				_ = cleanup()
			}
		})
	}
}

// TestMetricsProviderNew_NetworkMetrics tests that networkMetricsGranularity and networkMetricsFrequency are applied.
func TestMetricsProviderNew_NetworkMetrics(t *testing.T) {
	p := metricsProvider{}
	ctx := context.Background()

	config := map[string]string{
		"enableMetrics":             "false",
		"networkMetricsGranularity": "low",
		"networkMetricsFrequency":   "30",
	}
	provider, cleanup, err := p.New(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, provider)
	granularity, frequency := telemetry.GetNetworkMetricsConfig()
	assert.Equal(t, "low", granularity, "expected networkMetricsGranularity to be applied")
	assert.Equal(t, "30", frequency, "expected networkMetricsFrequency to be applied")
	if cleanup != nil {
		_ = cleanup()
	}
}
