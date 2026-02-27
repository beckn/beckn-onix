package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProviderAndMetrics(t *testing.T) {
	ctx := context.Background()
	provider, err := NewTestProvider(ctx)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.NotNil(t, provider.MeterProvider, "MeterProvider should be set")

	metrics, err := GetMetrics(ctx)
	require.NoError(t, err)
	require.NotNil(t, metrics)

	require.NoError(t, provider.Shutdown(ctx))
}

func TestNewProviderAndTraces(t *testing.T) {
	ctx := context.Background()
	provider, sr, err := NewTestProviderWithTrace(ctx)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.NotNil(t, provider.MeterProvider, "MeterProvider should be set")
	require.NotNil(t, provider.TraceProvider, "TraceProvider should be set")
	require.NotNil(t, sr, "SpanRecorder should be set")

	tracer := provider.TraceProvider.Tracer("test-instrumentation")
	_, span := tracer.Start(ctx, "test-span")
	span.End()

	ended := sr.Ended()
	require.Len(t, ended, 1, "exactly one span should be recorded")
	assert.Equal(t, "test-span", ended[0].Name(), "recorded span should have expected name")

	require.NoError(t, provider.Shutdown(ctx))
}
