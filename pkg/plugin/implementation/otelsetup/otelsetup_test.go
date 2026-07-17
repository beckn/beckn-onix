package otelsetup

import (
	"context"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

// TestDefaultConfig_ServiceVersionFollowsPkgVersion asserts DefaultConfig's
// ServiceVersion tracks pkg/version.Version rather than a fixed literal --
// regression test for otelsetup.go defaulting to "dev" unconditionally.
func TestDefaultConfig_ServiceVersionFollowsPkgVersion(t *testing.T) {
	original := version.Version
	defer func() { version.Version = original }()

	version.Version = "v9.9.9-test"
	assert.Equal(t, "v9.9.9-test", DefaultConfig().ServiceVersion,
		"DefaultConfig().ServiceVersion should follow pkg/version.Version")
}

// TestBuildBaseAttrs_IncludesBuildIdentity asserts the onix.build.* resource
// attributes are present and reflect pkg/version's current values -- these
// are attached to every metric/trace/log Resource via buildAtts(), which has
// no public getter to assert against directly on the OTel SDK providers.
func TestBuildBaseAttrs_IncludesBuildIdentity(t *testing.T) {
	origCommit, origTreeState, origDate := version.GitCommit, version.GitTreeState, version.BuildDate
	defer func() {
		version.GitCommit, version.GitTreeState, version.BuildDate = origCommit, origTreeState, origDate
	}()

	version.GitCommit = "abc1234"
	version.GitTreeState = "dirty"
	version.BuildDate = "2026-01-01T00:00:00Z"

	cfg := &Config{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
	}

	attrs := buildBaseAttrs(cfg)

	assert.Contains(t, attrs, attribute.String("onix.build.commit", "abc1234"))
	assert.Contains(t, attrs, attribute.String("onix.build.tree_state", "dirty"))
	assert.Contains(t, attrs, attribute.String("onix.build.date", "2026-01-01T00:00:00Z"))
	assert.Contains(t, attrs, attribute.String("service.name", "test-service"))
	assert.Contains(t, attrs, attribute.String("service.version", "1.0.0"))
}

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

// TestSetup_New_TracingEnabled tests New with only tracing enabled.
func TestSetup_New_TracingEnabled(t *testing.T) {
	setup := Setup{}
	ctx := context.Background()

	cfg := &Config{
		ServiceName:    "trace-service",
		ServiceVersion: "1.0.0",
		EnableMetrics:  false,
		EnableTracing:  true,
		EnableLogs:     false,
		OtlpEndpoint:   "localhost:4317",
		TimeInterval:   5,
	}

	provider, err := setup.New(ctx, cfg)
	require.NoError(t, err, "New() should succeed with tracing enabled")
	require.NotNil(t, provider, "New() should return a non-nil provider")
	assert.NotNil(t, provider.TraceProvider, "TraceProvider should be set when tracing is enabled")
	assert.Nil(t, provider.MeterProvider, "MeterProvider should be nil when metrics are disabled")
	assert.Nil(t, provider.LogProvider, "LogProvider should be nil when logs are disabled")

	_ = provider.Shutdown(ctx)
}

// TestSetup_New_LogsEnabled tests New with only logs enabled.
func TestSetup_New_LogsEnabled(t *testing.T) {
	setup := Setup{}
	ctx := context.Background()

	cfg := &Config{
		ServiceName:    "log-service",
		ServiceVersion: "1.0.0",
		EnableMetrics:  false,
		EnableTracing:  false,
		EnableLogs:     true,
		OtlpEndpoint:   "localhost:4317",
		TimeInterval:   5,
	}

	provider, err := setup.New(ctx, cfg)
	require.NoError(t, err, "New() should succeed with logs enabled")
	require.NotNil(t, provider, "New() should return a non-nil provider")
	assert.NotNil(t, provider.LogProvider, "LogProvider should be set when logs are enabled")
	assert.Nil(t, provider.MeterProvider, "MeterProvider should be nil when metrics are disabled")
	assert.Nil(t, provider.TraceProvider, "TraceProvider should be nil when tracing is disabled")

	_ = provider.Shutdown(ctx)
}

// TestSetup_New_NilConfig tests that New returns an error for a nil config.
func TestSetup_New_NilConfig(t *testing.T) {
	setup := Setup{}
	_, err := setup.New(context.Background(), nil)
	assert.ErrorContains(t, err, "telemetry config cannot be nil")
}

// TestSetup_New_AllEnabled tests New with all signals enabled and calls shutdown.
func TestSetup_New_AllEnabled(t *testing.T) {
	setup := Setup{}
	ctx := context.Background()

	cfg := &Config{
		ServiceName:    "full-service",
		ServiceVersion: "1.0.0",
		EnableMetrics:  true,
		EnableTracing:  true,
		EnableLogs:     true,
		OtlpEndpoint:   "localhost:4317",
		TimeInterval:   5,
	}

	provider, err := setup.New(ctx, cfg)
	require.NoError(t, err, "New() should succeed with all signals enabled")
	require.NotNil(t, provider, "New() should return a non-nil provider")
	assert.NotNil(t, provider.MeterProvider, "MeterProvider should be set")
	assert.NotNil(t, provider.TraceProvider, "TraceProvider should be set")
	assert.NotNil(t, provider.LogProvider, "LogProvider should be set")

	// Shutdown exercises the full provider shutdown path.
	_ = provider.Shutdown(ctx)
}
