package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ConstantsOverride describes a single beckn constant that a node is running
// with a non-canonical value.
type ConstantsOverride struct {
	PluginID  string // e.g. "schemav2validator"
	Key       string // e.g. "location"
	Canonical string // value defined in beckn-constants.yaml
	Actual    string // value the node is actually using
}

// RegisterBecknConstantsInfo registers a beckn_constants_info observable gauge
// that reports every beckn constant currently running with a non-canonical value.
// Each override becomes a separate time series with value 1 and labels
// {plugin_id, key, canonical, actual}. No-ops when overrides is empty.
// Call once after all modules are initialised.
func RegisterBecknConstantsInfo(_ context.Context, overrides []ConstantsOverride) error {
	if len(overrides) == 0 {
		return nil
	}
	meter := otel.GetMeterProvider().Meter(ScopeName, metric.WithInstrumentationVersion(ScopeVersion))
	gauge, err := meter.Int64ObservableGauge(
		"beckn_constants_info",
		metric.WithDescription("Beckn constants running with non-canonical values; value is always 1"),
		metric.WithUnit("{override}"),
	)
	if err != nil {
		return err
	}
	snapshot := make([]ConstantsOverride, len(overrides))
	copy(snapshot, overrides)
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		for _, ov := range snapshot {
			o.ObserveInt64(gauge, 1, metric.WithAttributes(
				AttrPluginID.String(ov.PluginID),
				attribute.String("key", ov.Key),
				attribute.String("canonical", ov.Canonical),
				attribute.String("actual", ov.Actual),
			))
		}
		return nil
	}, gauge)
	return err
}

// PluginEntry describes a single loaded plugin within a module.
type PluginEntry struct {
	Type string // e.g. "router", "registry", "step", "middleware"
	ID   string // plugin implementation ID as configured
}

// RegisterPluginInfo registers an observable gauge reporting all loaded plugins
// for a module. Each plugin appears as a separate time series with value 1 and
// labels {module, subscriber_id, plugin_type, plugin_id}. Safe to call once per
// module after all plugins are initialized; no-ops when entries is empty.
func RegisterPluginInfo(_ context.Context, moduleName, subscriberID string, entries []PluginEntry) error {
	if len(entries) == 0 {
		return nil
	}
	meter := otel.GetMeterProvider().Meter(ScopeName, metric.WithInstrumentationVersion(ScopeVersion))
	gauge, err := meter.Int64ObservableGauge(
		"onix_plugin_info",
		metric.WithDescription("Loaded plugins per ONIX module; value is always 1"),
		metric.WithUnit("{plugin}"),
	)
	if err != nil {
		return err
	}
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		for _, e := range entries {
			attrs := []attribute.KeyValue{
				AttrModule.String(moduleName),
				AttrPluginType.String(e.Type),
				AttrPluginID.String(e.ID),
			}
			if subscriberID != "" {
				attrs = append(attrs, attribute.String("subscriber_id", subscriberID))
			}
			o.ObserveInt64(gauge, 1, metric.WithAttributes(attrs...))
		}
		return nil
	}, gauge)
	return err
}
