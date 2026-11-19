package catalogcrawler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"
	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const (
	metricDuration = "onix_plugin_execution_duration_seconds"
	metricErrors   = "onix_plugin_errors_total"
)

// setupTelemetry installs a span recorder and a manual metric reader as the
// global otel providers for the test. They are process-global, so callers must
// not use t.Parallel and must stop any Scheduler they start before returning.
func setupTelemetry(t *testing.T) (*tracetest.SpanRecorder, *sdkmetric.ManualReader) {
	t.Helper()
	prevTP, prevMP := otel.GetTracerProvider(), otel.GetMeterProvider()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetMeterProvider(prevMP)
		_ = tp.Shutdown(context.Background())
		_ = mp.Shutdown(context.Background())
	})
	return sr, reader
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) []metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting metrics: %v", err)
	}
	var out []metricdata.Metrics
	for _, sm := range rm.ScopeMetrics {
		out = append(out, sm.Metrics...)
	}
	return out
}

// matches reports whether set carries this plugin's identity labels plus
// every want label.
func matches(set attribute.Set, want map[attribute.Key]string) bool {
	if v, _ := set.Value(telemetry.AttrPluginID); v.AsString() != pluginID {
		return false
	}
	if v, _ := set.Value(telemetry.AttrPluginType); v.AsString() != pluginType {
		return false
	}
	for k, w := range want {
		if v, ok := set.Value(k); !ok || v.AsString() != w {
			return false
		}
	}
	return true
}

// errorCount sums onix_plugin_errors_total over data points matching want.
func errorCount(t *testing.T, reader *sdkmetric.ManualReader, want map[attribute.Key]string) int64 {
	t.Helper()
	var n int64
	for _, m := range collectMetrics(t, reader) {
		if m.Name != metricErrors {
			continue
		}
		sum, ok := m.Data.(metricdata.Sum[int64])
		if !ok {
			t.Fatalf("%s data is %T, want metricdata.Sum[int64]", metricErrors, m.Data)
		}
		for _, dp := range sum.DataPoints {
			if matches(dp.Attributes, want) {
				n += dp.Value
			}
		}
	}
	return n
}

// durationCount sums the sample count of onix_plugin_execution_duration_seconds
// over data points matching want.
func durationCount(t *testing.T, reader *sdkmetric.ManualReader, want map[attribute.Key]string) uint64 {
	t.Helper()
	var n uint64
	for _, m := range collectMetrics(t, reader) {
		if m.Name != metricDuration {
			continue
		}
		hist, ok := m.Data.(metricdata.Histogram[float64])
		if !ok {
			t.Fatalf("%s data is %T, want metricdata.Histogram[float64]", metricDuration, m.Data)
		}
		for _, dp := range hist.DataPoints {
			if matches(dp.Attributes, want) {
				n += dp.Count
			}
		}
	}
	return n
}

func opLabels(op, status, errType string) map[attribute.Key]string {
	l := map[attribute.Key]string{telemetry.AttrOperation: op, telemetry.AttrStatus: status}
	if errType != "" {
		l[telemetry.AttrErrorType] = errType
	}
	return l
}

func spansNamed(sr *tracetest.SpanRecorder, op string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.Name() == spanNamePrefix+op {
			out = append(out, s)
		}
	}
	return out
}

func spanAttr(s sdktrace.ReadOnlySpan, key attribute.Key) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func hasEvent(s sdktrace.ReadOnlySpan, name string) bool {
	for _, e := range s.Events() {
		if e.Name == name {
			return true
		}
	}
	return false
}

// erroringStore is a fakeStore whose ClaimNext/RequeueOrAbandonParked can be
// made to fail.
type erroringStore struct {
	*fakeStore
	claimErr error
	sweepErr error
}

func (s erroringStore) ClaimNext(ctx context.Context) (*crawlmanager.ClaimedItem, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	return s.fakeStore.ClaimNext(ctx)
}

func (s erroringStore) RequeueOrAbandonParked(ctx context.Context, olderThan time.Duration, maxParkCount int) (int, int, error) {
	if s.sweepErr != nil {
		return 0, 0, s.sweepErr
	}
	return s.fakeStore.RequeueOrAbandonParked(ctx, olderThan, maxParkCount)
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"canceled", fmt.Errorf("wrapped: %w", context.Canceled), errTypeCanceled},
		{"deadline", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), errTypeTimeout},
		{"other", errors.New("boom"), errTypeLookupFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyError(tt.err, errTypeLookupFailed); got != tt.want {
				t.Fatalf("classifyError = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSyncErrorType(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"permanent with class", fmt.Errorf("sync: %w", crawler.PermanentFaultf(crawler.FaultSignature, "bad sig")), string(crawler.FaultSignature)},
		{"permanent without class", crawler.Permanentf("corrupt"), string(crawler.FaultDecode)},
		{"transient", errors.New("connection reset"), string(crawler.FaultTransient)},
		{"canceled", fmt.Errorf("sync: %w", context.Canceled), errTypeCanceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := syncErrorType(tt.err); got != tt.want {
				t.Fatalf("syncErrorType = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegistryDiscoverer_EmitsLookupSpansAndMetrics(t *testing.T) {
	sr, reader := setupTelemetry(t)
	lookup := fakeMetadataLookup{
		byNetwork: map[string][]model.SubscriberRecord{"net-ok": {{
			Subscription: model.Subscription{Subscriber: model.Subscriber{SubscriberID: "p1"}},
			MetaArrays:   map[string][]string{"catalog_index_urls": {"https://x/index"}},
		}}},
		errByNetwork: map[string]error{"net-bad": errors.New("registry down: 404")},
	}
	d := &registryDiscoverer{lookup: lookup, networkIDs: []string{"net-ok", "net-bad"}, log: slog.Default()}
	if _, err := d.Discover(context.Background()); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	spans := spansNamed(sr, opRegistryLookup)
	if len(spans) != 2 {
		t.Fatalf("got %d registry_lookup spans, want 2 (one per network)", len(spans))
	}
	byNet := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		v, _ := spanAttr(s, attrNetworkID)
		byNet[v.AsString()] = s
	}
	ok, bad := byNet["net-ok"], byNet["net-bad"]
	if ok == nil || bad == nil {
		t.Fatalf("registry_lookup spans by network_id = %v, want net-ok and net-bad", byNet)
	}
	if ok.Status().Code == codes.Error {
		t.Errorf("net-ok span status = %v, want not Error", ok.Status())
	}
	if v, _ := spanAttr(ok, attrProvidersFound); v.AsInt64() != 1 {
		t.Errorf("net-ok providers_found = %v, want 1", v.AsInt64())
	}
	if bad.Status().Code != codes.Error {
		t.Errorf("net-bad span status = %v, want Error", bad.Status())
	}
	if !hasEvent(bad, "exception") {
		t.Error("net-bad span has no recorded error (exception event)")
	}
	if v, _ := spanAttr(bad, telemetry.AttrErrorType); v.AsString() != errTypeLookupFailed {
		t.Errorf("net-bad error_type = %q, want %q", v.AsString(), errTypeLookupFailed)
	}

	if got := errorCount(t, reader, opLabels(opRegistryLookup, statusError, errTypeLookupFailed)); got != 1 {
		t.Errorf("registry_lookup errors = %d, want 1", got)
	}
	if got := durationCount(t, reader, opLabels(opRegistryLookup, statusSuccess, "")); got != 1 {
		t.Errorf("registry_lookup success durations = %d, want 1", got)
	}
	if got := durationCount(t, reader, opLabels(opRegistryLookup, statusError, "")); got != 1 {
		t.Errorf("registry_lookup error durations = %d, want 1", got)
	}
	for _, m := range collectMetrics(t, reader) {
		if hist, isHist := m.Data.(metricdata.Histogram[float64]); isHist {
			for _, dp := range hist.DataPoints {
				if _, has := dp.Attributes.Value(attrNetworkID); has {
					t.Errorf("%s data point carries network_id label: %v", m.Name, dp.Attributes)
				}
			}
		}
	}
}

func TestPollTick_RecordsOutcome(t *testing.T) {
	tests := []struct {
		name        string
		source      crawlmanager.Source
		wantStatus  string
		wantErrType string
	}{
		{
			name:       "success",
			source:     stubSource{},
			wantStatus: statusSuccess,
		},
		{
			name:        "one of two discovery sources fails",
			source:      multiSource{sources: []crawlmanager.Source{stubSource{}, stubSource{err: errors.New("registry unreachable")}}, log: slog.Default()},
			wantStatus:  statusPartialFailure,
			wantErrType: errTypePartialDiscovery,
		},
		{
			name: "one of two registry networks fails",
			source: &registryDiscoverer{
				lookup:     fakeMetadataLookup{byNetwork: map[string][]model.SubscriberRecord{"net-ok": {}}, errByNetwork: map[string]error{"net-bad": errors.New("404")}},
				networkIDs: []string{"net-ok", "net-bad"},
				log:        slog.Default(),
			},
			wantStatus:  statusPartialFailure,
			wantErrType: errTypePartialDiscovery,
		},
		{
			name:        "discovery fails outright",
			source:      stubSource{err: errors.New("registry unreachable")},
			wantStatus:  statusError,
			wantErrType: errTypePollFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sr, reader := setupTelemetry(t)
			s := NewScheduler(crawlmanager.Params{Source: tt.source, Store: &fakeStore{}}, SchedulerConfig{}, nil)

			parentCtx, parent := otel.Tracer("test").Start(context.Background(), "outer")
			s.pollTick(parentCtx)
			parent.End()

			spans := spansNamed(sr, opPollTick)
			if len(spans) != 1 {
				t.Fatalf("got %d poll_tick spans, want 1", len(spans))
			}
			tick := spans[0]
			if tick.Parent().IsValid() {
				t.Errorf("poll_tick span has parent %v, want a new root span", tick.Parent().SpanID())
			}
			if v, _ := spanAttr(tick, telemetry.AttrStatus); v.AsString() != tt.wantStatus {
				t.Errorf("poll_tick span status attr = %q, want %q", v.AsString(), tt.wantStatus)
			}
			if wantErrSpan := tt.wantStatus == statusError; (tick.Status().Code == codes.Error) != wantErrSpan {
				t.Errorf("poll_tick span status code = %v, want Error=%v", tick.Status().Code, wantErrSpan)
			}
			if wantEvent := tt.wantStatus == statusPartialFailure; hasEvent(tick, "partial discovery failure") != wantEvent {
				t.Errorf("poll_tick partial-failure event present = %v, want %v", !wantEvent, wantEvent)
			}

			if got := durationCount(t, reader, opLabels(opPollTick, tt.wantStatus, "")); got != 1 {
				t.Errorf("poll_tick %s durations = %d, want 1", tt.wantStatus, got)
			}
			wantErrs := int64(0)
			if tt.wantErrType != "" {
				wantErrs = 1
			}
			if got := errorCount(t, reader, map[attribute.Key]string{telemetry.AttrOperation: opPollTick}); got != wantErrs {
				t.Errorf("poll_tick errors (any label) = %d, want %d", got, wantErrs)
			}
			if tt.wantErrType != "" {
				if got := errorCount(t, reader, opLabels(opPollTick, tt.wantStatus, tt.wantErrType)); got != 1 {
					t.Errorf("poll_tick errors{status=%s,error_type=%s} = %d, want 1", tt.wantStatus, tt.wantErrType, got)
				}
			}

			for _, l := range spansNamed(sr, opRegistryLookup) {
				if l.Parent().SpanID() != tick.SpanContext().SpanID() {
					t.Errorf("registry_lookup span parent = %v, want the poll_tick span %v", l.Parent().SpanID(), tick.SpanContext().SpanID())
				}
			}
		})
	}
}

// The queue holds one item that syncs (a retire with nothing to wipe) and one
// that fails (a sync with no fetchable index).
func TestSyncTick_EmitsSyncNextSpansAndMetrics(t *testing.T) {
	sr, reader := setupTelemetry(t)
	ctx := context.Background()
	fs := &fakeStore{}
	for _, item := range []crawlmanager.QueueItem{
		{CatalogID: "p/retired", Op: crawlmanager.OpRetire},
		{CatalogID: "p/broken", Op: crawlmanager.OpSync},
	} {
		if err := fs.Enqueue(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	s := NewScheduler(crawlmanager.Params{Source: stubSource{}, Store: fs, Fetcher: noopFetcher()}, SchedulerConfig{}, nil)
	s.syncTick(ctx)

	ticks := spansNamed(sr, opSyncTick)
	if len(ticks) != 1 {
		t.Fatalf("got %d sync_tick spans, want 1", len(ticks))
	}
	tick := ticks[0]
	if tick.Parent().IsValid() {
		t.Error("sync_tick span should be a new root span")
	}
	if v, _ := spanAttr(tick, attrItemsClaimed); v.AsInt64() != 2 {
		t.Errorf("sync_tick items_claimed = %d, want 2", v.AsInt64())
	}

	claims := spansNamed(sr, opSyncNext)
	if len(claims) != 3 {
		t.Fatalf("got %d sync_next spans, want 3 (two claimed items + the empty claim ending the drain)", len(claims))
	}
	var failedErrType string
	for _, c := range claims {
		if c.Parent().SpanID() != tick.SpanContext().SpanID() {
			t.Errorf("sync_next span parent = %v, want the sync_tick span", c.Parent().SpanID())
		}
		if c.Status().Code == codes.Error {
			v, _ := spanAttr(c, telemetry.AttrErrorType)
			failedErrType = v.AsString()
		}
	}
	if failedErrType == "" || failedErrType == errTypeClaimFailed {
		t.Fatalf("failed sync_next error_type = %q, want a sync fault class", failedErrType)
	}

	for _, status := range []string{statusSuccess, statusEmpty, statusError} {
		if got := durationCount(t, reader, opLabels(opSyncNext, status, "")); got != 1 {
			t.Errorf("sync_next %s durations = %d, want 1", status, got)
		}
	}
	if got := errorCount(t, reader, opLabels(opSyncNext, statusError, failedErrType)); got != 1 {
		t.Errorf("sync_next errors{error_type=%s} = %d, want 1", failedErrType, got)
	}
	if got := errorCount(t, reader, map[attribute.Key]string{telemetry.AttrOperation: opSyncNext}); got != 1 {
		t.Errorf("sync_next errors (any label) = %d, want 1 (an empty claim is not an error)", got)
	}
}

func TestSyncTick_ClaimFailureIsCounted(t *testing.T) {
	sr, reader := setupTelemetry(t)
	store := erroringStore{fakeStore: &fakeStore{}, claimErr: errors.New("db down")}
	s := NewScheduler(crawlmanager.Params{Source: stubSource{}, Store: store}, SchedulerConfig{}, nil)
	s.syncTick(context.Background())

	claims := spansNamed(sr, opSyncNext)
	if len(claims) != 1 || claims[0].Status().Code != codes.Error {
		t.Fatalf("sync_next spans = %d (want 1, with Error status)", len(claims))
	}
	if got := errorCount(t, reader, opLabels(opSyncNext, statusError, errTypeClaimFailed)); got != 1 {
		t.Errorf("sync_next claim_failed errors = %d, want 1", got)
	}
}

func TestParkSweepTick_RecordsOutcome(t *testing.T) {
	tests := []struct {
		name        string
		sweepErr    error
		wantStatus  string
		wantErrType string
	}{
		{"success", nil, statusSuccess, ""},
		{"store failure", errors.New("db down"), statusError, errTypeSweepFailed},
		{"canceled", context.Canceled, statusError, errTypeCanceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sr, reader := setupTelemetry(t)
			store := erroringStore{fakeStore: &fakeStore{}, sweepErr: tt.sweepErr}
			s := NewScheduler(crawlmanager.Params{Source: stubSource{}, Store: store}, SchedulerConfig{}, nil)
			s.parkSweepTick(context.Background())

			spans := spansNamed(sr, opParkSweep)
			if len(spans) != 1 {
				t.Fatalf("got %d park_sweep spans, want 1", len(spans))
			}
			if spans[0].Parent().IsValid() {
				t.Error("park_sweep span should be a new root span")
			}
			if got := durationCount(t, reader, opLabels(opParkSweep, tt.wantStatus, "")); got != 1 {
				t.Errorf("park_sweep %s durations = %d, want 1", tt.wantStatus, got)
			}
			wantErrs := int64(0)
			if tt.wantErrType != "" {
				wantErrs = 1
			}
			if tt.wantErrType != "" {
				if got := errorCount(t, reader, opLabels(opParkSweep, tt.wantStatus, tt.wantErrType)); got != 1 {
					t.Errorf("park_sweep errors{error_type=%s} = %d, want 1", tt.wantErrType, got)
				}
			}
			if got := errorCount(t, reader, map[attribute.Key]string{telemetry.AttrOperation: opParkSweep}); got != wantErrs {
				t.Errorf("park_sweep errors (any label) = %d, want %d", got, wantErrs)
			}
		})
	}
}

func TestCrawlRegistry_EmitsOnDemandCrawlSpanAndMetrics(t *testing.T) {
	tests := []struct {
		name        string
		failing     map[string]error
		wantStatus  string
		wantErrType string
	}{
		{"all networks succeed", nil, statusSuccess, ""},
		{"one network fails", map[string]error{"net-b": errors.New("404")}, statusPartialFailure, errTypePartialDiscovery},
		{"every network fails", map[string]error{"net-a": errors.New("404"), "net-b": errors.New("timeout")}, statusError, errTypePollFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sr, reader := setupTelemetry(t)
			lookup := fakeMetadataLookup{byNetwork: map[string][]model.SubscriberRecord{}, errByNetwork: tt.failing}
			params := crawlmanager.Params{Source: stubSource{}, Store: &fakeStore{}}
			c := &crawlerImpl{
				params:         params,
				sched:          NewScheduler(params, SchedulerConfig{IndexInterval: time.Hour, CatalogInterval: time.Hour, ParkSweepInterval: time.Hour}, nil),
				metadataLookup: lookup,
				log:            slog.Default(),
			}
			if err := c.Start(context.Background()); err != nil {
				t.Fatal(err)
			}

			reqCtx, reqSpan := otel.Tracer("test").Start(context.Background(), "request")
			runID, err := c.CrawlRegistry(reqCtx, []string{"net-a", "net-b"})
			reqSpan.End()
			if err != nil {
				t.Fatalf("CrawlRegistry: %v", err)
			}
			if err := c.Stop(); err != nil { // waits for the on-demand run
				t.Fatal(err)
			}

			spans := spansNamed(sr, opOnDemandCrawl)
			if len(spans) != 1 {
				t.Fatalf("got %d on_demand_crawl spans, want 1", len(spans))
			}
			run := spans[0]
			if run.Parent().IsValid() {
				t.Error("on_demand_crawl span should be a new root span, not a child of the request")
			}
			if links := run.Links(); len(links) != 1 || links[0].SpanContext.SpanID() != reqSpan.SpanContext().SpanID() {
				t.Errorf("on_demand_crawl links = %+v, want one link to the request span", links)
			}
			if v, _ := spanAttr(run, attrRunID); v.AsString() != runID {
				t.Errorf("on_demand_crawl run_id = %q, want %q", v.AsString(), runID)
			}
			if got := len(spansNamed(sr, opRegistryLookup)); got != 2 {
				t.Errorf("got %d registry_lookup spans, want 2", got)
			}
			if got := durationCount(t, reader, opLabels(opOnDemandCrawl, tt.wantStatus, "")); got != 1 {
				t.Errorf("on_demand_crawl %s durations = %d, want 1", tt.wantStatus, got)
			}
			if tt.wantErrType != "" {
				if got := errorCount(t, reader, opLabels(opOnDemandCrawl, tt.wantStatus, tt.wantErrType)); got != 1 {
					t.Errorf("on_demand_crawl errors{status=%s,error_type=%s} = %d, want 1", tt.wantStatus, tt.wantErrType, got)
				}
			} else if got := errorCount(t, reader, map[attribute.Key]string{telemetry.AttrOperation: opOnDemandCrawl}); got != 0 {
				t.Errorf("on_demand_crawl errors = %d, want 0", got)
			}
		})
	}
}

func TestInstrumentation_SafeWithNoopProviders(t *testing.T) {
	prevTP, prevMP := otel.GetTracerProvider(), otel.GetMeterProvider()
	otel.SetTracerProvider(tracenoop.NewTracerProvider())
	otel.SetMeterProvider(metricnoop.NewMeterProvider())
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetMeterProvider(prevMP)
	})

	store := erroringStore{fakeStore: &fakeStore{}, claimErr: errors.New("db down"), sweepErr: errors.New("db down")}
	src := multiSource{sources: []crawlmanager.Source{stubSource{}, stubSource{err: errors.New("down")}}, log: slog.Default()}
	s := NewScheduler(crawlmanager.Params{Source: src, Store: store}, SchedulerConfig{}, nil)
	ctx := context.Background()
	s.pollTick(ctx)
	s.syncTick(ctx)
	s.parkSweepTick(ctx)
}

func TestRecordOperation_LongDurationLandsInFiniteBucket(t *testing.T) {
	_, reader := setupTelemetry(t)
	recordOperation(context.Background(), opPollTick, statusSuccess, "", 45*time.Second)

	for _, m := range collectMetrics(t, reader) {
		if m.Name != metricDuration {
			continue
		}
		hist, ok := m.Data.(metricdata.Histogram[float64])
		if !ok || len(hist.DataPoints) != 1 {
			t.Fatalf("%s: got %T with unexpected data points", metricDuration, m.Data)
		}
		dp := hist.DataPoints[0]
		if inf := dp.BucketCounts[len(dp.BucketCounts)-1]; inf != 0 {
			t.Fatalf("45s sample landed in +Inf (bounds %v)", dp.Bounds)
		}
		return
	}
	t.Fatalf("no %s data recorded", metricDuration)
}
