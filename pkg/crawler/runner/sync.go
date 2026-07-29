package runner

// sync.go — the SYNC job: drain the work queue, and for each claimed catalog
// run the Catalog Sync as a pipeline of named stages
// (resolve → pull+unpack → verify → scope → batch → push → settle), or a
// retire. It owns the retry/park routing (routeFailure → parkPermanently /
// scheduleRetry) and settles into a terminal SyncOutcome. The log vocabulary
// itself lives in telemetry.go.
//
// The whole per-catalog recipe is the list in syncCatalog — read it top to
// bottom. Each stage is a method with the same signature, so a new step is one
// line in that list.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/crawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/crawler/store"
)

// pushEnvelopeReserve is headroom subtracted from the push-body cap to cover the
// fixed /push envelope (context + one publishDirective) that wraps each batch's
// catalog doc. visibleTo is accounted for separately at the call site.
const pushEnvelopeReserve = 4 << 10

// catalogPass drains the queue: claim -> handle -> repeat until empty. It mints
// one run_id per tick and emits the sync finished summary only when there was
// work (an empty queue is silent, so the CatalogInterval never spams logs).
func (e *Engine) catalogPass(ctx context.Context) {
	runID := e.newID()
	start := e.deps.Now()
	var synced, skipped, failed, retrying int
	started := false
	for {
		if ctx.Err() != nil {
			return
		}
		item, err := e.deps.Store.ClaimNext(ctx)
		if err != nil {
			// Return, don't break: the tail below marks the pass successful, and a
			// queue we can't claim from is exactly the wedged state that
			// crawler_seconds_since_last_success must keep reporting. Same rule as
			// indexPass on a Source failure. (A drained queue still breaks — an
			// idle tick is a successful one.)
			e.storeUnhealthy("sync", runID, "claim", "", err)
			return
		}
		if item == nil {
			break // queue drained
		}
		started = true
		switch e.handleQueueItem(ctx, item, runID) {
		case catalog.OutcomePushed, catalog.OutcomeRetired:
			synced++
		case catalog.OutcomeSkipped:
			skipped++
		case catalog.OutcomePartial:
			retrying++ // partial push is a retry-in-progress, not a permanent fault
		default: // faulted
			failed++
		}
	}
	depthAfter, derr := e.deps.Store.QueueDepth(ctx)
	if derr == nil {
		e.deps.Metrics.SetQueueDepth(depthAfter)
	}
	if n, err := e.deps.Store.CountParked(ctx); err == nil {
		e.deps.Metrics.SetCatalogsParked(n)
	}
	if n, err := e.deps.Store.CountTracked(ctx); err == nil {
		e.deps.Metrics.SetCatalogsTracked(n)
	}
	e.deps.Metrics.MarkPassSuccess("sync")
	if started {
		e.logSyncFinished(runID, synced, skipped, failed, retrying, depthAfter, e.deps.Now().Sub(start))
	}
}

// handleQueueItem dispatches a claimed item: a retire settles immediately; a
// sync goes through the full resolve+push flow. It mints one pass_id for the
// claimed item and returns the terminal SyncOutcome so the pass can tally.
func (e *Engine) handleQueueItem(ctx context.Context, item *store.ClaimedItem, runID string) catalog.SyncOutcome {
	passID := e.newID()
	if item.Op == "retire" {
		if err := e.settle(ctx, item, "", catalog.CatalogRetired, store.PassReport{
			At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
			Outcome: string(catalog.OutcomeRetired),
		}); err != nil {
			e.storeUnhealthy("sync", runID, "complete", item.CatalogID, err)
			return catalog.OutcomeFaulted
		}
		e.deps.Metrics.RecordSyncOutcome(string(catalog.OutcomeRetired), "")
		e.logRetired(runID, passID, item)
		return catalog.OutcomeRetired
	}
	return e.syncCatalog(ctx, item, runID, passID)
}

// syncState carries the values a Catalog Sync accumulates as it moves through
// the pipeline stages, so each stage has one shared object to read from and
// write to instead of threading a dozen return values. Populated left-to-right:
// resolveEntry sets entry/participantID, fetchContent sets pushDoc/mode/cs, etc.
type syncState struct {
	item          *store.ClaimedItem
	runID, passID string

	entry         catalog.CatalogEntry
	participantID string
	pushDoc       []byte
	mode          string
	cs            catalog.Changeset
	resCount      int
	offCount      int
	visibleTo     []string
	batches       []publish.CatalogBatch
	outcomes      []publish.BatchOutcome
	acked         int
}

// stageFn is one step in the Catalog Sync pipeline. It does the step against the
// shared state and returns (outcome, stop): stop=true ends the sync here —
// either a terminal success/skip, or a failure the step already routed
// (park/retry). The pipeline literal keeps each step's human label as an inline
// comment so the recipe still reads top-to-bottom.
type stageFn func(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool)

// syncCatalog runs ONE claimed catalog through the sync pipeline and returns its
// terminal SyncOutcome. This list is the whole per-catalog recipe — read it top
// to bottom. To add a step (e.g. §9a content-schema validation), insert one line
// (e.validateContent /* validate content schema */).
func (e *Engine) syncCatalog(ctx context.Context, item *store.ClaimedItem, runID, passID string) catalog.SyncOutcome {
	s := &syncState{item: item, runID: runID, passID: passID}
	e.logSyncing(runID, passID, item)

	pipeline := []stageFn{
		e.resolveEntry,      // fetch index & find catalog
		e.fetchContent,      // pull & unpack files
		e.verifyContent,     // verify digests & decide upserts
		e.resolveVisibility, // resolve who may see it (visibleTo)
		e.batch,             // batch by size cap
		e.publish,           // push each batch to Discovery
		e.complete,          // record & advance cursor
	}
	return e.runPipeline(ctx, s, pipeline)
}

// runPipeline walks the stages in order; a stage returning stop=true ends the
// sync with its outcome.
func (e *Engine) runPipeline(ctx context.Context, s *syncState, stages []stageFn) catalog.SyncOutcome {
	for _, st := range stages {
		if outcome, stop := st(ctx, s); stop {
			return outcome
		}
	}
	return catalog.OutcomePushed
}

// --- the pipeline stages ----------------------------------------------------

// resolveEntry (resolving): fetch the index and find this catalog's entry. The
// sync always needs the body to resolve the entry, so it fetches unconditionally
// (empty cond) rather than risking a 304.
func (e *Engine) resolveEntry(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool) {
	res, err := e.deps.FetchIndex(ctx, s.item.IndexURL, catalog.IndexConditions{})
	if err != nil {
		e.routeFailure(ctx, s.item, e.newFailureReport(s.item, 0, "index_fetch: "+err.Error()), err, s.runID, s.passID)
		return catalog.OutcomeFaulted, true
	}
	entry, ok := catalog.FindCatalog(res.Index, s.item.CatalogID)
	if !ok {
		e.parkPermanently(ctx, s.item, e.newFailureReport(s.item, 0, "catalog_absent_from_index"), catalog.FaultAbsent, s.runID, s.passID)
		return catalog.OutcomeFaulted, true
	}
	s.entry = entry
	s.participantID = res.Index.ParticipantID
	return "", false
}

// fetchContent (resolving): pull the baseline/change files and unpack them into
// the push doc + updateMode. Each file's digest is verified inside FetchFile as
// it is pulled.
func (e *Engine) fetchContent(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool) {
	fetch := func(f catalog.FileEntry) ([]byte, error) { return e.deps.FetchFile(ctx, f) }
	pushDoc, mode, cs, err := e.buildPushDoc(s.entry, s.item, fetch)
	if err != nil {
		e.routeFailure(ctx, s.item, e.newFailureReport(s.item, 0, "resolve: "+err.Error()), err, s.runID, s.passID)
		return catalog.OutcomeFaulted, true
	}
	s.pushDoc, s.mode, s.cs = pushDoc, mode, cs
	return "", false
}

// verifyContent (verifying): digests were already checked during the pull; this
// stage records deferred removals and decides whether there is anything to push.
// Nothing to upsert (e.g. a removal-only change while removals are deferred) is a
// clean skip — the cursor still advances so it isn't re-processed.
func (e *Engine) verifyContent(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool) {
	s.resCount, s.offCount = publish.DocCounts(s.pushDoc)
	if s.resCount == 0 && s.offCount == 0 {
		e.completeSkipped(ctx, s.item, s.participantID, s.mode, "no upserts (removals deferred)", s.runID, s.passID)
		return catalog.OutcomeSkipped, true
	}
	return "", false
}

// resolveVisibility (scoping): resolve who this catalog is visible to (handed to
// Discovery as visibleTo). Membership — whether we carry this catalog at all —
// was already decided upstream at enqueue time in the crawl job's decideCatalog,
// so ResolveScope's take flag is discarded here and only visibleTo is kept.
func (e *Engine) resolveVisibility(_ context.Context, s *syncState) (catalog.SyncOutcome, bool) {
	_, visibleTo := catalog.ResolveScope(s.entry, e.cfg.Networks)
	s.visibleTo = visibleTo
	return "", false
}

// batch (scoping): split the push doc by serialized SIZE so no /push body
// exceeds Discovery's cap. The doc budget is the body cap minus headroom for the
// envelope (context + directive + visibleTo) that BuildPushBody wraps around
// each batch's doc.
func (e *Engine) batch(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool) {
	docBudget := e.cfg.MaxPushBytes - pushEnvelopeReserve
	if vb, err := json.Marshal(s.visibleTo); err == nil {
		docBudget -= int64(len(vb))
	}
	if docBudget < 1 {
		docBudget = 1 // still emit ≥1 resource per batch; an oversize single resource is unavoidable
	}
	batches, err := publish.BatchCatalog(s.pushDoc, docBudget, s.mode)
	if err != nil {
		e.parkPermanently(ctx, s.item, e.newFailureReport(s.item, 0, "batch: "+err.Error()), catalog.FaultOversize, s.runID, s.passID)
		return catalog.OutcomeFaulted, true
	}
	s.batches = batches
	return "", false
}

// publish (publishing): build, validate and push each batch to Discovery,
// stopping at the first un-acked batch (don't send later MERGE batches after a
// failed FULL). A non-OK rollup is routed as a failure.
func (e *Engine) publish(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool) {
	start := e.deps.Now()
	var outcomes []publish.BatchOutcome
	for _, b := range s.batches {
		body, err := publish.BuildPushBody(publish.PushMeta{
			ParticipantID: s.participantID, BppURI: e.cfg.BppURI,
			MessageID: e.newID(), TransactionID: e.newID(),
			Timestamp:  e.deps.Now().UTC().Format(time.RFC3339),
			UpdateMode: b.UpdateMode, CatalogType: s.entry.CatalogType,
			VisibleTo: s.visibleTo,
		}, b.Doc)
		if err != nil {
			e.parkPermanently(ctx, s.item, e.newFailureReport(s.item, 0, "build_push: "+err.Error()), catalog.FaultContentInvalid, s.runID, s.passID)
			return catalog.OutcomeFaulted, true
		}
		if e.deps.Validate != nil {
			if err := e.deps.Validate(ctx, body); err != nil {
				e.parkPermanently(ctx, s.item, e.newFailureReport(s.item, 0, "schema: "+err.Error()), catalog.FaultPushSchema, s.runID, s.passID)
				return catalog.OutcomeFaulted, true
			}
		}
		out, err := e.deps.Push(ctx, body)
		if err != nil {
			out = publish.BatchOutcome{HTTPStatus: out.HTTPStatus, Reason: err.Error()}
		}
		outcomes = append(outcomes, out)
		if !out.Acked {
			break
		}
	}
	e.deps.Metrics.ObservePushSeconds(e.deps.Now().Sub(start).Seconds())

	s.outcomes = outcomes
	status, failed, acked := publish.Rollup(outcomes)
	s.acked = acked
	if status != publish.SyncOK {
		httpStatus, reason := 0, "push failed"
		if len(failed) > 0 {
			httpStatus, reason = failed[0].HTTPStatus, failed[0].Reason
		}
		e.routeFailure(ctx, s.item, store.PassReport{
			At: e.deps.Now().UTC(), FromVersion: s.item.FromVersion, ToVersion: s.item.ToVersion,
			Mode: s.mode, Resources: s.resCount, Offers: s.offCount,
			Removals:     s.cs.RemovedResources + s.cs.RemovedOffers,
			BatchesAcked: s.acked, BatchesTotal: len(s.batches),
			Outcome: string(classifyOutcome(httpStatus, s.acked, nil)), HTTPStatus: httpStatus, Reason: "push: " + reason,
		}, nil, s.runID, s.passID)
		return classifyOutcome(httpStatus, s.acked, nil), true
	}
	return "", false
}

// complete (publishing): settle a fully-acked sync — advance the cursor, record
// the pushed pass report, and emit the success terminal.
func (e *Engine) complete(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool) {
	if err := e.settle(ctx, s.item, s.participantID, catalog.CatalogActive, store.PassReport{
		At: e.deps.Now().UTC(), FromVersion: s.item.FromVersion, ToVersion: s.item.ToVersion,
		Mode: s.mode, Resources: s.resCount, Offers: s.offCount,
		Removals:     s.cs.RemovedResources + s.cs.RemovedOffers,
		BatchesAcked: s.acked, BatchesTotal: len(s.batches),
		Outcome: string(catalog.OutcomePushed), HTTPStatus: s.outcomes[len(s.outcomes)-1].HTTPStatus,
	}); err != nil {
		e.storeUnhealthy("sync", s.runID, "complete", s.item.CatalogID, err)
		return catalog.OutcomeFaulted, true
	}
	e.deps.Metrics.RecordSyncOutcome(string(catalog.OutcomePushed), "")
	e.deps.Metrics.ObserveSyncLagSeconds(e.deps.Now().Sub(s.item.EnqueuedAt).Seconds())
	e.logSynced(s.runID, s.passID, s.item, s.mode, s.resCount, s.offCount, len(s.outcomes))
	return catalog.OutcomePushed, true
}

// --- shared helpers used by the stages --------------------------------------

// buildPushDoc produces the push doc + updateMode for a claimed catalog.
//
// This version (MergeOnly) always MERGEs: an incremental update is built as a
// delta straight from the change files (no baseline fetch) when they carry the
// catalog metadata envelope; a first sync — or a change set without that
// envelope — falls back to a full resolve, still pushed as MERGE. Removals are
// recorded in the changeset but not applied (deferred).
//
// With MergeOnly=false the dormant mode-by-changeset path runs: only-upserts ->
// MERGE (just the changed resources); any removal / new / re-baseline -> FULL.
func (e *Engine) buildPushDoc(entry catalog.CatalogEntry, item *store.ClaimedItem, fetch catalog.FetchFunc) ([]byte, string, catalog.Changeset, error) {
	if !e.cfg.MergeOnly {
		full, cs, err := catalog.ResolveWithChangeset(entry, item.FromVersion, item.FromVersion > 0, item.ToVersion, fetch)
		if err != nil {
			return nil, "", cs, err
		}
		if cs.FromBaseline || cs.HasRemovals {
			return full, publish.UpdateModeFull, cs, nil
		}
		filtered, err := catalog.FilterCatalog(full, cs.UpsertedResources, cs.UpsertedOffers)
		return filtered, publish.UpdateModeMerge, cs, err
	}

	// Incremental update: try the delta straight from the change files (no
	// baseline fetch). firstSync (cursor behind the baseline) can't — the
	// baseline is the only content — so it falls through to the full resolve.
	if item.FromVersion >= entry.Baseline.Version {
		delta, cs, ok, err := catalog.ResolveDelta(entry, item.FromVersion, item.ToVersion, fetch)
		if err != nil {
			return nil, "", cs, err
		}
		if !ok {
			// The change file(s) carry no catalog metadata envelope. An incremental
			// MERGE requires it (id/descriptor/provider), so a missing envelope is a
			// malformed change file — NOT a reason to re-download the baseline. Park
			// it as a permanent content fault until the publisher republishes a
			// compliant change file.
			return nil, "", cs, catalog.PermanentFaultf(catalog.FaultContentInvalid,
				"crawler: change file(s) for %s carry no catalog metadata envelope (required for an incremental MERGE)", entry.CatalogID)
		}
		return delta, publish.UpdateModeMerge, cs, nil
	}
	// First sync (cursor behind the baseline): the baseline is the only content,
	// so it is fetched here — this is not a fallback, it is the initial resolve.
	full, cs, err := catalog.ResolveWithChangeset(entry, item.FromVersion, item.FromVersion > 0, item.ToVersion, fetch)
	return full, publish.UpdateModeMerge, cs, err
}

// settle builds the common CatalogState envelope and calls Store.Complete for a
// terminal outcome (pushed / skipped / retired): the cursor advances to the
// item's ToVersion and the pass report is appended. It is the single write path
// shared by the three terminal sites — the caller handles the returned error
// (all currently via storeUnhealthy("complete")).
func (e *Engine) settle(ctx context.Context, item *store.ClaimedItem, participantID string, status catalog.CatalogStatus, report store.PassReport) error {
	return e.deps.Store.Complete(ctx, item.ID, item.ClaimID, item.ToVersion, store.CatalogState{
		CatalogID: item.CatalogID, IndexURL: item.IndexURL, ParticipantID: participantID,
		Version: item.ToVersion, Status: string(status), Report: report,
	})
}

// completeSkipped settles a claimed item that had nothing to push (e.g. a
// removal-only change while removals are deferred): the cursor still advances
// and a skipped pass report is recorded, so it isn't re-processed.
func (e *Engine) completeSkipped(ctx context.Context, item *store.ClaimedItem, participantID, mode, reason, runID, passID string) {
	rep := store.PassReport{
		At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
		Mode: mode, Outcome: string(catalog.OutcomeSkipped), Reason: reason,
	}
	if err := e.settle(ctx, item, participantID, catalog.CatalogActive, rep); err != nil {
		e.storeUnhealthy("sync", runID, "complete", item.CatalogID, err)
		return
	}
	e.deps.Metrics.RecordSyncOutcome(string(catalog.OutcomeSkipped), "")
	e.logSkipped(runID, passID, item, reason)
}

// routeFailure routes a failure: permanent (won't fix on retry) faults and 4xx
// rejections are parked; everything else is a transient retry. Either way the
// pass report is appended to the catalog's push_status history. The
// permanent-vs-transient axis is a single typed FaultClass (§6b).
func (e *Engine) routeFailure(ctx context.Context, item *store.ClaimedItem, report store.PassReport, err error, runID, passID string) {
	fc := catalog.ClassifyFault(report.HTTPStatus, err)
	if fc.Permanent() {
		e.parkPermanently(ctx, item, report, fc, runID, passID)
		return
	}
	e.scheduleRetry(ctx, item, report, fc, runID, passID)
}

// newFailureReport builds the minimal pass report for a pre-push failure (no
// push counts): the outcome is derived from the HTTP status.
func (e *Engine) newFailureReport(item *store.ClaimedItem, httpStatus int, reason string) store.PassReport {
	return store.PassReport{
		At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
		Outcome: string(classifyOutcome(httpStatus, 0, nil)), HTTPStatus: httpStatus, Reason: reason,
	}
}

// parkPermanently parks a permanently-failed item (no hot retry; re-activates on
// a version bump), appends the pass report WITHOUT advancing the cursor, counts
// it, and logs at error level (the operator alert). "Too big" / "corrupt" /
// "unsupported encoding" becomes visible and actionable, never silently lost.
func (e *Engine) parkPermanently(ctx context.Context, item *store.ClaimedItem, report store.PassReport, fc catalog.FaultClass, runID, passID string) {
	if err := e.deps.Store.ParkQueueItem(ctx, item.ID, item.ClaimID); err != nil {
		e.storeUnhealthy("sync", runID, "park", item.CatalogID, err)
	}
	if err := e.deps.Store.RecordFailure(ctx, item.CatalogID, item.IndexURL, "", report); err != nil {
		e.storeUnhealthy("sync", runID, "record", item.CatalogID, err)
	}
	e.deps.Metrics.RecordSyncOutcome(report.Outcome, fc.String())
	// Parked: won't retry until the publisher publishes a new version (ERROR).
	e.logFailed(runID, passID, item, fc, report, false, item.Attempts+1)
}

// scheduleRetry retries with capped backoff, and appends the pass report once
// MaxAttempts is reached. The cursor is NEVER advanced on failure (only
// Complete advances it), so the item keeps retrying until it succeeds.
func (e *Engine) scheduleRetry(ctx context.Context, item *store.ClaimedItem, report store.PassReport, fc catalog.FaultClass, runID, passID string) {
	attempts := item.Attempts + 1
	next := e.deps.Now().Add(Backoff(attempts))
	if err := e.deps.Store.RescheduleQueueItem(ctx, item.ID, item.ClaimID, next); err != nil {
		e.storeUnhealthy("sync", runID, "reschedule", item.CatalogID, err)
	}
	if attempts >= e.cfg.MaxAttempts {
		// Past MaxAttempts: record the failure (queryable) WITHOUT advancing the
		// version, and keep the item queued — it still retries with backoff; the
		// cap only decides when the failure gets recorded, it does not park it.
		if err := e.deps.Store.RecordFailure(ctx, item.CatalogID, item.IndexURL, "", report); err != nil {
			e.storeUnhealthy("sync", runID, "record", item.CatalogID, err)
		}
		e.deps.Metrics.RecordSyncOutcome(report.Outcome, fc.String())
		// Still a retry — the item was rescheduled above; MaxAttempts only gates
		// when the failure is recorded, it does not park the item (WARN, not ERROR).
		e.logFailed(runID, passID, item, fc, report, true, attempts)
		return
	}
	// Transient fault, will retry with backoff (WARN).
	e.logFailed(runID, passID, item, fc, report, true, attempts)
}

// classifyOutcome is the ONE place the push-outcome rule lives (it replaced the
// duplicated logic at the old engine fail sites): a 4xx push rejection or any
// error is `faulted` (the FaultClass — push_rejected for a 4xx — is decided
// separately by catalog.ClassifyFault and drives park-vs-retry); a push that
// acked some batches but not all is `partial`; anything else (5xx / all
// unacked) is `faulted`. Success (all acked) and skipped are decided by their
// own sites, not here.
func classifyOutcome(httpStatus, ackedBatches int, err error) catalog.SyncOutcome {
	switch {
	case httpStatus >= 400 && httpStatus < 500:
		return catalog.OutcomeFaulted
	case err != nil:
		return catalog.OutcomeFaulted
	case ackedBatches > 0:
		return catalog.OutcomePartial
	default:
		return catalog.OutcomeFaulted
	}
}
