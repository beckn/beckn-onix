package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRegisterPluginInfo_EmptyEntries(t *testing.T) {
	err := RegisterPluginInfo(context.Background(), "mod", "sub", nil)
	assert.NoError(t, err, "empty entries should be a no-op")
}

func TestRegisterPluginInfo_AttributesWithSubscriberID(t *testing.T) {
	ctx := context.Background()
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { mp.Shutdown(ctx) })

	entries := []PluginEntry{
		{Type: "router", ID: "static_router"},
		{Type: "signer", ID: "ed25519_signer"},
	}
	require.NoError(t, RegisterPluginInfo(ctx, "search", "bap.example.com", entries))

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))
	require.Len(t, rm.ScopeMetrics, 1)
	require.Len(t, rm.ScopeMetrics[0].Metrics, 1)

	gauge := rm.ScopeMetrics[0].Metrics[0]
	assert.Equal(t, "onix_plugin_info", gauge.Name)

	data, ok := gauge.Data.(metricdata.Gauge[int64])
	require.True(t, ok, "metric data should be Gauge[int64]")
	assert.Len(t, data.DataPoints, 2)

	for _, dp := range data.DataPoints {
		attrs := dp.Attributes.ToSlice()
		attrMap := make(map[string]string, len(attrs))
		for _, a := range attrs {
			attrMap[string(a.Key)] = a.Value.AsString()
		}
		assert.Equal(t, "search", attrMap["module"])
		assert.Equal(t, "bap.example.com", attrMap["subscriber_id"])
		assert.Contains(t, []string{"router", "signer"}, attrMap["plugin_type"])
		assert.NotEmpty(t, attrMap["plugin_id"])
	}
}

func TestRegisterPluginInfo_NoSubscriberID(t *testing.T) {
	ctx := context.Background()
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { mp.Shutdown(ctx) })

	entries := []PluginEntry{{Type: "router", ID: "static_router"}}
	require.NoError(t, RegisterPluginInfo(ctx, "search", "", entries))

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))

	data, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[int64])
	require.True(t, ok)
	require.Len(t, data.DataPoints, 1)

	for _, attr := range data.DataPoints[0].Attributes.ToSlice() {
		assert.NotEqual(t, "subscriber_id", string(attr.Key),
			"subscriber_id attribute must be absent when subscriberID is empty")
	}
}
