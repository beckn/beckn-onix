package schemaversionmediator

import (
	"context"
	"errors"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const (
	outcomesMetric    = "onix_schema_mediation_outcomes_total"
	cacheHitsMetric   = "onix_schema_mediation_cache_hits_total"
	cacheMissesMetric = "onix_schema_mediation_cache_misses_total"
)

// newMetricReader installs an in-memory MeterProvider as the global one and
// returns its reader. The previous provider is restored on cleanup. Installing
// a provider also invalidates mediatorMetricsCache, so each test binds its own
// instruments.
func newMetricReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	previous := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown meter provider: %v", err)
		}
		otel.SetMeterProvider(previous)
	})
	return reader
}

// counterValue sums the data points of the named counter whose attributes
// include every wanted key/value pair. An absent metric counts as zero.
func counterValue(t *testing.T, reader sdkmetric.Reader, name string, want ...attribute.KeyValue) int64 {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	var total int64
	for _, scope := range collected.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != name {
				continue
			}
			sum, ok := recorded.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %q holds %T, want metricdata.Sum[int64]", name, recorded.Data)
			}
			for _, point := range sum.DataPoints {
				if hasAttributes(point.Attributes, want) {
					total += point.Value
				}
			}
		}
	}
	return total
}

// hasAttributes reports whether set carries every wanted key with the same value.
func hasAttributes(set attribute.Set, want []attribute.KeyValue) bool {
	for _, kv := range want {
		got, ok := set.Value(kv.Key)
		if !ok || got.Emit() != kv.Value.Emit() {
			return false
		}
	}
	return true
}

// outcomeIs is shorthand for the outcome label filter used by most assertions.
func outcomeIs(outcome string) attribute.KeyValue {
	return attrOutcome.String(outcome)
}

// newInstrumentedMediator builds a test mediator bound to the current global
// meter provider, as New does at plugin load time.
func newInstrumentedMediator(t *testing.T, loader *mockManifestLoader, cfg map[string]string, localManifest *model.NodeManifest) *mediator {
	t.Helper()
	m := newTestMediatorFull(t, loader, cfg, localManifest)
	metrics, err := GetMediatorMetrics(context.Background())
	if err != nil {
		t.Fatalf("GetMediatorMetrics: %v", err)
	}
	m.metrics = metrics
	return m
}

// mediationPayload builds a Beckn body carrying an action, a network ID and a
// single message-level schema object declaration.
func mediationPayload(action, networkID, contextURL string) []byte {
	return []byte(`{"context":{"action":"` + action + `","network_id":"` + networkID +
		`"},"message":{"@context":"` + contextURL + `","@type":"Order","status":"ACTIVE"}}`)
}

// --- instrument registration ---

func TestGetMediatorMetrics_ReturnsCachedMetrics(t *testing.T) {
	newMetricReader(t)
	ctx := context.Background()

	first, err := GetMediatorMetrics(ctx)
	if err != nil {
		t.Fatalf("GetMediatorMetrics: %v", err)
	}
	second, err := GetMediatorMetrics(ctx)
	if err != nil {
		t.Fatalf("GetMediatorMetrics: %v", err)
	}
	if first != second {
		t.Error("expected the cached metrics instance on the second call")
	}
}

func TestGetMediatorMetrics_RebuildsOnProviderChange(t *testing.T) {
	newMetricReader(t)
	first, err := GetMediatorMetrics(context.Background())
	if err != nil {
		t.Fatalf("GetMediatorMetrics: %v", err)
	}

	newMetricReader(t) // installs a different provider
	second, err := GetMediatorMetrics(context.Background())
	if err != nil {
		t.Fatalf("GetMediatorMetrics: %v", err)
	}
	if first == second {
		t.Error("expected instruments to be rebuilt after the meter provider changed")
	}
}

func TestNew_RegistersMeter(t *testing.T) {
	newMetricReader(t)
	// nodeId is absent, so the mediator is notOnboarded but must still register.
	instance, _, err := New(context.Background(), &mockManifestLoader{}, map[string]string{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m, ok := instance.(*mediator)
	if !ok {
		t.Fatalf("New returned %T, want *mediator", instance)
	}
	if m.metrics == nil {
		t.Fatal("expected New to register the mediation instruments")
	}
}

// --- outcome counters, one per mediation branch ---

func TestMediate_RecordsTranslationApplied(t *testing.T) {
	reader := newMetricReader(t)
	srv := artifactServer(t, map[string]string{
		"/retail/v1.0/Order_from_v2.0.jsonata": `{"state": status}`,
	})
	defer srv.Close()

	lm := localManifestWith(model.SchemaObject{
		BaseURL:           srv.URL + "/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{}, lm)
	m.httpClient = srv.Client()

	body := mediationPayload("search", "net1", srv.URL+"/retail/v2.0/Order.jsonld")
	if err := m.Mediate(stepCtxWithRemoteID(body, "bap.example.com")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assert the full label set, not just the outcome.
	got := counterValue(t, reader, outcomesMetric,
		outcomeIs(outcomeTranslationApplied),
		attribute.String("action", "search"),
		attrCounterpartyID.String("bap.example.com"),
		attrNetworkID.String("net1"),
	)
	if got != 1 {
		t.Errorf("translation_applied count = %d, want 1", got)
	}
}

func TestMediate_RecordsSkippedCompatible(t *testing.T) {
	reader := newMetricReader(t)
	lm := localManifestWith(model.SchemaObject{
		BaseURL:           "https://schema.beckn.io/retail",
		Type:              "Order",
		SupportedVersions: []string{"v2.0"},
	})
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{}, lm)

	body := mediationPayload("select", "net1", "https://schema.beckn.io/retail/v2.0/Order.jsonld")
	if err := m.Mediate(stepCtxWithRemoteID(body, "bap.example.com")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := counterValue(t, reader, outcomesMetric, outcomeIs(outcomeSkippedCompatible)); got != 1 {
		t.Errorf("translation_skipped_compatible count = %d, want 1", got)
	}
	if got := counterValue(t, reader, outcomesMetric, outcomeIs(outcomeTranslationApplied)); got != 0 {
		t.Errorf("translation_applied count = %d, want 0", got)
	}
}

func TestMediate_RecordsSkippedNoManifest_CounterpartyUnavailable(t *testing.T) {
	reader := newMetricReader(t)
	loader := &mockManifestLoader{
		bySubscriberID: func(context.Context, string) (*model.ManifestDocument, error) {
			return nil, errors.New("dedi lookup failed")
		},
	}
	m := newInstrumentedMediator(t, loader, map[string]string{"onFailure": "passThrough"}, nil)

	body := mediationPayload("confirm", "net1", "https://schema.beckn.io/retail/v2.0/Order.jsonld")
	if err := m.Mediate(callerStepCtxWithRemoteID(body, "bpp.example.com")); err != nil {
		t.Fatalf("expected pass-through, got: %v", err)
	}

	if got := counterValue(t, reader, outcomesMetric, outcomeIs(outcomeSkippedNoManifest)); got != 1 {
		t.Errorf("translation_skipped_no_manifest count = %d, want 1", got)
	}
}

func TestMediate_RecordsSkippedNoManifest_LocalManifestMissing(t *testing.T) {
	reader := newMetricReader(t)
	// Receiver path with no local manifest: CheckCompatibility reports ErrNoManifest.
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{}, nil)

	body := mediationPayload("search", "net1", "https://schema.beckn.io/retail/v2.0/Order.jsonld")
	if err := m.Mediate(stepCtxWithRemoteID(body, "bap.example.com")); !errors.Is(err, ErrNoManifest) {
		t.Fatalf("expected ErrNoManifest, got: %v", err)
	}

	if got := counterValue(t, reader, outcomesMetric, outcomeIs(outcomeSkippedNoManifest)); got != 1 {
		t.Errorf("translation_skipped_no_manifest count = %d, want 1", got)
	}
}

func TestMediate_RecordsArtifactFetchFailure(t *testing.T) {
	reader := newMetricReader(t)
	// Every artifact request 404s.
	srv := artifactServer(t, nil)
	defer srv.Close()

	lm := localManifestWith(model.SchemaObject{
		BaseURL:           srv.URL + "/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{"onFailure": "reject"}, lm)
	m.httpClient = srv.Client()

	body := mediationPayload("search", "net1", srv.URL+"/retail/v2.0/Order.jsonld")
	if err := m.Mediate(stepCtxWithRemoteID(body, "bap.example.com")); err == nil {
		t.Fatal("expected a rejection when the artifact cannot be fetched")
	}

	if got := counterValue(t, reader, outcomesMetric, outcomeIs(outcomeArtifactFetchFailure)); got != 1 {
		t.Errorf("artifact_fetch_failure count = %d, want 1", got)
	}
}

func TestMediate_RecordsRejectedByPolicy(t *testing.T) {
	reader := newMetricReader(t)
	lm := localManifestWith(model.SchemaObject{
		BaseURL:           "https://schema.beckn.io/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{"action": "reject"}, lm)

	body := mediationPayload("search", "net1", "https://schema.beckn.io/retail/v2.0/Order.jsonld")
	if err := m.Mediate(stepCtxWithRemoteID(body, "bap.example.com")); err == nil {
		t.Fatal("expected a rejection when action=reject")
	}

	if got := counterValue(t, reader, outcomesMetric, outcomeIs(outcomeRejectedByPolicy)); got != 1 {
		t.Errorf("translation_rejected_by_policy count = %d, want 1", got)
	}
}

func TestMediate_RecordsTranslationFailed(t *testing.T) {
	reader := newMetricReader(t)
	// The artifact is served but is not a compilable JSONata expression.
	srv := artifactServer(t, map[string]string{
		"/retail/v1.0/Order_from_v2.0.jsonata": `!!!invalid jsonata{{`,
	})
	defer srv.Close()

	lm := localManifestWith(model.SchemaObject{
		BaseURL:           srv.URL + "/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{}, lm)
	m.httpClient = srv.Client()

	body := mediationPayload("search", "net1", srv.URL+"/retail/v2.0/Order.jsonld")
	if err := m.Mediate(stepCtxWithRemoteID(body, "bap.example.com")); err == nil {
		t.Fatal("expected an error when the artifact expression cannot be compiled")
	}

	if got := counterValue(t, reader, outcomesMetric, outcomeIs(outcomeTranslationFailed)); got != 1 {
		t.Errorf("translation_failed count = %d, want 1", got)
	}
}

// The cold-start guard rejects every request until the local manifest is
// published, so it is counted rather than left to the logs.
func TestMediate_RecordsNotOnboarded(t *testing.T) {
	reader := newMetricReader(t)
	metrics, err := GetMediatorMetrics(context.Background())
	if err != nil {
		t.Fatalf("GetMediatorMetrics: %v", err)
	}
	body := mediationPayload("search", "net1", "https://schema.beckn.io/retail/v2.0/Order.jsonld")

	m := &mediator{notOnboarded: true, metrics: metrics}
	if err := m.Mediate(stepCtxWithRemoteID(body, "bap.example.com")); err == nil {
		t.Fatal("expected SCH_SUBSCRIBER_NOT_FOUND")
	}

	if got := counterValue(t, reader, outcomesMetric, outcomeIs(outcomeNotOnboarded)); got != 1 {
		t.Errorf("rejected_not_onboarded count = %d, want 1", got)
	}
}

// Without reqpreprocessor every request is forwarded untranslated, which is
// invisible on the wire.
func TestMediate_RecordsSkippedNoCounterparty(t *testing.T) {
	reader := newMetricReader(t)
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{}, nil)
	body := mediationPayload("search", "net1", "https://schema.beckn.io/retail/v2.0/Order.jsonld")

	if err := m.Mediate(&model.StepContext{Context: context.Background(), Body: body}); err != nil {
		t.Fatalf("expected pass-through, got: %v", err)
	}

	if got := counterValue(t, reader, outcomesMetric, outcomeIs(outcomeSkippedNoCounterparty)); got != 1 {
		t.Errorf("skipped_no_counterparty count = %d, want 1", got)
	}
	// The missing identity must degrade to a label, not drop the data point.
	if got := counterValue(t, reader, outcomesMetric,
		outcomeIs(outcomeSkippedNoCounterparty),
		attrCounterpartyID.String(unknownLabel),
	); got != 1 {
		t.Errorf("skipped_no_counterparty with unknown counterparty = %d, want 1", got)
	}
}

// sum(outcomes) must equal the number of Mediate calls; the counter is
// documented as a rate on that basis.
func TestMediate_RecordsExactlyOneOutcomePerCall(t *testing.T) {
	reader := newMetricReader(t)
	srv := artifactServer(t, map[string]string{
		"/retail/v1.0/Order_from_v2.0.jsonata": `{"state": status}`,
	})
	defer srv.Close()

	lm := localManifestWith(model.SchemaObject{
		BaseURL:           srv.URL + "/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{}, lm)
	m.httpClient = srv.Client()

	calls := []*model.StepContext{
		// translated
		stepCtxWithRemoteID(mediationPayload("search", "net1", srv.URL+"/retail/v2.0/Order.jsonld"), "bap.example.com"),
		// already compatible
		stepCtxWithRemoteID(mediationPayload("search", "net1", srv.URL+"/retail/v1.0/Order.jsonld"), "bap.example.com"),
		// no counterparty
		{Context: context.Background(), Body: mediationPayload("search", "net1", srv.URL+"/retail/v2.0/Order.jsonld")},
	}
	for i, call := range calls {
		if err := m.Mediate(call); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}

	if got := counterValue(t, reader, outcomesMetric); got != int64(len(calls)) {
		t.Errorf("total outcomes = %d, want %d (one per Mediate call)", got, len(calls))
	}
}

// --- data-loss classification ---

// resolveOutcome binds the data_loss_detected counter to
// MediationError.DroppedFields rather than to a single call site. Data-loss
// policy enforcement is not implemented yet (#770); the counter starts
// reporting once it constructs this error.
func TestResolveOutcome(t *testing.T) {
	tests := []struct {
		name   string
		branch string
		err    error
		want   string
	}{
		{
			name:   "dropped fields classify as data loss",
			branch: outcomeTranslationApplied,
			err:    &MediationError{Code: "SCH_SCHEMA_ADAPTATION_FAILED", DroppedFields: []string{"order.id"}},
			want:   outcomeDataLossDetected,
		},
		{
			name:   "wrapped dropped fields classify as data loss",
			branch: outcomeTranslationFailed,
			err: errors.Join(errors.New("context"),
				&MediationError{Code: "SCH_SCHEMA_ADAPTATION_FAILED", DroppedFields: []string{"order.id"}}),
			want: outcomeDataLossDetected,
		},
		{
			name:   "rejection without dropped fields keeps the branch outcome",
			branch: outcomeRejectedByPolicy,
			err:    &MediationError{Code: "SCH_SCHEMA_ADAPTATION_FAILED"},
			want:   outcomeRejectedByPolicy,
		},
		{
			name:   "success keeps the branch outcome",
			branch: outcomeTranslationApplied,
			err:    nil,
			want:   outcomeTranslationApplied,
		},
		{
			name:   "plain error keeps the branch outcome",
			branch: outcomeTranslationFailed,
			err:    errors.New("boom"),
			want:   outcomeTranslationFailed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveOutcome(tc.branch, tc.err); got != tc.want {
				t.Errorf("resolveOutcome() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRecordOutcome_DataLossCounter(t *testing.T) {
	reader := newMetricReader(t)
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{}, nil)
	body := mediationPayload("search", "net1", "https://schema.beckn.io/retail/v2.0/Order.jsonld")

	m.recordOutcome(stepCtxWithRemoteID(body, "bap.example.com"), outcomeDataLossDetected)

	if got := counterValue(t, reader, outcomesMetric, outcomeIs(outcomeDataLossDetected)); got != 1 {
		t.Errorf("data_loss_detected count = %d, want 1", got)
	}
}

// --- cache effectiveness counters ---

func TestMediate_RecordsCacheHitsAndMisses(t *testing.T) {
	reader := newMetricReader(t)
	srv := artifactServer(t, map[string]string{
		"/retail/v1.0/Order_from_v2.0.jsonata": `{"state": status}`,
	})
	defer srv.Close()

	lm := localManifestWith(model.SchemaObject{
		BaseURL:           srv.URL + "/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{}, lm)
	m.httpClient = srv.Client()

	// Two identical requests: the first fills both caches, the second is served
	// from memory.
	for i := 0; i < 2; i++ {
		body := mediationPayload("search", "net1", srv.URL+"/retail/v2.0/Order.jsonld")
		if err := m.Mediate(stepCtxWithRemoteID(body, "bap.example.com")); err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
	}

	for _, tc := range []struct {
		cache string
	}{{cacheArtifact}, {cacheExpression}} {
		if got := counterValue(t, reader, cacheMissesMetric, attrCache.String(tc.cache)); got != 1 {
			t.Errorf("%s cache misses = %d, want 1", tc.cache, got)
		}
		if got := counterValue(t, reader, cacheHitsMetric, attrCache.String(tc.cache)); got != 1 {
			t.Errorf("%s cache hits = %d, want 1", tc.cache, got)
		}
	}
}

// A remembered 404 counts as a hit: the counter measures fetches avoided.
func TestMediate_RecordsNegativeArtifactCacheHit(t *testing.T) {
	reader := newMetricReader(t)
	srv := artifactServer(t, nil) // always 404
	defer srv.Close()

	lm := localManifestWith(model.SchemaObject{
		BaseURL:           srv.URL + "/retail",
		Type:              "Order",
		SupportedVersions: []string{"v1.0"},
	})
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{"onFailure": "passThrough"}, lm)
	m.httpClient = srv.Client()

	for i := 0; i < 2; i++ {
		body := mediationPayload("search", "net1", srv.URL+"/retail/v2.0/Order.jsonld")
		if err := m.Mediate(stepCtxWithRemoteID(body, "bap.example.com")); err != nil {
			t.Fatalf("request %d: expected pass-through, got: %v", i, err)
		}
	}

	if got := counterValue(t, reader, cacheMissesMetric, attrCache.String(cacheArtifact)); got != 1 {
		t.Errorf("artifact cache misses = %d, want 1", got)
	}
	if got := counterValue(t, reader, cacheHitsMetric, attrCache.String(cacheArtifact)); got != 1 {
		t.Errorf("artifact cache hits = %d, want 1 (negative cache entry)", got)
	}
}

// --- labels and degraded modes ---

func TestMediationAttrs(t *testing.T) {
	t.Run("identities absent from the request fall back to unknown", func(t *testing.T) {
		ctx := &model.StepContext{Context: context.Background(), Body: []byte(`{"message":{}}`)}
		want := map[attribute.Key]string{
			"action":          unknownLabel,
			"counterparty_id": unknownLabel,
			"network_id":      unknownLabel,
		}
		assertAttrs(t, mediationAttrs(ctx), want)
	})

	t.Run("network id from the request context wins over the payload", func(t *testing.T) {
		goCtx := context.WithValue(context.Background(), model.ContextKeyRemoteID, "bap.example.com")
		goCtx = context.WithValue(goCtx, model.ContextKeyNetworkID, "net-from-context")
		ctx := &model.StepContext{
			Context: goCtx,
			Body:    []byte(`{"context":{"action":"search","network_id":"net-from-body"},"message":{}}`),
		}
		want := map[attribute.Key]string{
			"action":          "search",
			"counterparty_id": "bap.example.com",
			"network_id":      "net-from-context",
		}
		assertAttrs(t, mediationAttrs(ctx), want)
	})

	t.Run("camelCase networkId in the payload is resolved", func(t *testing.T) {
		ctx := stepCtxWithRemoteID([]byte(`{"context":{"networkId":"net1"},"message":{}}`), "bap.example.com")
		assertAttrs(t, mediationAttrs(ctx), map[attribute.Key]string{"network_id": "net1"})
	})

	t.Run("malformed body degrades to unknown", func(t *testing.T) {
		ctx := stepCtxWithRemoteID([]byte(`not json`), "bap.example.com")
		want := map[attribute.Key]string{
			"action":          unknownLabel,
			"counterparty_id": "bap.example.com",
			"network_id":      unknownLabel,
		}
		assertAttrs(t, mediationAttrs(ctx), want)
	})
}

func assertAttrs(t *testing.T, got []attribute.KeyValue, want map[attribute.Key]string) {
	t.Helper()
	set := attribute.NewSet(got...)
	for key, wantValue := range want {
		value, ok := set.Value(key)
		if !ok {
			t.Errorf("attribute %q missing", key)
			continue
		}
		if value.AsString() != wantValue {
			t.Errorf("attribute %q = %q, want %q", key, value.AsString(), wantValue)
		}
	}
}

// Recording must be a no-op, not a panic, when the meter is unregistered.
func TestRecordersWithoutMetrics(t *testing.T) {
	m := &mediator{}
	body := mediationPayload("search", "net1", "https://schema.beckn.io/retail/v2.0/Order.jsonld")
	m.recordOutcome(stepCtxWithRemoteID(body, "bap.example.com"), outcomeTranslationApplied)
	m.recordCacheLookup(context.Background(), cacheArtifact, true)
	m.recordCacheLookup(context.Background(), cacheArtifact, false)
}

func TestRecordOutcome_EmptyOutcomeIgnored(t *testing.T) {
	reader := newMetricReader(t)
	m := newInstrumentedMediator(t, &mockManifestLoader{}, map[string]string{}, nil)
	body := mediationPayload("search", "net1", "https://schema.beckn.io/retail/v2.0/Order.jsonld")

	m.recordOutcome(stepCtxWithRemoteID(body, "bap.example.com"), "")

	if got := counterValue(t, reader, outcomesMetric); got != 0 {
		t.Errorf("outcome count = %d, want 0", got)
	}
}

// Metric names are the operator-facing contract; pin them.
func TestNewMediatorMetrics_InstrumentNames(t *testing.T) {
	reader := newMetricReader(t)
	metrics, err := GetMediatorMetrics(context.Background())
	if err != nil {
		t.Fatalf("GetMediatorMetrics: %v", err)
	}
	ctx := context.Background()
	metrics.MediationOutcomesTotal.Add(ctx, 1, metric.WithAttributes(outcomeIs(outcomeTranslationApplied)))
	metrics.CacheHitsTotal.Add(ctx, 1, metric.WithAttributes(attrCache.String(cacheArtifact)))
	metrics.CacheMissesTotal.Add(ctx, 1, metric.WithAttributes(attrCache.String(cacheExpression)))

	for _, name := range []string{outcomesMetric, cacheHitsMetric, cacheMissesMetric} {
		if got := counterValue(t, reader, name); got != 1 {
			t.Errorf("%s = %d, want 1", name, got)
		}
	}
}
