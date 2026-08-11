package runner

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/store"
)

// classifyOutcome is the ONE place the push-outcome rule lives. External
// behavior (retry-vs-park, cursor advancement) is decided elsewhere; this only
// pins the persisted SyncOutcome string.
func TestClassifyOutcome(t *testing.T) {
	tests := []struct {
		name       string
		httpStatus int
		acked      int
		err        error
		want       catalog.SyncOutcome
	}{
		{"4xx rejection -> faulted", 400, 0, nil, catalog.OutcomeFaulted},
		{"4xx even with acked -> faulted", 409, 2, nil, catalog.OutcomeFaulted},
		{"transport error -> faulted", 0, 0, errors.New("boom"), catalog.OutcomeFaulted},
		{"5xx none acked -> faulted", 500, 0, nil, catalog.OutcomeFaulted},
		{"5xx some acked -> partial", 500, 1, nil, catalog.OutcomePartial},
		{"pre-push failure (status 0) -> faulted", 0, 0, nil, catalog.OutcomeFaulted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyOutcome(tt.httpStatus, tt.acked, tt.err); got != tt.want {
				t.Errorf("classifyOutcome(%d,%d,%v) = %q, want %q", tt.httpStatus, tt.acked, tt.err, got, tt.want)
			}
		})
	}
}

// decideFailureAction is the ONE place the park-vs-retry policy lives. The two
// rules that are easy to get wrong, and that this table pins:
//   - MaxAttempts must actually give up (park), or a permanently failing catalog
//     retries forever and never becomes operator-actionable;
//   - a partial push (acked > 0) must never park, even on a 4xx — Discovery
//     already holds a prefix of a version our cursor hasn't reached, and parking
//     freezes that split brain silently.
func TestDecideFailureAction(t *testing.T) {
	tests := []struct {
		name        string
		fault       catalog.FaultClass
		acked       int
		attempts    int // the attempt this failure just consumed (item.Attempts+1)
		maxAttempts int
		want        failureAction
	}{
		// Partial pushes: acked > 0 outranks everything, including a permanent class.
		{"4xx with acked batches retries (never park a partial write)", catalog.FaultPushRejected, 2, 1, 3, actionRetry},
		{"4xx with acked batches retries past MaxAttempts too", catalog.FaultPushRejected, 2, 9, 3, actionRetry},
		{"5xx with acked batches retries", catalog.FaultTransient, 1, 1, 3, actionRetry},
		{"5xx with acked batches retries past MaxAttempts too", catalog.FaultTransient, 1, 9, 3, actionRetry},

		// Genuine permanent rejections: nothing landed, so parking is safe.
		{"4xx with nothing acked parks", catalog.FaultPushRejected, 0, 1, 3, actionPark},
		{"corrupt artifact parks on the first attempt", catalog.FaultDigestMismatch, 0, 1, 3, actionPark},
		{"schema rejection parks on the first attempt", catalog.FaultPushSchema, 0, 1, 3, actionPark},

		// Transient faults: retry until the budget is spent, then park.
		{"transient under the budget retries", catalog.FaultTransient, 0, 1, 3, actionRetry},
		{"transient one below the budget retries", catalog.FaultTransient, 0, 2, 3, actionRetry},
		{"transient at MaxAttempts parks", catalog.FaultTransient, 0, 3, 3, actionPark},
		{"transient past MaxAttempts parks", catalog.FaultTransient, 0, 9, 3, actionPark},
		{"index fetch failure at MaxAttempts parks", catalog.FaultIndexFetch, 0, 3, 3, actionPark},
		{"store failure at MaxAttempts parks", catalog.FaultStore, 0, 3, 3, actionPark},

		// An unset cap must not park on the first failure (zero-value config).
		{"no cap configured never parks a transient", catalog.FaultTransient, 0, 99, 0, actionRetry},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideFailureAction(tt.fault, tt.acked, tt.attempts, tt.maxAttempts)
			if got != tt.want {
				t.Errorf("decideFailureAction(%q, acked=%d, attempts=%d, max=%d) = %v, want %v",
					tt.fault, tt.acked, tt.attempts, tt.maxAttempts, got, tt.want)
			}
		})
	}
}

// closedStore builds a Store whose every call fails immediately (the pool is
// closed, so nothing touches the network). Each attempted store op then shows up
// as a storeUnhealthy line naming it, which is how routeFailure's chosen path is
// observed without a live database.
func closedStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open("postgres://u:p@127.0.0.1:5432/none?sslmode=disable")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return store.New(db)
}

// storeOps collects the `operation` field of every recorded storeUnhealthy line.
func storeOps(f *fakeLogger) []string {
	var ops []string
	for _, e := range f.entries {
		if op, ok := e.kvString("operation"); ok {
			ops = append(ops, op)
		}
	}
	return ops
}

// willRetryField returns the will_retry field of the single failure terminal
// (logFailed): true when the item was rescheduled, false when it was parked.
func willRetryField(t *testing.T, f *fakeLogger) bool {
	t.Helper()
	for _, e := range f.entries {
		for i := 0; i+1 < len(e.kv); i += 2 {
			if k, ok := e.kv[i].(string); ok && k == "will_retry" {
				v, ok := e.kv[i+1].(bool)
				if !ok {
					t.Fatalf("will_retry field is %T, want bool", e.kv[i+1])
				}
				return v
			}
		}
	}
	t.Fatalf("no failure terminal logged; entries: %+v", f.entries)
	return false
}

// routeFailure must send each failure down the store path its policy chose: a
// park releases the row with ParkQueueItem (ERROR, never re-claimed on its own),
// a retry releases it with RescheduleQueueItem (WARN, behind the backoff). The
// two are mutually exclusive — a row that is both parked and rescheduled would
// be a policy bug even if the decision itself were right.
func TestRouteFailure_ParkVsRetry(t *testing.T) {
	tests := []struct {
		name          string
		httpStatus    int
		err           error
		acked         int
		priorAttempts int
		wantPark      bool
	}{
		{"4xx with acked batches must not park", 409, nil, 2, 0, false},
		{"4xx with nothing acked parks", 400, nil, 0, 0, true},
		{"transient under the budget retries", 503, nil, 0, 0, false},
		{"transient at MaxAttempts parks", 503, nil, 0, 2, true},
		{"transient at MaxAttempts with acked batches still retries", 503, nil, 1, 2, false},
		{"429 is retryable even though it is a 4xx", 429, nil, 0, 0, false},
		{"permanent content fault parks on the first attempt", 0, catalog.PermanentFaultf(catalog.FaultDigestMismatch, "tampered"), 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &fakeLogger{}
			eng := New(EngineConfig{
				MaxAttempts: 3, IndexInterval: time.Hour, CatalogInterval: time.Hour,
			}, Deps{
				Store:   closedStore(t),
				Log:     log,
				Metrics: NopMetrics{},
				NewID:   func() string { return "id" },
			})
			item := &catalog.ClaimedItem{
				ID: "q1", ClaimID: "c1", CatalogID: "p/c", IndexURL: "https://x/i.json",
				FromVersion: 1, ToVersion: 2, Op: "sync", Attempts: tt.priorAttempts,
			}
			report := catalog.PassReport{
				At: time.Now().UTC(), FromVersion: 1, ToVersion: 2,
				BatchesAcked: tt.acked, BatchesTotal: 3,
				Outcome:    string(classifyOutcome(tt.httpStatus, tt.acked, tt.err)),
				HTTPStatus: tt.httpStatus, Reason: "push: rejected",
			}

			eng.routeFailure(context.Background(), item, report, tt.err, "run", "pass")

			var parked, rescheduled bool
			for _, op := range storeOps(log) {
				switch op {
				case "park":
					parked = true
				case "reschedule":
					rescheduled = true
				}
			}
			if parked != tt.wantPark || rescheduled == tt.wantPark {
				t.Fatalf("parked=%v rescheduled=%v, want parked=%v; store ops: %v",
					parked, rescheduled, tt.wantPark, storeOps(log))
			}
			if got := willRetryField(t, log); got == tt.wantPark {
				t.Fatalf("failure terminal will_retry = %v, want %v", got, !tt.wantPark)
			}
		})
	}
}

// recordingStore is a Store whose every call succeeds and that remembers the
// queue transition the sync policy chose plus the pass reports it persisted.
// closedStore shows WHICH store op was attempted; this one also shows WHAT was
// written, which is the only way to check the acked count that reaches the
// history.
type recordingStore struct {
	parked      int
	rescheduled int
	reports     []catalog.PassReport
	completed   []completedCall

	// envelope* configure GetCatalogEnvelope's canned response, for the retire
	// tests: envelopeOK false is "never synced with an envelope captured".
	envelopeOK                                 bool
	envelopeDescriptor, envelopeProvider       []byte
	envelopeCatalogType, envelopeParticipantID string
}

// completedCall is one settle: the version the cursor advanced to and the
// catalog state written with it. A settle is the only thing that advances the
// cursor, so counting them is how "this pass silently accepted the content" is
// observed.
type completedCall struct {
	toVersion int64
	state     catalog.CatalogState
}

func (s *recordingStore) ParkQueueItem(context.Context, string, string) error {
	s.parked++
	return nil
}

func (s *recordingStore) RescheduleQueueItem(context.Context, string, string, time.Time) error {
	s.rescheduled++
	return nil
}

func (s *recordingStore) RecordFailure(_ context.Context, _, _, _ string, report catalog.PassReport) error {
	s.reports = append(s.reports, report)
	return nil
}

func (s *recordingStore) GetCatalogVersion(context.Context, string) (int64, int64, bool, error) {
	return 0, 0, false, nil
}
func (s *recordingStore) UpsertCatalog(context.Context, catalog.CatalogState) error { return nil }
func (s *recordingStore) CountParked(context.Context) (int, error)                  { return 0, nil }
func (s *recordingStore) CountTracked(context.Context) (int, error)                 { return 0, nil }
func (s *recordingStore) GetCatalogReports(context.Context, string) ([]catalog.PassReport, error) {
	return nil, nil
}
func (s *recordingStore) GetCatalogEnvelope(context.Context, string) ([]byte, []byte, string, string, bool, error) {
	return s.envelopeDescriptor, s.envelopeProvider, s.envelopeCatalogType, s.envelopeParticipantID, s.envelopeOK, nil
}
func (s *recordingStore) GetIndex(context.Context, string) (*catalog.IndexState, error) {
	return nil, nil
}
func (s *recordingStore) KnownIndexes(context.Context) ([]catalog.KnownIndex, error) {
	return nil, nil
}
func (s *recordingStore) UpsertIndex(context.Context, string, string, string, string, time.Time, string, string) error {
	return nil
}
func (s *recordingStore) AdvanceIndexCadence(context.Context, string, time.Time) error { return nil }
func (s *recordingStore) Enqueue(context.Context, catalog.QueueItem) error             { return nil }
func (s *recordingStore) ClaimNext(context.Context) (*catalog.ClaimedItem, error)      { return nil, nil }
func (s *recordingStore) Complete(_ context.Context, _, _ string, toVersion int64, c catalog.CatalogState) error {
	s.completed = append(s.completed, completedCall{toVersion: toVersion, state: c})
	return nil
}
func (s *recordingStore) QueueDepth(context.Context) (int, error) { return 0, nil }

// ackThenStop is a Pusher that acks every batch handed to it and counts the
// calls. The batch that fails in these tests fails BEFORE the push (bad body or
// bad schema), so the push count also proves the loop stopped there.
func ackThenStop(calls *int) Pusher {
	return func(context.Context, []byte) (publish.BatchOutcome, error) {
		*calls++
		return publish.BatchOutcome{Acked: true, HTTPStatus: 200}, nil
	}
}

// failValidateOn returns a Validator that rejects the nth body (1-based) and
// accepts every other one, so a mid-loop schema failure can be staged.
func failValidateOn(n int) Validator {
	seen := 0
	return func(context.Context, []byte) error {
		seen++
		if seen == n {
			return errors.New("schema: resources[0].id is required")
		}
		return nil
	}
}

// retireCatalog must build a Discovery wipe (FULL, id+descriptor+provider,
// empty resources) from the stored envelope, push it, and only settle
// CatalogRetired once it's acked -- never before, and never at all if the push
// fails (a failed wipe must retry/park like any other sync failure, not
// silently mark the catalog retired while Discovery still serves it).
func TestRetireCatalog_WipesFromStoredEnvelope(t *testing.T) {
	st := &recordingStore{
		envelopeOK:            true,
		envelopeDescriptor:    json.RawMessage(`{"name":"Old Catalog"}`),
		envelopeProvider:      json.RawMessage(`{"id":"prov-1"}`),
		envelopeCatalogType:   "REGULAR",
		envelopeParticipantID: "bpp.example.com",
	}
	var pushedBodies [][]byte
	push := func(_ context.Context, body []byte) (publish.BatchOutcome, error) {
		pushedBodies = append(pushedBodies, body)
		return publish.BatchOutcome{Acked: true, HTTPStatus: 200}, nil
	}
	eng := New(EngineConfig{MaxAttempts: 3}, Deps{
		Store: st, Push: push, Log: &fakeLogger{}, Metrics: NopMetrics{},
		Now: time.Now, NewID: func() string { return "id" },
	})
	item := &catalog.ClaimedItem{
		ID: "q1", ClaimID: "c1", CatalogID: "p/c", IndexURL: "https://x/i.json",
		FromVersion: 3, ToVersion: 3, Op: "retire",
	}

	outcome := eng.handleQueueItem(context.Background(), item, "run")

	if outcome != catalog.OutcomeRetired {
		t.Fatalf("outcome = %v, want OutcomeRetired", outcome)
	}
	if len(pushedBodies) != 1 {
		t.Fatalf("push calls = %d, want 1", len(pushedBodies))
	}
	var body struct {
		Message struct {
			Catalogs []struct {
				ID         string            `json:"id"`
				Descriptor json.RawMessage   `json:"descriptor"`
				Provider   json.RawMessage   `json:"provider"`
				Resources  []json.RawMessage `json:"resources"`
				Offers     []json.RawMessage `json:"offers"`
			} `json:"catalogs"`
			PublishDirectives []struct {
				CatalogID   string `json:"catalogId"`
				CatalogType string `json:"catalogType"`
				UpdateMode  string `json:"updateMode"`
			} `json:"publishDirectives"`
		} `json:"message"`
	}
	if err := json.Unmarshal(pushedBodies[0], &body); err != nil {
		t.Fatalf("parsing pushed body: %v", err)
	}
	if len(body.Message.Catalogs) != 1 {
		t.Fatalf("catalogs = %d, want 1", len(body.Message.Catalogs))
	}
	c := body.Message.Catalogs[0]
	if c.ID != "p/c" || string(c.Descriptor) != `{"name":"Old Catalog"}` || string(c.Provider) != `{"id":"prov-1"}` {
		t.Fatalf("catalog = %+v, want id=p/c with the stored descriptor/provider", c)
	}
	if len(c.Resources) != 0 || c.Offers != nil {
		t.Fatalf("resources/offers = %v/%v, want empty/absent (a genuine wipe)", c.Resources, c.Offers)
	}
	if len(body.Message.PublishDirectives) != 1 || body.Message.PublishDirectives[0].UpdateMode != publish.UpdateModeFull {
		t.Fatalf("directives = %+v, want one FULL directive", body.Message.PublishDirectives)
	}
	if len(st.completed) != 1 || st.completed[0].state.Status != string(catalog.CatalogRetired) {
		t.Fatalf("completed = %+v, want one CatalogRetired settle", st.completed)
	}
}

// A push that isn't acked must NOT settle the retire -- it goes through the
// same park-vs-retry policy a normal sync failure does.
func TestRetireCatalog_PushFailureDoesNotSettle(t *testing.T) {
	st := &recordingStore{envelopeOK: true, envelopeDescriptor: json.RawMessage(`{}`), envelopeProvider: json.RawMessage(`{}`)}
	push := func(context.Context, []byte) (publish.BatchOutcome, error) {
		return publish.BatchOutcome{Acked: false, HTTPStatus: 500, Reason: "discovery unavailable"}, nil
	}
	eng := New(EngineConfig{MaxAttempts: 3}, Deps{
		Store: st, Push: push, Log: &fakeLogger{}, Metrics: NopMetrics{},
		Now: time.Now, NewID: func() string { return "id" },
	})
	item := &catalog.ClaimedItem{
		ID: "q1", ClaimID: "c1", CatalogID: "p/c", IndexURL: "https://x/i.json",
		FromVersion: 3, ToVersion: 3, Op: "retire",
	}

	outcome := eng.handleQueueItem(context.Background(), item, "run")

	if outcome != catalog.OutcomeFaulted {
		t.Fatalf("outcome = %v, want OutcomeFaulted", outcome)
	}
	if len(st.completed) != 0 {
		t.Fatalf("completed = %+v, want none — a failed wipe must not settle as retired", st.completed)
	}
	if st.rescheduled == 0 && st.parked == 0 {
		t.Fatalf("expected the failure routed to either reschedule or park")
	}
}

// A catalog retired with no envelope ever captured (shouldn't reach here in
// practice — decideCatalog's !seen guard refuses to enqueue this — but
// defensively) settles as retired with no push, rather than sending a
// malformed wipe.
func TestRetireCatalog_NoEnvelopeSettlesWithoutPush(t *testing.T) {
	st := &recordingStore{envelopeOK: false}
	pushed := false
	push := func(context.Context, []byte) (publish.BatchOutcome, error) {
		pushed = true
		return publish.BatchOutcome{Acked: true, HTTPStatus: 200}, nil
	}
	eng := New(EngineConfig{}, Deps{
		Store: st, Push: push, Log: &fakeLogger{}, Metrics: NopMetrics{},
		Now: time.Now, NewID: func() string { return "id" },
	})
	item := &catalog.ClaimedItem{
		ID: "q1", ClaimID: "c1", CatalogID: "p/c", IndexURL: "https://x/i.json",
		FromVersion: 3, ToVersion: 3, Op: "retire",
	}

	outcome := eng.handleQueueItem(context.Background(), item, "run")

	if outcome != catalog.OutcomeRetired {
		t.Fatalf("outcome = %v, want OutcomeRetired", outcome)
	}
	if pushed {
		t.Fatal("expected no push when there is no stored envelope")
	}
	if len(st.completed) != 1 || st.completed[0].state.Status != string(catalog.CatalogRetired) {
		t.Fatalf("completed = %+v, want one CatalogRetired settle", st.completed)
	}
}

// goodDoc / badDoc are batch docs: one BuildPushBody can read, one it cannot
// (BuildPushBody's only failure mode is a doc that will not unmarshal).
var (
	goodDoc = []byte(`{"id":"p/c","resources":[{"id":"r1"}]}`)
	badDoc  = []byte(`{not json`)
)

// A failure raised INSIDE the batch loop — the push body could not be built, or
// it failed local schema validation — must obey the same park-vs-retry policy as
// a remote rejection.
//
// This is the split-brain case. Batch 1 is pushed and Discovery durably applies
// it; batch 2 then fails locally. Parking there would freeze the catalog
// half-applied: no retry, no cursor advance, and nothing self-heals until the
// publisher happens to bump the version. So an acked prefix must retry, and the
// persisted report must carry the true acked count — a report claiming 0 acked
// hides the half-applied state from the operator.
//
// Nothing acked is the opposite case and must still park: a malformed body or a
// schema rejection will not fix itself on retry.
func TestPublish_LocalFailureMidBatch_RoutesOnAckedCount(t *testing.T) {
	tests := []struct {
		name          string
		docs          [][]byte
		validate      Validator
		priorAttempts int
		wantPushes    int
		wantPark      bool
		wantOutcome   catalog.SyncOutcome
		wantRecorded  bool // a pass report reached RecordFailure
		wantAcked     int  // BatchesAcked on that report
	}{
		{
			name: "build_push fails on batch 2 after batch 1 acked -> retries",
			docs: [][]byte{goodDoc, badDoc}, priorAttempts: 0,
			wantPushes: 1, wantPark: false, wantOutcome: catalog.OutcomePartial,
			wantRecorded: false,
		},
		{
			name: "build_push fails on batch 2 past the attempt budget -> still retries, records true acked",
			docs: [][]byte{goodDoc, badDoc}, priorAttempts: 3,
			wantPushes: 1, wantPark: false, wantOutcome: catalog.OutcomePartial,
			wantRecorded: true, wantAcked: 1,
		},
		{
			name: "schema fails on batch 2 after batch 1 acked -> retries",
			docs: [][]byte{goodDoc, goodDoc}, validate: failValidateOn(2), priorAttempts: 0,
			wantPushes: 1, wantPark: false, wantOutcome: catalog.OutcomePartial,
			wantRecorded: false,
		},
		{
			name: "schema fails on batch 2 past the attempt budget -> still retries, records true acked",
			docs: [][]byte{goodDoc, goodDoc}, validate: failValidateOn(2), priorAttempts: 3,
			wantPushes: 1, wantPark: false, wantOutcome: catalog.OutcomePartial,
			wantRecorded: true, wantAcked: 1,
		},
		{
			name: "build_push fails on batch 1 with nothing acked -> parks",
			docs: [][]byte{badDoc, goodDoc}, priorAttempts: 0,
			wantPushes: 0, wantPark: true, wantOutcome: catalog.OutcomeFaulted,
			wantRecorded: true, wantAcked: 0,
		},
		{
			name: "schema fails on batch 1 with nothing acked -> parks",
			docs: [][]byte{goodDoc, goodDoc}, validate: failValidateOn(1), priorAttempts: 0,
			wantPushes: 0, wantPark: true, wantOutcome: catalog.OutcomeFaulted,
			wantRecorded: true, wantAcked: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &recordingStore{}
			pushes := 0
			eng := New(EngineConfig{
				MaxAttempts: 3, IndexInterval: time.Hour, CatalogInterval: time.Hour,
			}, Deps{
				Store:    st,
				Log:      &fakeLogger{},
				Metrics:  NopMetrics{},
				Validate: tt.validate,
				Push:     ackThenStop(&pushes),
				NewID:    func() string { return "id" },
			})

			batches := make([]publish.CatalogBatch, 0, len(tt.docs))
			for i, d := range tt.docs {
				mode := publish.UpdateModeMerge
				if i == 0 {
					mode = publish.UpdateModeFull
				}
				batches = append(batches, publish.CatalogBatch{Doc: d, UpdateMode: mode})
			}
			s := &syncState{
				item: &catalog.ClaimedItem{
					ID: "q1", ClaimID: "c1", CatalogID: "p/c", IndexURL: "https://x/i.json",
					FromVersion: 1, ToVersion: 2, Op: "sync", Attempts: tt.priorAttempts,
				},
				runID: "run", passID: "pass",
				nodeID: "p", mode: publish.UpdateModeMerge,
				resCount: 2, batches: batches,
			}

			outcome, stop := eng.publish(context.Background(), s)

			if !stop {
				t.Fatalf("publish must stop the pipeline on a failed batch, got stop=false")
			}
			if outcome != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", outcome, tt.wantOutcome)
			}
			if pushes != tt.wantPushes {
				t.Errorf("pushed %d batch(es), want %d (the loop must stop at the failed batch)", pushes, tt.wantPushes)
			}
			if (st.parked > 0) != tt.wantPark {
				t.Fatalf("parked=%d rescheduled=%d, want parked=%v; an acked prefix must never park",
					st.parked, st.rescheduled, tt.wantPark)
			}
			if (st.rescheduled > 0) == tt.wantPark {
				t.Fatalf("parked=%d rescheduled=%d, want rescheduled=%v", st.parked, st.rescheduled, !tt.wantPark)
			}
			if got := len(st.reports) > 0; got != tt.wantRecorded {
				t.Fatalf("recorded %d pass report(s), want recorded=%v", len(st.reports), tt.wantRecorded)
			}
			if !tt.wantRecorded {
				return
			}
			rep := st.reports[0]
			if rep.BatchesAcked != tt.wantAcked {
				t.Errorf("persisted report BatchesAcked = %d, want %d; a report that understates the "+
					"acked count hides a half-applied catalog", rep.BatchesAcked, tt.wantAcked)
			}
			if rep.BatchesTotal != len(tt.docs) {
				t.Errorf("persisted report BatchesTotal = %d, want %d", rep.BatchesTotal, len(tt.docs))
			}
			if rep.Outcome != string(tt.wantOutcome) {
				t.Errorf("persisted report Outcome = %q, want %q", rep.Outcome, tt.wantOutcome)
			}
		})
	}
}

// scheduleRetry's past-budget branch and decideFailureAction must agree on what
// "no cap" means. With MaxAttempts <= 0 the cap is disabled, so a retry must not
// take the past-budget branch and record a failure on every attempt.
// runner.New coerces <= 0 to 5 today, so this pins the function against a future
// caller that builds an Engine without it.
func TestScheduleRetry_NoCapConfigured_DoesNotRecord(t *testing.T) {
	tests := []struct {
		name         string
		maxAttempts  int
		attempts     int
		wantRecorded bool
	}{
		{"no cap configured never reaches the past-budget branch", 0, 99, false},
		{"negative cap is also no cap", -1, 99, false},
		{"under the cap does not record", 3, 1, false},
		{"at the cap records", 3, 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &recordingStore{}
			eng := &Engine{
				cfg: EngineConfig{MaxAttempts: tt.maxAttempts},
				deps: Deps{
					Store: st, Log: &fakeLogger{}, Metrics: NopMetrics{},
					Now: time.Now, NewID: func() string { return "id" },
				},
			}
			item := &catalog.ClaimedItem{
				ID: "q1", ClaimID: "c1", CatalogID: "p/c", IndexURL: "https://x/i.json",
				FromVersion: 1, ToVersion: 2, Op: "sync", Attempts: tt.attempts - 1,
			}
			report := catalog.PassReport{BatchesAcked: 1, BatchesTotal: 2, Outcome: string(catalog.OutcomePartial)}

			eng.scheduleRetry(context.Background(), item, report, catalog.FaultTransient, "run", "pass")

			if st.rescheduled != 1 {
				t.Fatalf("rescheduled %d time(s), want 1", st.rescheduled)
			}
			if got := len(st.reports) > 0; got != tt.wantRecorded {
				t.Fatalf("recorded %d pass report(s), want recorded=%v", len(st.reports), tt.wantRecorded)
			}
		})
	}
}

// Content fixtures for the push-doc tests. baselineCorrupt is the dangerous one:
// it is a file that passes every transport check (gzip, digest, signature) and
// still cannot be read, which is exactly the case that must not settle.
const (
	baselineOK          = `{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"},"resources":[{"id":"r1"}]}`
	baselineEmpty       = `{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"},"resources":[]}`
	baselineCorrupt     = `{"id":"p/c","descriptor":{"name":"C"},"resources":[{"id":"r1"}`
	baselineNoContainer = `{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"}}`
	changeEnvelope      = `"catalog":{"id":"p/c","descriptor":{"name":"C"},"provider":{"id":"prov"}}`
)

// buildPushDoc is where content enters the sync, so it is where corrupt content
// has to be caught. The blocker it pins: a first sync with no applicable change
// files returns the baseline bytes verbatim, so an unvalidated corrupt baseline
// reaches the counting stage, counts as zero resources, and settles as a clean
// skip that advances the cursor. Corrupt content must be a permanent
// content_invalid fault instead; an EMPTY catalog is a different thing, a real
// published state, and must still resolve cleanly.
func TestBuildPushDoc_ContentIntegrity(t *testing.T) {
	files := map[string][]byte{
		"v2": []byte(`{"catalogId":"p/c","fromVersion":1,"toVersion":2,` + changeEnvelope + `,"resources":{"upserts":[{"id":"r2"}]},"offers":{}}`),
		"v3": []byte(`{"catalogId":"p/c","fromVersion":2,"toVersion":3,` + changeEnvelope + `,"resources":{"upserts":[{"id":"r3"}]},"offers":{}}`),
	}
	allChanges := []catalog.FileEntry{{Version: 2, URL: "v2"}, {Version: 3, URL: "v3"}}

	tests := []struct {
		name        string
		mergeOnly   bool
		fromVersion int64
		changes     []catalog.FileEntry
		baseline    string
		wantFault   catalog.FaultClass // "" => must resolve
		wantMode    string
		wantResIDs  []string
	}{
		{
			name: "first sync with a readable baseline resolves", mergeOnly: true,
			fromVersion: 0, baseline: baselineOK,
			wantMode: publish.UpdateModeMerge, wantResIDs: []string{"r1", "r2", "r3"}, changes: allChanges,
		},
		{
			name: "first sync with an empty baseline resolves (a real empty catalog)", mergeOnly: true,
			fromVersion: 0, baseline: baselineEmpty,
			wantMode: publish.UpdateModeMerge, wantResIDs: []string{"r2", "r3"}, changes: allChanges,
		},
		{
			name:      "first sync with a corrupt baseline and no change files is a permanent content fault",
			mergeOnly: true, fromVersion: 0, baseline: baselineCorrupt, changes: nil,
			wantFault: catalog.FaultContentInvalid,
		},
		{
			name:      "first sync with a corrupt baseline and change files is still a content fault",
			mergeOnly: true, fromVersion: 0, baseline: baselineCorrupt, changes: allChanges,
			wantFault: catalog.FaultContentInvalid,
		},
		{
			name:      "first sync with a baseline that has no resources or offers container is a content fault",
			mergeOnly: true, fromVersion: 0, baseline: baselineNoContainer, changes: nil,
			wantFault: catalog.FaultContentInvalid,
		},
		{
			name:      "the mode-by-changeset path also rejects a corrupt baseline",
			mergeOnly: false, fromVersion: 0, baseline: baselineCorrupt, changes: allChanges,
			wantFault: catalog.FaultContentInvalid,
		},
		{
			name: "incremental delta composes a contiguous change set", mergeOnly: true,
			fromVersion: 1, baseline: baselineOK, changes: allChanges,
			wantMode: publish.UpdateModeMerge, wantResIDs: []string{"r2", "r3"},
		},
		{
			name: "incremental delta with a missing intermediate version is a gap", mergeOnly: true,
			fromVersion: 1, baseline: baselineOK, changes: []catalog.FileEntry{{Version: 3, URL: "v3"}},
			wantFault: catalog.FaultGap,
		},
		{
			name: "incremental delta with an unpublished placeholder mid-range is a gap", mergeOnly: true,
			fromVersion: 1, baseline: baselineOK,
			changes:   []catalog.FileEntry{{Version: 2, URL: ""}, {Version: 3, URL: "v3"}},
			wantFault: catalog.FaultGap,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := catalog.CatalogEntry{
				CatalogID: "p/c",
				Baseline:  catalog.FileEntry{Version: 1, URL: "base", Digest: "d"},
				Changes:   tt.changes,
			}
			fetch := func(f catalog.FileEntry) ([]byte, error) {
				if f.URL == "base" {
					return []byte(tt.baseline), nil
				}
				return files[f.URL], nil
			}
			item := &catalog.ClaimedItem{
				ID: "q1", ClaimID: "c1", CatalogID: "p/c", IndexURL: "https://x/i.json",
				FromVersion: tt.fromVersion, ToVersion: 3, Op: "sync",
			}
			eng := &Engine{cfg: EngineConfig{MergeOnly: tt.mergeOnly}}

			doc, mode, _, err := eng.buildPushDoc(entry, item, fetch)

			if tt.wantFault == "" {
				if err != nil {
					t.Fatalf("buildPushDoc error = %v, want nil", err)
				}
				if mode != tt.wantMode {
					t.Errorf("mode = %q, want %q", mode, tt.wantMode)
				}
				if ids := resourceIDs(t, doc); !slices.Equal(ids, tt.wantResIDs) {
					t.Errorf("resources = %v, want %v", ids, tt.wantResIDs)
				}
				return
			}
			if err == nil {
				t.Fatalf("corrupt content resolved without error; doc = %s", doc)
			}
			if !catalog.IsPermanent(err) {
				t.Fatalf("content fault must be permanent (park, not retry), got %v", err)
			}
			if got := catalog.ClassifyFault(0, err); got != tt.wantFault {
				t.Fatalf("fault class = %q, want %q (err: %v)", got, tt.wantFault, err)
			}
		})
	}
}

// buildPushDoc must carry the index entry's isActive through to the pushed
// doc, across both the MergeOnly delta path and the full-resolve path -- and
// leave it unset (not forced true) when the entry never stamped one, so
// Discovery's own schema default applies instead of the crawler inventing one.
func TestBuildPushDoc_StampsIsActive(t *testing.T) {
	fetch := func(f catalog.FileEntry) ([]byte, error) {
		if f.URL == "base" {
			return []byte(baselineOK), nil
		}
		return nil, nil
	}
	paused := false
	active := true

	tests := []struct {
		name        string
		mergeOnly   bool
		fromVersion int64
		isActive    *bool
	}{
		{name: "first sync (full resolve), paused", mergeOnly: true, fromVersion: 0, isActive: &paused},
		{name: "first sync (full resolve), active", mergeOnly: true, fromVersion: 0, isActive: &active},
		{name: "first sync (full resolve), unset", mergeOnly: true, fromVersion: 0, isActive: nil},
		{name: "incremental delta, paused", mergeOnly: true, fromVersion: 1, isActive: &paused},
		{name: "mode-by-changeset full resolve, paused", mergeOnly: false, fromVersion: 0, isActive: &paused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := catalog.CatalogEntry{
				CatalogID: "p/c",
				Baseline:  catalog.FileEntry{Version: 1, URL: "base", Digest: "d"},
				IsActive:  tt.isActive,
			}
			item := &catalog.ClaimedItem{
				ID: "q1", ClaimID: "c1", CatalogID: "p/c", IndexURL: "https://x/i.json",
				FromVersion: tt.fromVersion, ToVersion: 1, Op: "sync",
			}
			eng := &Engine{cfg: EngineConfig{MergeOnly: tt.mergeOnly}}

			doc, _, _, err := eng.buildPushDoc(entry, item, fetch)
			if err != nil {
				t.Fatalf("buildPushDoc error = %v", err)
			}
			var got struct {
				IsActive *bool `json:"isActive"`
			}
			if err := json.Unmarshal(doc, &got); err != nil {
				t.Fatalf("parsing doc: %v", err)
			}
			if tt.isActive == nil {
				if got.IsActive != nil {
					t.Fatalf("isActive = %v, want omitted", *got.IsActive)
				}
				return
			}
			if got.IsActive == nil || *got.IsActive != *tt.isActive {
				t.Fatalf("isActive = %v, want %v", got.IsActive, *tt.isActive)
			}
		})
	}
}

// verifyContent is the last gate before a pass settles and the cursor advances,
// and it decides on counts that publish.DocCounts produces best-effort — a doc
// that will not parse counts as zero. So a zero count alone must not settle:
// corrupt content parks as a permanent content fault, while a doc that genuinely
// carries an empty catalog still skips cleanly and advances. The recorded reason
// has to say which of the two it was.
func TestVerifyContent_ZeroCountGate(t *testing.T) {
	tests := []struct {
		name        string
		doc         string
		cs          catalog.Changeset
		wantOutcome catalog.SyncOutcome
		wantStop    bool
		wantSettled bool
		wantParked  bool
		wantReason  string
	}{
		{
			name: "a doc with resources continues down the pipeline",
			doc:  baselineOK, wantStop: false,
		},
		{
			name: "an empty catalog skips cleanly and advances the cursor",
			doc:  baselineEmpty, wantOutcome: catalog.OutcomeSkipped, wantStop: true,
			wantSettled: true, wantReason: "catalog carries no resources or offers at this version",
		},
		{
			name: "a removal-only change still reports removals as the reason",
			doc:  baselineEmpty, cs: catalog.Changeset{RemovedResources: 2, HasRemovals: true},
			wantOutcome: catalog.OutcomeSkipped, wantStop: true,
			wantSettled: true, wantReason: "no upserts (removals deferred)",
		},
		{
			name: "a corrupt doc parks instead of settling as a skip",
			doc:  baselineCorrupt, wantOutcome: catalog.OutcomeFaulted, wantStop: true,
			wantParked: true,
		},
		{
			name: "a doc with no resources or offers container parks",
			doc:  baselineNoContainer, wantOutcome: catalog.OutcomeFaulted, wantStop: true,
			wantParked: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &recordingStore{}
			eng := New(EngineConfig{
				MaxAttempts: 3, IndexInterval: time.Hour, CatalogInterval: time.Hour,
			}, Deps{
				Store:   st,
				Log:     &fakeLogger{},
				Metrics: NopMetrics{},
				NewID:   func() string { return "id" },
			})
			s := &syncState{
				item: &catalog.ClaimedItem{
					ID: "q1", ClaimID: "c1", CatalogID: "p/c", IndexURL: "https://x/i.json",
					FromVersion: 1, ToVersion: 2, Op: "sync",
				},
				runID: "run", passID: "pass",
				nodeID: "p", mode: publish.UpdateModeMerge,
				pushDoc: []byte(tt.doc), cs: tt.cs,
			}

			outcome, stop := eng.verifyContent(context.Background(), s)

			if stop != tt.wantStop {
				t.Fatalf("stop = %v, want %v", stop, tt.wantStop)
			}
			if outcome != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", outcome, tt.wantOutcome)
			}
			if got := len(st.completed) > 0; got != tt.wantSettled {
				t.Fatalf("settled = %v (completed %d), want %v; corrupt content must never advance the cursor",
					got, len(st.completed), tt.wantSettled)
			}
			if got := st.parked > 0; got != tt.wantParked {
				t.Fatalf("parked = %v, want %v", got, tt.wantParked)
			}
			if tt.wantSettled {
				c := st.completed[0]
				if c.toVersion != s.item.ToVersion {
					t.Errorf("settled at version %d, want %d", c.toVersion, s.item.ToVersion)
				}
				if c.state.Report.Reason != tt.wantReason {
					t.Errorf("skip reason = %q, want %q", c.state.Report.Reason, tt.wantReason)
				}
				if c.state.Report.Outcome != string(catalog.OutcomeSkipped) {
					t.Errorf("settled outcome = %q, want %q", c.state.Report.Outcome, catalog.OutcomeSkipped)
				}
			}
			if !tt.wantParked {
				return
			}
			if len(st.reports) != 1 {
				t.Fatalf("recorded %d failure report(s), want 1 (a parked catalog must be queryable)", len(st.reports))
			}
			if !strings.Contains(st.reports[0].Reason, "verify:") {
				t.Errorf("failure reason = %q, want it to name the verify stage", st.reports[0].Reason)
			}
		})
	}
}

// stepPhrase maps each fault class to the "couldn't <…>" clause the failure
// message is built from (see the Logs section of the crawler plugin README,
// pkg/plugin/implementation/crawler/README.md, under `sync`, job 2).
func TestStepPhrase(t *testing.T) {
	cases := map[string]string{
		"index_fetch":     "resolve the catalog",
		"absent":          "resolve the catalog",
		"decode":          "unpack the files",
		"gap":             "unpack the files",
		"digest_mismatch": "verify the downloaded files",
		// oversize said "batch the catalog" while no code path produced the class.
		// The fetch layer now raises it for the download cap, so the phrase has to
		// name that stage; a batch Discovery rejects arrives as a 413 =>
		// push_rejected. A decompression bomb stays `decode` ("unpack the files").
		"oversize":        "download the files",
		"ssrf":            "download the files",
		"content_invalid": "build the push request",
		"push_schema":     "send the catalog to Discovery",
		"push_rejected":   "send the catalog to Discovery",
		"transient":       "send the catalog to Discovery",
		"store":           "save progress",
	}
	for fault, want := range cases {
		if got := stepPhrase(fault); got != want {
			t.Errorf("stepPhrase(%q) = %q, want %q", fault, got, want)
		}
	}
}
