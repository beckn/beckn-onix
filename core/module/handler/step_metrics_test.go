package handler

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/metric"

	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetStepMetrics_Success(t *testing.T) {
	ctx := context.Background()

	// Initialize telemetry provider first
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	// Test getting step metrics
	metrics, err := GetStepMetrics(ctx)
	require.NoError(t, err, "GetStepMetrics() should not return error")
	require.NotNil(t, metrics, "GetStepMetrics() should return non-nil metrics")

	// Verify all metric instruments are initialized
	assert.NotNil(t, metrics.StepExecutionDuration, "StepExecutionDuration should be initialized")
	assert.NotNil(t, metrics.StepExecutionTotal, "StepExecutionTotal should be initialized")
	assert.NotNil(t, metrics.StepErrorsTotal, "StepErrorsTotal should be initialized")
}

func TestGetStepMetrics_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()

	// Initialize telemetry provider first
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	// Since sync.Once was removed, each call returns a fresh *StepMetrics wrapping
	// the same underlying OTel instruments (the SDK deduplicates by name).
	// Verify both calls succeed and return fully-initialized instruments.
	metrics1, err1 := GetStepMetrics(ctx)
	require.NoError(t, err1)
	require.NotNil(t, metrics1)
	assert.NotNil(t, metrics1.StepExecutionDuration)
	assert.NotNil(t, metrics1.StepExecutionTotal)
	assert.NotNil(t, metrics1.StepErrorsTotal)

	metrics2, err2 := GetStepMetrics(ctx)
	require.NoError(t, err2)
	require.NotNil(t, metrics2)
	assert.NotNil(t, metrics2.StepExecutionDuration)
	assert.NotNil(t, metrics2.StepExecutionTotal)
	assert.NotNil(t, metrics2.StepErrorsTotal)

	// Verify concurrent access is safe — 20 goroutines each calling GetStepMetrics
	// while simultaneously recording on the returned instruments.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := GetStepMetrics(ctx)
			require.NoError(t, err)
			require.NotNil(t, m)
			m.StepExecutionTotal.Add(ctx, 1,
				metric.WithAttributes(telemetry.AttrStep.String("concurrent"), telemetry.AttrModule.String("test")))
		}()
	}
	wg.Wait()
}

func TestGetStepMetrics_WithoutProvider(t *testing.T) {
	ctx := context.Background()

	// Test getting step metrics without initializing provider
	// This should still work but may not have a valid meter provider
	metrics, err := GetStepMetrics(ctx)
	// Note: This might succeed or fail depending on OTel's default behavior
	// We're just checking it doesn't panic
	if err != nil {
		t.Logf("GetStepMetrics returned error (expected if no provider): %v", err)
	} else {
		assert.NotNil(t, metrics, "Metrics should be returned even without explicit provider")
	}
}

func TestStepMetrics_Instruments(t *testing.T) {
	ctx := context.Background()

	// Initialize telemetry provider
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	// Get step metrics
	metrics, err := GetStepMetrics(ctx)
	require.NoError(t, err)
	require.NotNil(t, metrics)

	// Test that we can record metrics (this tests the instruments are functional)
	// Note: We can't easily verify the metrics were recorded without querying the exporter,
	// but we can verify the instruments are not nil and can be called without panicking

	// Test StepExecutionDuration
	require.NotPanics(t, func() {
		metrics.StepExecutionDuration.Record(ctx, 0.5,
			metric.WithAttributes(telemetry.AttrStep.String("test-step"), telemetry.AttrModule.String("test-module")))
	}, "StepExecutionDuration.Record should not panic")

	// Test StepExecutionTotal
	require.NotPanics(t, func() {
		metrics.StepExecutionTotal.Add(ctx, 1,
			metric.WithAttributes(telemetry.AttrStep.String("test-step"), telemetry.AttrModule.String("test-module")))
	}, "StepExecutionTotal.Add should not panic")

	// Test StepErrorsTotal
	require.NotPanics(t, func() {
		metrics.StepErrorsTotal.Add(ctx, 1,
			metric.WithAttributes(telemetry.AttrStep.String("test-step"), telemetry.AttrModule.String("test-module")))
	}, "StepErrorsTotal.Add should not panic")

	// MeterProvider is set by NewTestProvider; metrics are recorded via OTel SDK
	assert.NotNil(t, provider.MeterProvider, "MeterProvider should be set")
}

func TestStepMetrics_MultipleCalls(t *testing.T) {
	ctx := context.Background()

	// Initialize telemetry provider
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	// Call GetStepMetrics multiple times
	for i := 0; i < 10; i++ {
		metrics, err := GetStepMetrics(ctx)
		require.NoError(t, err, "GetStepMetrics should succeed on call %d", i)
		require.NotNil(t, metrics, "GetStepMetrics should return non-nil on call %d", i)
		assert.NotNil(t, metrics.StepExecutionDuration, "StepExecutionDuration should be initialized")
		assert.NotNil(t, metrics.StepExecutionTotal, "StepExecutionTotal should be initialized")
		assert.NotNil(t, metrics.StepErrorsTotal, "StepErrorsTotal should be initialized")
	}
}

func TestStepMetrics_RecordWithDifferentAttributes(t *testing.T) {
	ctx := context.Background()
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	metrics, err := GetStepMetrics(ctx)
	require.NoError(t, err)
	require.NotNil(t, metrics)

	attrsList := []struct {
		step   string
		module string
	}{
		{"test-step", "test-module"},
		{"", "module-only"},
		{"step-only", ""},
		{"", ""},
		{"long-step-name-with-many-parts", "long-module-name"},
	}

	for _, a := range attrsList {
		attrs := metric.WithAttributes(
			telemetry.AttrStep.String(a.step),
			telemetry.AttrModule.String(a.module),
		)
		require.NotPanics(t, func() {
			metrics.StepExecutionDuration.Record(ctx, 0.01, attrs)
			metrics.StepExecutionTotal.Add(ctx, 1, attrs)
			metrics.StepErrorsTotal.Add(ctx, 0, attrs)
		}, "Recording with step=%q module=%q should not panic", a.step, a.module)
	}
}

func TestStepMetrics_DurationValues(t *testing.T) {
	ctx := context.Background()
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	metrics, err := GetStepMetrics(ctx)
	require.NoError(t, err)
	require.NotNil(t, metrics)

	attrs := metric.WithAttributes(
		telemetry.AttrStep.String("test-step"),
		telemetry.AttrModule.String("test-module"),
	)

	durations := []float64{0, 0.0005, 0.001, 0.01, 0.1, 0.5}
	for _, d := range durations {
		d := d
		require.NotPanics(t, func() {
			metrics.StepExecutionDuration.Record(ctx, d, attrs)
		}, "StepExecutionDuration.Record(%.4f) should not panic", d)
	}
}

func TestStepMetrics_ConcurrentRecord(t *testing.T) {
	ctx := context.Background()
	provider, err := telemetry.NewTestProvider(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(context.Background())

	metrics, err := GetStepMetrics(ctx)
	require.NoError(t, err)
	require.NotNil(t, metrics)

	attrs := metric.WithAttributes(
		telemetry.AttrStep.String("concurrent-step"),
		telemetry.AttrModule.String("concurrent-module"),
	)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			metrics.StepExecutionDuration.Record(ctx, 0.05, attrs)
			metrics.StepExecutionTotal.Add(ctx, 1, attrs)
			metrics.StepErrorsTotal.Add(ctx, 0, attrs)
		}()
	}
	wg.Wait()
}

func TestStepMetrics_WithTraceProvider(t *testing.T) {
	ctx := context.Background()
	provider, sr, err := telemetry.NewTestProviderWithTrace(ctx)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.NotNil(t, sr)
	defer provider.Shutdown(ctx)

	metrics, err := GetStepMetrics(ctx)
	require.NoError(t, err)
	require.NotNil(t, metrics)
	assert.NotNil(t, provider.MeterProvider, "MeterProvider should be set")
	assert.NotNil(t, provider.TraceProvider, "TraceProvider should be set")

	attrs := metric.WithAttributes(
		telemetry.AttrStep.String("trace-test-step"),
		telemetry.AttrModule.String("trace-test-module"),
	)
	require.NotPanics(t, func() {
		metrics.StepExecutionDuration.Record(ctx, 0.1, attrs)
		metrics.StepExecutionTotal.Add(ctx, 1, attrs)
		metrics.StepErrorsTotal.Add(ctx, 0, attrs)
	}, "Step metrics should work when trace provider is also set")
}
