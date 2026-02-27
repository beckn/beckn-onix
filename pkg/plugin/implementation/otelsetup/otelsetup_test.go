package otelsetup

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetup_New_Success(t *testing.T) {
	setup := Setup{}
	ctx := context.Background()

	tests := []struct {
		name string
		cfg  *Config
	}{
		{
			name: "Valid config with all fields",
			cfg: &Config{
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				EnableMetrics:  true,
				EnableTracing:  false,
				Environment:    "test",
				Domain:         "test-domain",
				DeviceID:       "test-device",
				OtlpEndpoint:   "localhost:4317",
				TimeInterval:   5,
			},
		},
		{
			name: "Valid config with metrics and tracing disabled",
			cfg: &Config{
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				EnableMetrics:  false,
				EnableTracing:  false,
				Environment:    "test",
			},
		},
		{
			name: "Config with empty fields uses defaults",
			cfg: &Config{
				ServiceName:    "",
				ServiceVersion: "",
				EnableMetrics:  true,
				EnableTracing:  false,
				Environment:    "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := setup.New(ctx, tt.cfg)

			require.NoError(t, err, "New() should not return error")
			require.NotNil(t, provider, "New() should return non-nil provider")
			require.NotNil(t, provider.Shutdown, "Provider should have shutdown function")

			if tt.cfg.EnableMetrics {
				assert.NotNil(t, provider.MeterProvider, "MeterProvider should be set when metrics enabled")
			}
			if tt.cfg.EnableTracing {
				assert.NotNil(t, provider.TraceProvider, "TraceProvider should be set when tracing enabled")
			}

			// Shutdown for cleanup. When metrics/tracing are enabled, shutdown may fail without a real OTLP backend.
			_ = provider.Shutdown(ctx)
		})
	}
}

func TestSetup_New_Failure(t *testing.T) {
	setup := Setup{}
	ctx := context.Background()

	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name:    "Nil config",
			cfg:     nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := setup.New(ctx, tt.cfg)

			if tt.wantErr {
				assert.Error(t, err, "New() should return error")
				assert.Nil(t, provider, "New() should return nil provider on error")
			} else {
				assert.NoError(t, err, "New() should not return error")
				assert.NotNil(t, provider, "New() should return non-nil provider")
			}
		})
	}
}

func TestSetup_New_DefaultValues(t *testing.T) {
	setup := Setup{}
	ctx := context.Background()

	// Test with empty fields - should use defaults
	cfg := &Config{
		ServiceName:    "",
		ServiceVersion: "",
		EnableMetrics:  true,
		EnableTracing:  false,
		Environment:    "",
		OtlpEndpoint:   "localhost:4317",
		TimeInterval:   5,
	}

	provider, err := setup.New(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Verify defaults are applied by checking that provider is functional
	assert.NotNil(t, provider.MeterProvider, "MeterProvider should be set with defaults")

	// Cleanup (shutdown may fail without a real OTLP backend)
	_ = provider.Shutdown(ctx)
}

func TestSetup_New_MetricsDisabled(t *testing.T) {
	setup := Setup{}
	ctx := context.Background()

	cfg := &Config{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		EnableMetrics:  false,
		EnableTracing:  false,
		Environment:    "test",
	}

	provider, err := setup.New(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// When metrics and tracing are disabled, MeterProvider and TraceProvider should be nil
	assert.Nil(t, provider.MeterProvider, "MeterProvider should be nil when metrics disabled")
	assert.Nil(t, provider.TraceProvider, "TraceProvider should be nil when tracing disabled")

	// Shutdown should still work
	err = provider.Shutdown(ctx)
	assert.NoError(t, err, "Shutdown should work even when metrics disabled")
}

func TestToPluginConfig_Success(t *testing.T) {
	tests := []struct {
		name           string
		cfg            *Config
		expectedID     string
		expectedConfig map[string]string
	}{
		{
			name: "Valid config with all fields",
			cfg: &Config{
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				EnableMetrics:  true,
				EnableTracing:  true,
				EnableLogs:     true,
				Environment:    "test",
				Domain:         "test-domain",
				DeviceID:       "test-device",
				OtlpEndpoint:   "localhost:4317",
				TimeInterval:   5,
			},
			expectedID: "otelsetup",
			expectedConfig: map[string]string{
				"serviceName":       "test-service",
				"serviceVersion":    "1.0.0",
				"environment":       "test",
				"domain":            "test-domain",
				"enableMetrics":     "true",
				"enableTracing":     "true",
				"enableLogs":        "true",
				"otlpEndpoint":      "localhost:4317",
				"deviceID":          "test-device",
				"timeInterval":      "5",
				"auditFieldsConfig": "",
			},
		},
		{
			name: "Config with enableMetrics and enableTracing false",
			cfg: &Config{
				ServiceName:    "my-service",
				ServiceVersion: "2.0.0",
				EnableMetrics:  false,
				EnableTracing:  false,
				Environment:    "production",
			},
			expectedID: "otelsetup",
			expectedConfig: map[string]string{
				"serviceName":       "my-service",
				"serviceVersion":    "2.0.0",
				"environment":       "production",
				"domain":            "",
				"enableMetrics":     "false",
				"enableTracing":     "false",
				"enableLogs":        "false",
				"otlpEndpoint":      "",
				"deviceID":          "",
				"timeInterval":      "0",
				"auditFieldsConfig": "",
			},
		},
		{
			name: "Config with empty fields",
			cfg: &Config{
				ServiceName:    "",
				ServiceVersion: "",
				EnableMetrics:  true,
				EnableTracing:  false,
				Environment:    "",
				Domain:         "",
				DeviceID:       "",
				OtlpEndpoint:   "",
			},
			expectedID: "otelsetup",
			expectedConfig: map[string]string{
				"serviceName":       "",
				"serviceVersion":    "",
				"environment":       "",
				"domain":            "",
				"enableMetrics":     "true",
				"enableTracing":     "false",
				"enableLogs":        "false",
				"otlpEndpoint":      "",
				"deviceID":          "",
				"timeInterval":      "0",
				"auditFieldsConfig": "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ToPluginConfig(tt.cfg)

			require.NotNil(t, result, "ToPluginConfig should return non-nil config")
			assert.Equal(t, tt.expectedID, result.ID, "Plugin ID should be 'otelsetup'")
			assert.Equal(t, tt.expectedConfig, result.Config, "Config map should match expected values")
		})
	}
}

func TestToPluginConfig_NilConfig(t *testing.T) {
	// Test that ToPluginConfig handles nil config
	// Note: This will panic if nil is passed, which is acceptable behavior
	// as the function expects a valid config. In practice, callers should check for nil.
	assert.Panics(t, func() {
		ToPluginConfig(nil)
	}, "ToPluginConfig should panic when given nil config")
}

func TestToPluginConfig_BooleanConversion(t *testing.T) {
	tests := []struct {
		name           string
		enableMetrics  bool
		enableTracing  bool
		expectedMetric string
		expectedTrace  string
	}{
		{
			name:           "EnableMetrics and EnableTracing true",
			enableMetrics:  true,
			enableTracing:  true,
			expectedMetric: "true",
			expectedTrace:  "true",
		},
		{
			name:           "EnableMetrics and EnableTracing false",
			enableMetrics:  false,
			enableTracing:  false,
			expectedMetric: "false",
			expectedTrace:  "false",
		},
		{
			name:           "EnableMetrics true, EnableTracing false",
			enableMetrics:  true,
			enableTracing:  false,
			expectedMetric: "true",
			expectedTrace:  "false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				ServiceName:    "test",
				ServiceVersion: "1.0.0",
				EnableMetrics:  tt.enableMetrics,
				EnableTracing:  tt.enableTracing,
				Environment:    "test",
				OtlpEndpoint:   "localhost:4317",
				DeviceID:       "test-device",
			}

			result := ToPluginConfig(cfg)
			require.NotNil(t, result)
			assert.Equal(t, tt.expectedMetric, result.Config["enableMetrics"], "enableMetrics should be converted to string correctly")
			assert.Equal(t, tt.expectedTrace, result.Config["enableTracing"], "enableTracing should be converted to string correctly")
			assert.Equal(t, "localhost:4317", result.Config["otlpEndpoint"], "otlpEndpoint should be included")
			assert.Equal(t, "test-device", result.Config["deviceID"], "deviceID should be included")
		})
	}
}
