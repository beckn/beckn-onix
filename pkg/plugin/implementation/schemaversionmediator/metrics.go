package schemaversionmediator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Mediation outcomes recorded as the "outcome" label on
// onix_schema_mediation_outcomes_total. Mediate records exactly one per call,
// so the sum across outcomes is the call count and each outcome is a rate.
const (
	// outcomeTranslationApplied marks a payload that was translated and patched.
	outcomeTranslationApplied = "translation_applied"
	// outcomeSkippedCompatible marks a payload whose schema objects were all at
	// a version the target manifest supports.
	outcomeSkippedCompatible = "translation_skipped_compatible"
	// outcomeSkippedNoManifest marks a target manifest that was unavailable,
	// leaving no translation target.
	outcomeSkippedNoManifest = "translation_skipped_no_manifest"
	// outcomeDataLossDetected marks a rejection for fields dropped by translation.
	outcomeDataLossDetected = "data_loss_detected"
	// outcomeArtifactFetchFailure marks an artifact that could not be fetched,
	// either unpublished or unreachable.
	outcomeArtifactFetchFailure = "artifact_fetch_failure"
	// outcomeRejectedByPolicy marks incompatible objects rejected under action=reject.
	outcomeRejectedByPolicy = "translation_rejected_by_policy"
	// outcomeTranslationFailed marks artifacts that were fetched but could not
	// be applied.
	outcomeTranslationFailed = "translation_failed"
	// outcomeNotOnboarded marks a rejection by the cold-start guard. Applies to
	// every request until the local node manifest is published.
	outcomeNotOnboarded = "rejected_not_onboarded"
	// outcomeSkippedNoCounterparty marks a payload forwarded untranslated for
	// want of a counterparty subscriber ID, usually a missing reqpreprocessor
	// middleware. Applies to every request until it is configured.
	outcomeSkippedNoCounterparty = "skipped_no_counterparty"
)

// Caches identified by the "cache" label on the hit and miss counters.
const (
	// cacheArtifact is the translation artifact cache; a hit avoids an HTTP fetch.
	cacheArtifact = "artifact"
	// cacheExpression is the compiled JSONata cache; a hit avoids a compile.
	cacheExpression = "expression"
)

// unknownLabel stands in for an identity absent from the request, keeping the
// data point on a stable series.
const unknownLabel = "unknown"

// Attribute keys owned by this plugin. Keys shared with other instruments come
// from pkg/telemetry.
var (
	attrOutcome        = attribute.Key("outcome")
	attrCounterpartyID = attribute.Key("counterparty_id")
	attrNetworkID      = attribute.Key("network_id")
	attrCache          = attribute.Key("cache")
)

// MediatorMetrics exposes schema version mediation metric instruments.
type MediatorMetrics struct {
	MediationOutcomesTotal metric.Int64Counter
	CacheHitsTotal         metric.Int64Counter
	CacheMissesTotal       metric.Int64Counter
}

// mediatorMetricsCache caches MediatorMetrics for the current global MeterProvider.
// Instruments are rebound only when otel.SetMeterProvider changes the provider pointer.
var mediatorMetricsCache struct {
	mu       sync.RWMutex
	provider metric.MeterProvider
	m        *MediatorMetrics
}

// GetMediatorMetrics returns MediatorMetrics bound to the current global
// MeterProvider, rebuilding only when the provider has been replaced since the
// last call.
func GetMediatorMetrics(_ context.Context) (*MediatorMetrics, error) {
	current := otel.GetMeterProvider()

	mediatorMetricsCache.mu.RLock()
	if mediatorMetricsCache.provider == current && mediatorMetricsCache.m != nil {
		m := mediatorMetricsCache.m
		mediatorMetricsCache.mu.RUnlock()
		return m, nil
	}
	mediatorMetricsCache.mu.RUnlock()

	mediatorMetricsCache.mu.Lock()
	defer mediatorMetricsCache.mu.Unlock()
	// Double-check after acquiring the write lock.
	if mediatorMetricsCache.provider == current && mediatorMetricsCache.m != nil {
		return mediatorMetricsCache.m, nil
	}
	m, err := newMediatorMetrics()
	if err != nil {
		return nil, err
	}
	mediatorMetricsCache.provider = current
	mediatorMetricsCache.m = m
	return m, nil
}

func newMediatorMetrics() (*MediatorMetrics, error) {
	meter := otel.GetMeterProvider().Meter(
		"github.com/beckn-one/beckn-onix/schemaversionmediator",
		metric.WithInstrumentationVersion("1.0.0"),
	)

	m := &MediatorMetrics{}
	var err error

	if m.MediationOutcomesTotal, err = meter.Int64Counter(
		"onix_schema_mediation_outcomes_total",
		metric.WithDescription("Schema version mediation decisions by outcome"),
		metric.WithUnit("{mediation}"),
	); err != nil {
		return nil, fmt.Errorf("onix_schema_mediation_outcomes_total: %w", err)
	}

	if m.CacheHitsTotal, err = meter.Int64Counter(
		"onix_schema_mediation_cache_hits_total",
		metric.WithDescription("Mediator cache lookups served from memory"),
		metric.WithUnit("{hit}"),
	); err != nil {
		return nil, fmt.Errorf("onix_schema_mediation_cache_hits_total: %w", err)
	}

	if m.CacheMissesTotal, err = meter.Int64Counter(
		"onix_schema_mediation_cache_misses_total",
		metric.WithDescription("Mediator cache lookups that missed"),
		metric.WithUnit("{miss}"),
	); err != nil {
		return nil, fmt.Errorf("onix_schema_mediation_cache_misses_total: %w", err)
	}

	return m, nil
}

// recordOutcome records one mediation outcome. An empty outcome or an
// unregistered meter makes it a no-op.
func (m *mediator) recordOutcome(ctx *model.StepContext, outcome string) {
	if m.metrics == nil || outcome == "" {
		return
	}
	m.metrics.MediationOutcomesTotal.Add(ctx, 1,
		metric.WithAttributes(append(mediationAttrs(ctx), attrOutcome.String(outcome))...))
}

// recordCacheLookup records one lookup against the named cache. The counters
// carry only the cache identity; the per-counterparty view is on the outcome
// counter.
func (m *mediator) recordCacheLookup(ctx context.Context, cache string, hit bool) {
	if m.metrics == nil {
		return
	}
	counter := m.metrics.CacheMissesTotal
	if hit {
		counter = m.metrics.CacheHitsTotal
	}
	counter.Add(ctx, 1, metric.WithAttributes(attrCache.String(cache)))
}

// resolveOutcome returns the outcome to record for a completed Mediate call.
// branch is what the terminating branch set. A rejection carrying dropped field
// paths is data loss wherever it was raised, so the counter follows
// MediationError.DroppedFields rather than a single call site.
func resolveOutcome(branch string, err error) string {
	var mediationErr *MediationError
	if errors.As(err, &mediationErr) && len(mediationErr.DroppedFields) > 0 {
		return outcomeDataLossDetected
	}
	return branch
}

// mediationAttrs builds the labels shared by every outcome: the beckn action,
// the counterparty subscriber ID and the network ID.
func mediationAttrs(ctx *model.StepContext) []attribute.KeyValue {
	counterpartyID, _ := ctx.Value(model.ContextKeyRemoteID).(string)
	networkID, _ := ctx.Value(model.ContextKeyNetworkID).(string)

	becknContext := payloadContext(ctx.Body)
	if networkID == "" {
		networkID = model.ResolveNetworkID(becknContext)
	}
	action, _ := becknContext["action"].(string)

	return []attribute.KeyValue{
		telemetry.AttrAction.String(orUnknown(action)),
		attrCounterpartyID.String(orUnknown(counterpartyID)),
		attrNetworkID.String(orUnknown(networkID)),
	}
}

// payloadContext returns the top-level "context" block of a Beckn payload, or
// nil when the body is not a JSON object carrying one.
func payloadContext(body []byte) map[string]any {
	var envelope struct {
		Context map[string]any `json:"context"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	return envelope.Context
}

// orUnknown substitutes a stable placeholder for an absent label value.
func orUnknown(v string) string {
	if v == "" {
		return unknownLabel
	}
	return v
}
