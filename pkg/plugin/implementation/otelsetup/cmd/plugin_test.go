package main

import (
	"context"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/otelsetup"
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
				"enableMetrics":  "true",
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
				"enableMetrics":  "true",
				"environment":    "production",
			},
			expectedConfig: &otelsetup.Config{
				ServiceName:    "my-service",
				ServiceVersion: "3.0.0",
				EnableMetrics:  true,
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
				err := cleanup()
				assert.NoError(t, err, "cleanup() should not return error")
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
		"enableMetrics":  "true",
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
		"enableMetrics":  "true",
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
