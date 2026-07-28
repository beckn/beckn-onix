package catalogcrawler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/state"
	"github.com/beckn-one/beckn-onix/pkg/catalogfile"
)

// Logger is the minimal structured-log sink the engine needs. It is
// injected (no process-global logger), so an onix plugin passes its own.
type Logger interface {
	Info(event string, kv ...any)
	Warn(event string, kv ...any)
	Error(event string, kv ...any)
}

// NopLogger discards all events (handy default / for tests).
type NopLogger struct{}

func (NopLogger) Info(string, ...any)  {}
func (NopLogger) Warn(string, ...any)  {}
func (NopLogger) Error(string, ...any) {}

// IndexCond carries the conditional-GET validators from the last successful
// index fetch. Both empty means an unconditional GET (the host sent none, or
// we've never fetched this index).
type IndexCond struct {
	ETag         string
	LastModified string
}

// IndexResult is one index fetch. NotModified is true when the host answered
// 304 (the index is unchanged and Index is zero — skip it). ETag/LastModified
// are the validators to store for next time (echoed back on a 304).
type IndexResult struct {
	Index        Index
	NotModified  bool
	ETag         string
	LastModified string
}

// IndexFetcher fetches and parses a publisher's catalog index, sending cond as
// If-None-Match / If-Modified-Since so an unchanged index can answer 304.
type IndexFetcher func(ctx context.Context, indexURL string, cond IndexCond) (IndexResult, error)

// Validator schema-validates the /push request body before it is sent
// (Phase 1; reuses onix's schemav2validator against the catalog/publish
// action, which keys on the request path). A nil error means valid.
type Validator func(ctx context.Context, pushBody []byte) error

// Pusher pushes one /push body to Discovery and reports the outcome.
type Pusher func(ctx context.Context, body []byte) (PartOutcome, error)

// FileFetcher fetches + verifies one catalog file. It is context-aware (for
// real HTTP); Resolve wraps it into a ctx-free FetchFunc per catalog.
type FileFetcher func(ctx context.Context, f FileEntry) ([]byte, error)

// Config is the engine's tunables (all config-driven, no hardcodes).
type Config struct {
	Networks        []string      // crawler's networkIds (selection + on-demand default)
	BppURI          string        // publisher URI for the push context
	IndexInterval   time.Duration // index-job cadence
	CatalogInterval time.Duration // catalog-job cadence
	MaxAttempts     int           // give up a catalog after this many failed pushes
	MaxPushBytes    int64         // max /push body size Discovery accepts (0 => default 10 MiB)
}

// Deps are the engine's injected collaborators.
type Deps struct {
	Store      *state.Store
	Source     Source
	FetchIndex IndexFetcher
	FetchFile  FileFetcher
	Validate   Validator
	Push       Pusher
	Log        Logger
	Metrics    Metrics
	Now        func() time.Time
	NewID      func() string
}

// Engine runs the two scheduled jobs (index + catalog) linked by the queue.
type Engine struct {
	cfg  Config
	deps Deps
	wg   sync.WaitGroup

	mu      sync.Mutex
	ctx     context.Context
	stop    context.CancelFunc
	stopped bool
}

// New builds an Engine, filling in sane defaults for optional deps.
func New(cfg Config, deps Deps) *Engine {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Log == nil {
		deps.Log = NopLogger{}
	}
	if deps.Metrics == nil {
		deps.Metrics = NopMetrics{}
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.MaxPushBytes <= 0 {
		cfg.MaxPushBytes = 10 << 20
	}
	if cfg.IndexInterval <= 0 {
		cfg.IndexInterval = 5 * time.Minute
	}
	if cfg.CatalogInterval <= 0 {
		cfg.CatalogInterval = 30 * time.Second
	}
	return &Engine{cfg: cfg, deps: deps}
}

// Start launches the index and catalog jobs as two goroutines. It returns
// immediately; Stop() drains them.
func (e *Engine) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.ctx, e.stop = ctx, cancel
	e.mu.Unlock()
	e.loop(ctx, e.cfg.IndexInterval, e.indexPass)
	e.loop(ctx, e.cfg.CatalogInterval, e.catalogPass)
	return nil
}

// Stop signals all jobs (scheduled + in-flight /crawl) and waits for them to
// drain, so a caller can safely close the DB afterwards.
func (e *Engine) Stop() error {
	e.mu.Lock()
	e.stopped = true
	stop := e.stop
	e.mu.Unlock()
	if stop != nil {
		stop()
	}
	e.wg.Wait()
	return nil
}

// CrawlNow runs an immediate index crawl for one index URL (the /crawl
// supportability trigger). It launches a tracked goroutine on the engine's own
// context, so Stop() drains it before the DB is closed.
func (e *Engine) CrawlNow(_ context.Context, indexURL string) error {
	e.mu.Lock()
	if e.stopped || e.ctx == nil {
		e.mu.Unlock()
		return fmt.Errorf("catalogcrawler: engine not running")
	}
	ctx := e.ctx
	e.wg.Add(1)
	e.mu.Unlock()

	go func() {
		defer e.wg.Done()
		e.crawlIndex(ctx, IndexRef{IndexURL: indexURL, Source: SourceOnDemand}, true)
	}()
	return nil
}

// loop runs fn once immediately, then every interval until ctx is done.
func (e *Engine) loop(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		fn(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fn(ctx)
			}
		}
	}()
}

// --- index job ---

// indexPass resolves every configured source and crawls each index.
func (e *Engine) indexPass(ctx context.Context) {
	refs, err := e.deps.Source.IndexURLs(ctx)
	if err != nil {
		e.deps.Log.Error("crawler.source.failed", "err", err)
		return
	}
	for _, ref := range refs {
		if ctx.Err() != nil {
			return
		}
		e.crawlIndex(ctx, ref, false)
	}
}

// crawlIndex fetches one index and, for each catalog, decides + enqueues.
// force bypasses the per-index next_crawl_at cadence gate (used by /crawl).
func (e *Engine) crawlIndex(ctx context.Context, ref IndexRef, force bool) {
	prev, err := e.deps.Store.GetIndex(ctx, ref.IndexURL)
	if err != nil {
		e.deps.Log.Error("crawler.index.state_failed", "indexUrl", ref.IndexURL, "err", err)
		return
	}
	now := e.deps.Now()
	if !force && prev != nil && !prev.NextCrawlAt.IsZero() && prev.NextCrawlAt.After(now) {
		return // not due yet (per-index cadence via next_crawl_at)
	}

	cond := IndexCond{}
	if prev != nil {
		cond = IndexCond{ETag: prev.ETag, LastModified: prev.LastModified}
	}
	res, err := e.deps.FetchIndex(ctx, ref.IndexURL, cond)
	if err != nil {
		e.deps.Log.Warn("crawler.index.fetch_failed", "indexUrl", ref.IndexURL, "err", err)
		return
	}
	if res.NotModified {
		// 304 Not Modified: the host confirmed nothing changed — no body
		// downloaded, no parse. Just advance the crawl cadence.
		e.deps.Log.Info("crawler.index.not_modified", "indexUrl", ref.IndexURL)
		if err := e.deps.Store.TouchIndex(ctx, ref.IndexURL, e.nextIndexCrawl()); err != nil {
			e.deps.Log.Error("crawler.index.touch_failed", "indexUrl", ref.IndexURL, "err", err)
		}
		e.deps.Metrics.ObserveIndexSeconds(e.deps.Now().Sub(now).Seconds())
		return
	}
	idx := res.Index
	e.deps.Log.Info("crawler.index.checked", "indexUrl", ref.IndexURL, "version", idx.Version)

	// Process only when the index version advanced; per-catalog cursors +
	// the queue handle any still-pending work independently.
	if prev != nil && prev.IndexVersion == idx.Version {
		e.deps.Log.Info("crawler.index.unchanged", "indexUrl", ref.IndexURL, "version", idx.Version)
	} else {
		for _, entry := range idx.Catalogs {
			take, _ := Select(entry, e.cfg.Networks)
			cursor, seen, err := e.deps.Store.GetCatalogVersion(ctx, entry.CatalogID)
			if err != nil {
				e.deps.Log.Error("crawler.catalog.state_failed", "catalogId", entry.CatalogID, "err", err)
				continue
			}
			switch d := Decide(entry, cursor, seen); d.Action {
			case ActionSync:
				if !take {
					continue // not for this crawler's networks
				}
				if err := e.deps.Store.Enqueue(ctx, state.QueueItem{
					CatalogID: entry.CatalogID, IndexURL: ref.IndexURL,
					FromVersion: cursor, ToVersion: d.ToVersion, Op: "sync",
				}); err != nil {
					e.deps.Log.Error("crawler.enqueue_failed", "catalogId", entry.CatalogID, "err", err)
					continue
				}
				e.deps.Log.Info("crawler.catalog.enqueued", "catalogId", entry.CatalogID, "toVersion", d.ToVersion)
			case ActionRetire:
				if !seen {
					continue // never had it; nothing to retire
				}
				if err := e.deps.Store.Enqueue(ctx, state.QueueItem{
					CatalogID: entry.CatalogID, IndexURL: ref.IndexURL, ToVersion: cursor, Op: "retire",
				}); err != nil {
					e.deps.Log.Error("crawler.enqueue_failed", "catalogId", entry.CatalogID, "err", err)
					continue
				}
				e.deps.Log.Info("crawler.catalog.retire_enqueued", "catalogId", entry.CatalogID)
			case ActionRollback:
				e.deps.Log.Warn("crawler.catalog.rollback", "catalogId", entry.CatalogID, "cursor", cursor, "indexVersion", d.ToVersion)
			case ActionSkipUnchanged:
				// nothing to do
			}
		}
	}

	if err := e.deps.Store.UpsertIndex(ctx, ref.IndexURL, idx.ParticipantID, ref.Source, idx.Version, SyncOK, e.nextIndexCrawl(), res.ETag, res.LastModified); err != nil {
		e.deps.Log.Error("crawler.index.record_failed", "indexUrl", ref.IndexURL, "err", err)
	}
	e.deps.Metrics.ObserveIndexSeconds(e.deps.Now().Sub(now).Seconds())
}

func (e *Engine) nextIndexCrawl() time.Time {
	if e.cfg.IndexInterval <= 0 {
		return time.Time{}
	}
	return e.deps.Now().Add(e.cfg.IndexInterval)
}

// --- catalog job ---

// catalogPass drains the queue: claim -> process -> repeat until empty.
func (e *Engine) catalogPass(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		item, err := e.deps.Store.ClaimNext(ctx)
		if err != nil {
			e.deps.Log.Error("crawler.claim_failed", "err", err)
			break
		}
		if item == nil {
			break // queue drained
		}
		e.processItem(ctx, item)
	}
	if n, err := e.deps.Store.QueueDepth(ctx); err == nil {
		e.deps.Metrics.SetQueueDepth(n)
	}
}

// processItem resolves + pushes one claimed catalog, then settles it.
func (e *Engine) processItem(ctx context.Context, item *state.ClaimedItem) {
	if item.Op == "retire" {
		if err := e.deps.Store.Complete(ctx, item.ID, item.ClaimID, item.ToVersion, state.CatalogState{
			CatalogID: item.CatalogID, IndexURL: item.IndexURL,
			Version: item.ToVersion, Status: "retired",
			Report: state.PassReport{
				At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
				Outcome: "skipped",
			},
		}); err != nil {
			e.deps.Log.Error("crawler.retire_failed", "catalogId", item.CatalogID, "err", err)
		} else {
			e.deps.Log.Info("crawler.catalog.retired", "catalogId", item.CatalogID)
		}
		return
	}

	// The catalog job always needs the body to resolve the entry, so it fetches
	// unconditionally (empty cond) rather than risking a 304.
	res, err := e.deps.FetchIndex(ctx, item.IndexURL, IndexCond{})
	if err != nil {
		e.fail(ctx, item, e.failReport(item, 0, "index_fetch: "+err.Error()), err)
		return
	}
	entry, ok := findCatalog(res.Index, item.CatalogID)
	if !ok {
		e.failPermanent(ctx, item, e.failReport(item, 0, "catalog_absent_from_index"))
		return
	}

	fetch := func(f FileEntry) ([]byte, error) { return e.deps.FetchFile(ctx, f) }
	catalog, cs, err := ResolveWithChangeset(entry, item.FromVersion, item.FromVersion > 0, item.ToVersion, fetch)
	if err != nil {
		e.fail(ctx, item, e.failReport(item, 0, "resolve: "+err.Error()), err)
		return
	}

	// Mode by changeset: only-upserts -> MERGE (push just the changed
	// resources); any removal, or a new / re-baselined catalog -> FULL
	// (push the complete catalog, the only mode Discovery deletes in).
	mode := UpdateModeMerge
	pushDoc := catalog
	if cs.FromBaseline || cs.HasRemovals {
		mode = UpdateModeFull
	} else {
		pushDoc, err = filterCatalog(catalog, cs.UpsertedResources, cs.UpsertedOffers)
		if err != nil {
			e.failPermanent(ctx, item, e.failReport(item, 0, "filter: "+err.Error()))
			return
		}
	}

	resCount, offCount := docCounts(pushDoc) // what this pass actually pushes
	_, visibleTo := Select(entry, e.cfg.Networks)
	// Batch by serialized SIZE so no /push body exceeds Discovery's cap. The doc
	// budget is the body cap minus headroom for the envelope (context + directive
	// + visibleTo) that BuildPushBody wraps around each batch's doc.
	docBudget := e.cfg.MaxPushBytes - pushEnvelopeReserve
	if vb, err := json.Marshal(visibleTo); err == nil {
		docBudget -= int64(len(vb))
	}
	if docBudget < 1 {
		docBudget = 1 // still emit ≥1 resource per batch; an oversize single resource is unavoidable
	}
	batches, err := BatchCatalog(pushDoc, docBudget, mode)
	if err != nil {
		e.failPermanent(ctx, item, e.failReport(item, 0, "batch: "+err.Error()))
		return
	}

	start := e.deps.Now()
	var outcomes []PartOutcome
	for _, batch := range batches {
		body, err := BuildPushBody(PushMeta{
			ParticipantID: res.Index.ParticipantID, BppURI: e.cfg.BppURI,
			MessageID: e.newID(), TransactionID: e.newID(),
			Timestamp:  e.deps.Now().UTC().Format(time.RFC3339),
			UpdateMode: batch.UpdateMode, CatalogType: entry.CatalogType,
			VisibleTo: visibleTo,
		}, batch.Doc)
		if err != nil {
			e.failPermanent(ctx, item, e.failReport(item, 0, "build_push: "+err.Error()))
			return
		}
		if e.deps.Validate != nil {
			if err := e.deps.Validate(ctx, body); err != nil {
				e.failPermanent(ctx, item, e.failReport(item, 0, "schema: "+err.Error()))
				return
			}
		}
		out, err := e.deps.Push(ctx, body)
		if err != nil {
			out = PartOutcome{HTTPStatus: out.HTTPStatus, Reason: err.Error()}
		}
		outcomes = append(outcomes, out)
		if !out.Acked {
			break // don't send later MERGE batches after a failed FULL
		}
	}
	e.deps.Metrics.ObservePushSeconds(e.deps.Now().Sub(start).Seconds())

	acked := ackedCount(outcomes)
	if status, failed := Rollup(outcomes); status != SyncOK {
		httpStatus, reason := 0, "push failed"
		if len(failed) > 0 {
			httpStatus, reason = failed[0].HTTPStatus, failed[0].Reason
		}
		outcome := "failed"
		if httpStatus >= 400 && httpStatus < 500 {
			outcome = "rejected"
		} else if acked > 0 {
			outcome = "partial" // some batches landed, some didn't
		}
		e.fail(ctx, item, state.PassReport{
			At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
			Mode: mode, Resources: resCount, Offers: offCount,
			Removals:     cs.RemovedResources + cs.RemovedOffers,
			BatchesAcked: acked, BatchesTotal: len(batches),
			Outcome: outcome, HTTPStatus: httpStatus, Reason: "push: " + reason,
		}, nil)
		return
	}

	if err := e.deps.Store.Complete(ctx, item.ID, item.ClaimID, item.ToVersion, state.CatalogState{
		CatalogID: item.CatalogID, IndexURL: item.IndexURL, ParticipantID: res.Index.ParticipantID,
		Version: item.ToVersion, Status: "active",
		Report: state.PassReport{
			At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
			Mode: mode, Resources: resCount, Offers: offCount,
			Removals:     cs.RemovedResources + cs.RemovedOffers,
			BatchesAcked: acked, BatchesTotal: len(batches),
			Outcome: "pushed", HTTPStatus: outcomes[len(outcomes)-1].HTTPStatus,
		},
	}); err != nil {
		e.deps.Log.Error("crawler.complete_failed", "catalogId", item.CatalogID, "err", err)
		return
	}
	e.deps.Metrics.CatalogPushed()
	e.deps.Log.Info("crawler.catalog.pushed", "catalogId", item.CatalogID, "version", item.ToVersion,
		"mode", mode, "resources", resCount, "offers", offCount, "batches", len(outcomes))
}

// fail routes a failure: permanent (won't fix on retry) errors and 4xx
// rejections are parked; everything else is a transient retry. Either way the
// pass report is appended to the catalog's push_status history.
func (e *Engine) fail(ctx context.Context, item *state.ClaimedItem, report state.PassReport, err error) {
	if IsPermanent(err) || (report.HTTPStatus >= 400 && report.HTTPStatus < 500) {
		e.failPermanent(ctx, item, report)
		return
	}
	e.failItem(ctx, item, report)
}

// failReport builds the minimal pass report for a pre-push failure (no push
// counts): the outcome is derived from the HTTP status.
func (e *Engine) failReport(item *state.ClaimedItem, httpStatus int, reason string) state.PassReport {
	outcome := "failed"
	if httpStatus >= 400 && httpStatus < 500 {
		outcome = "rejected"
	}
	return state.PassReport{
		At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
		Outcome: outcome, HTTPStatus: httpStatus, Reason: reason,
	}
}

// failPermanent parks a permanently-failed item (no hot retry; re-activates on
// a version bump), appends the pass report WITHOUT advancing the cursor, counts
// it, and logs at error level (the operator alert). "Too big" / "corrupt" /
// "unsupported encoding" becomes visible and actionable, never silently lost.
func (e *Engine) failPermanent(ctx context.Context, item *state.ClaimedItem, report state.PassReport) {
	if err := e.deps.Store.ParkQueueItem(ctx, item.ID, item.ClaimID); err != nil {
		e.deps.Log.Error("crawler.park_failed", "catalogId", item.CatalogID, "err", err)
	}
	if err := e.deps.Store.RecordFailure(ctx, item.CatalogID, item.IndexURL, "", report); err != nil {
		e.deps.Log.Error("crawler.recordfailure_failed", "catalogId", item.CatalogID, "err", err)
	}
	e.deps.Metrics.CatalogFailed(reasonCategory(report.Reason))
	e.deps.Log.Error("crawler.catalog.permanent_failure", "catalogId", item.CatalogID, "reason", report.Reason, "httpStatus", report.HTTPStatus)
}

// failItem retries with capped backoff, and appends the pass report once
// MaxAttempts is reached. The cursor is NEVER advanced on failure (only
// Complete advances it), so the item keeps retrying until it succeeds.
func (e *Engine) failItem(ctx context.Context, item *state.ClaimedItem, report state.PassReport) {
	attempts := item.Attempts + 1
	next := e.deps.Now().Add(Backoff(attempts))
	if err := e.deps.Store.FailQueueItem(ctx, item.ID, item.ClaimID, next); err != nil {
		e.deps.Log.Error("crawler.fail_failed", "catalogId", item.CatalogID, "err", err)
	}
	if attempts >= e.cfg.MaxAttempts {
		// Record the failure WITHOUT advancing the version (queryable), and keep
		// the item queued for retry.
		if err := e.deps.Store.RecordFailure(ctx, item.CatalogID, item.IndexURL, "", report); err != nil {
			e.deps.Log.Error("crawler.recordfailure_failed", "catalogId", item.CatalogID, "err", err)
		}
		e.deps.Metrics.CatalogFailed(reasonCategory(report.Reason))
		e.deps.Log.Warn("crawler.catalog.failed", "catalogId", item.CatalogID, "reason", report.Reason, "attempts", attempts, "httpStatus", report.HTTPStatus)
		return
	}
	e.deps.Log.Warn("crawler.catalog.retry", "catalogId", item.CatalogID, "reason", report.Reason, "attempts", attempts)
}

func (e *Engine) newID() string {
	if e.deps.NewID != nil {
		return e.deps.NewID()
	}
	return ""
}

// pushEnvelopeReserve is headroom subtracted from the push-body cap to cover the
// fixed /push envelope (context + one publishDirective) that wraps each batch's
// catalog doc. visibleTo is accounted for separately at the call site.
const pushEnvelopeReserve = 4 << 10

// docCounts reports how many resources and offers a pushed catalog doc carries
// (best-effort — a doc that won't parse counts as zero, never blocks the pass).
func docCounts(b []byte) (resources, offers int) {
	var d catalogfile.Doc
	if json.Unmarshal(b, &d) == nil {
		return len(d.Resources), len(d.Offers)
	}
	return 0, 0
}

// ackedCount is how many pushed batches Discovery acknowledged.
func ackedCount(outcomes []PartOutcome) int {
	n := 0
	for _, o := range outcomes {
		if o.Acked {
			n++
		}
	}
	return n
}

// reasonCategory reduces a full failure reason ("schema: ...") to a
// low-cardinality category ("schema") for the failed-total metric label.
func reasonCategory(reason string) string {
	if i := strings.IndexByte(reason, ':'); i > 0 {
		return reason[:i]
	}
	return reason
}

func findCatalog(idx Index, catalogID string) (CatalogEntry, bool) {
	for _, c := range idx.Catalogs {
		if c.CatalogID == catalogID {
			return c, true
		}
	}
	return CatalogEntry{}, false
}
