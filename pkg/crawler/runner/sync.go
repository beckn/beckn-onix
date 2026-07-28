package runner

// sync.go — the catalog job: drain the queue, and for each claimed item run
// the Catalog Sync (resolve → verify → scope → publish → settle) or a retire.
// It owns the retry/park routing (routeFailure → parkPermanently / scheduleRetry)
// and drives the SyncPhase breadcrumbs + terminal SyncOutcome. The log
// vocabulary itself lives in telemetry.go.

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
// one run_id per tick and emits the sync_pass heartbeat only when there was
// work (an empty queue is silent, so the CatalogInterval never spams logs).
func (e *Engine) catalogPass(ctx context.Context) {
	runID := e.newID()
	start := e.deps.Now()
	var synced, skipped, dropped, faulted int
	started := false
	for {
		if ctx.Err() != nil {
			return
		}
		item, err := e.deps.Store.ClaimNext(ctx)
		if err != nil {
			e.storeUnhealthy(runID, "claim", "", err)
			break
		}
		if item == nil {
			break // queue drained
		}
		if !started {
			started = true
			depth, _ := e.deps.Store.QueueDepth(ctx)
			e.logSyncPassStarted(runID, depth)
		}
		switch e.handleQueueItem(ctx, item, runID) {
		case catalog.OutcomePushed, catalog.OutcomeRetired:
			synced++
		case catalog.OutcomeSkipped:
			skipped++
		case catalog.OutcomeDropped:
			dropped++
		default: // faulted / partial (retried)
			faulted++
		}
	}
	depthAfter, derr := e.deps.Store.QueueDepth(ctx)
	if derr == nil {
		e.deps.Metrics.SetQueueDepth(depthAfter)
	}
	if started {
		e.logSyncPassCompleted(runID, synced, skipped, dropped, faulted, depthAfter, e.deps.Now().Sub(start))
	}
}

// handleQueueItem dispatches a claimed item: a retire settles immediately; a
// sync goes through the full resolve+push flow. It mints one pass_id for the
// claimed item and returns the terminal SyncOutcome so the pass can tally.
func (e *Engine) handleQueueItem(ctx context.Context, item *store.ClaimedItem, runID string) catalog.SyncOutcome {
	passID := e.newID()
	if item.Op == "retire" {
		if err := e.deps.Store.Complete(ctx, item.ID, item.ClaimID, item.ToVersion, store.CatalogState{
			CatalogID: item.CatalogID, IndexURL: item.IndexURL,
			Version: item.ToVersion, Status: string(catalog.CatalogRetired),
			Report: store.PassReport{
				At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
				Outcome: string(catalog.OutcomeRetired),
			},
		}); err != nil {
			e.storeUnhealthy(runID, "complete", item.CatalogID, err)
			return catalog.OutcomeFaulted
		}
		e.logSyncRetired(runID, passID, item)
		return catalog.OutcomeRetired
	}
	return e.syncCatalog(ctx, item, runID, passID)
}

// resolvePush produces the push doc + updateMode for a claimed catalog.
//
// This version (MergeOnly) always MERGEs: an incremental update is built as a
// delta straight from the change files (no baseline fetch) when they carry the
// catalog metadata envelope; a first sync — or a change set without that
// envelope — falls back to a full resolve, still pushed as MERGE. Removals are
// recorded in the changeset but not applied (deferred).
//
// With MergeOnly=false the dormant mode-by-changeset path runs: only-upserts ->
// MERGE (just the changed resources); any removal / new / re-baseline -> FULL.
func (e *Engine) resolvePush(entry catalog.CatalogEntry, item *store.ClaimedItem, fetch catalog.FetchFunc) ([]byte, string, catalog.Changeset, error) {
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
		if ok {
			return delta, publish.UpdateModeMerge, cs, nil
		}
		// No metadata envelope in the change files -> fall back to a full resolve.
	}
	full, cs, err := catalog.ResolveWithChangeset(entry, item.FromVersion, item.FromVersion > 0, item.ToVersion, fetch)
	return full, publish.UpdateModeMerge, cs, err
}

// completeSkipped settles a claimed item that had nothing to push (e.g. a
// removal-only change while removals are deferred): the cursor still advances
// and a skipped pass report is recorded, so it isn't re-processed.
func (e *Engine) completeSkipped(ctx context.Context, item *store.ClaimedItem, participantID, mode, reason, runID, passID string) {
	rep := store.PassReport{
		At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
		Mode: mode, Outcome: string(catalog.OutcomeSkipped), Reason: reason,
	}
	if err := e.deps.Store.Complete(ctx, item.ID, item.ClaimID, item.ToVersion, store.CatalogState{
		CatalogID: item.CatalogID, IndexURL: item.IndexURL, ParticipantID: participantID,
		Version: item.ToVersion, Status: string(catalog.CatalogActive), Report: rep,
	}); err != nil {
		e.storeUnhealthy(runID, "complete", item.CatalogID, err)
		return
	}
	e.logSyncSkipped(runID, passID, item, mode, reason)
}

// syncCatalog resolves + pushes one claimed catalog, then settles it. It walks
// the SyncPhase breadcrumbs (resolving → verifying → scoping → publishing) as
// each real step lands and returns the terminal SyncOutcome.
func (e *Engine) syncCatalog(ctx context.Context, item *store.ClaimedItem, runID, passID string) catalog.SyncOutcome {
	// resolving: the catalog job always needs the body to resolve the entry, so
	// it fetches unconditionally (empty cond) rather than risking a 304.
	phase := SyncResolving
	e.logSyncPhase(phase, runID, passID, item)
	res, err := e.deps.FetchIndex(ctx, item.IndexURL, catalog.IndexConditions{})
	if err != nil {
		e.routeFailure(ctx, item, e.newFailureReport(item, 0, "index_fetch: "+err.Error()), err, runID, passID)
		return catalog.OutcomeFaulted
	}
	entry, ok := catalog.FindCatalog(res.Index, item.CatalogID)
	if !ok {
		e.parkPermanently(ctx, item, e.newFailureReport(item, 0, "catalog_absent_from_index"), catalog.FaultAbsent, runID, passID)
		return catalog.OutcomeFaulted
	}

	fetch := func(f catalog.FileEntry) ([]byte, error) { return e.deps.FetchFile(ctx, f) }
	pushDoc, mode, cs, err := e.resolvePush(entry, item, fetch)
	if err != nil {
		e.routeFailure(ctx, item, e.newFailureReport(item, 0, "resolve: "+err.Error()), err, runID, passID)
		return catalog.OutcomeFaulted
	}
	// verifying: the resolve succeeded and each file's digest was checked inside
	// FetchFile as it was pulled. (validating/§9a is skipped — not implemented.)
	phase = e.advancePhase(phase, SyncVerifying, runID, passID, item)

	// This version applies upserts only; removals are recorded but deferred.
	if e.cfg.MergeOnly && cs.HasRemovals {
		e.logCatalogRemovalsSkipped(runID, item.CatalogID, cs.RemovedResources, cs.RemovedOffers)
	}

	resCount, offCount := publish.DocCounts(pushDoc) // what this pass actually pushes
	// Nothing to upsert (e.g. a removal-only change while removals are deferred):
	// skip the push, still advance the cursor, and record a skipped pass.
	if resCount == 0 && offCount == 0 {
		e.completeSkipped(ctx, item, res.Index.ParticipantID, mode, "no upserts (removals deferred)", runID, passID)
		return catalog.OutcomeSkipped
	}

	// scoping: decide membership + who this catalog is visible to.
	phase = e.advancePhase(phase, SyncScoping, runID, passID, item)
	_, visibleTo := catalog.ResolveScope(entry, e.cfg.Networks)
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
	batches, err := publish.BatchCatalog(pushDoc, docBudget, mode)
	if err != nil {
		e.parkPermanently(ctx, item, e.newFailureReport(item, 0, "batch: "+err.Error()), catalog.FaultOversize, runID, passID)
		return catalog.OutcomeFaulted
	}

	// publishing: push each batch to Discovery.
	phase = e.advancePhase(phase, SyncPublishing, runID, passID, item)
	start := e.deps.Now()
	var outcomes []publish.BatchOutcome
	for _, batch := range batches {
		body, err := publish.BuildPushBody(publish.PushMeta{
			ParticipantID: res.Index.ParticipantID, BppURI: e.cfg.BppURI,
			MessageID: e.newID(), TransactionID: e.newID(),
			Timestamp:  e.deps.Now().UTC().Format(time.RFC3339),
			UpdateMode: batch.UpdateMode, CatalogType: entry.CatalogType,
			VisibleTo: visibleTo,
		}, batch.Doc)
		if err != nil {
			e.parkPermanently(ctx, item, e.newFailureReport(item, 0, "build_push: "+err.Error()), catalog.FaultContentInvalid, runID, passID)
			return catalog.OutcomeFaulted
		}
		if e.deps.Validate != nil {
			if err := e.deps.Validate(ctx, body); err != nil {
				e.parkPermanently(ctx, item, e.newFailureReport(item, 0, "schema: "+err.Error()), catalog.FaultPushSchema, runID, passID)
				return catalog.OutcomeFaulted
			}
		}
		out, err := e.deps.Push(ctx, body)
		if err != nil {
			out = publish.BatchOutcome{HTTPStatus: out.HTTPStatus, Reason: err.Error()}
		}
		outcomes = append(outcomes, out)
		if !out.Acked {
			break // don't send later MERGE batches after a failed FULL
		}
	}
	e.deps.Metrics.ObservePushSeconds(e.deps.Now().Sub(start).Seconds())

	acked := publish.AckedCount(outcomes)
	if status, failed := publish.Rollup(outcomes); status != publish.SyncOK {
		httpStatus, reason := 0, "push failed"
		if len(failed) > 0 {
			httpStatus, reason = failed[0].HTTPStatus, failed[0].Reason
		}
		e.routeFailure(ctx, item, store.PassReport{
			At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
			Mode: mode, Resources: resCount, Offers: offCount,
			Removals:     cs.RemovedResources + cs.RemovedOffers,
			BatchesAcked: acked, BatchesTotal: len(batches),
			Outcome: string(classifyOutcome(httpStatus, acked, nil)), HTTPStatus: httpStatus, Reason: "push: " + reason,
		}, nil, runID, passID)
		return classifyOutcome(httpStatus, acked, nil)
	}

	if err := e.deps.Store.Complete(ctx, item.ID, item.ClaimID, item.ToVersion, store.CatalogState{
		CatalogID: item.CatalogID, IndexURL: item.IndexURL, ParticipantID: res.Index.ParticipantID,
		Version: item.ToVersion, Status: string(catalog.CatalogActive),
		Report: store.PassReport{
			At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
			Mode: mode, Resources: resCount, Offers: offCount,
			Removals:     cs.RemovedResources + cs.RemovedOffers,
			BatchesAcked: acked, BatchesTotal: len(batches),
			Outcome: string(catalog.OutcomePushed), HTTPStatus: outcomes[len(outcomes)-1].HTTPStatus,
		},
	}); err != nil {
		e.storeUnhealthy(runID, "complete", item.CatalogID, err)
		return catalog.OutcomeFaulted
	}
	e.deps.Metrics.CatalogPushed()
	e.logSyncPushed(runID, passID, item, mode, resCount, offCount, len(outcomes))
	return catalog.OutcomePushed
}

// advancePhase moves the Catalog Sync to its next running sub-state, emitting
// the DEBUG breadcrumb only when the transition is a declared one (ValidSyncPhase
// guards against emitting a phase that doesn't follow the machine).
func (e *Engine) advancePhase(from, to SyncPhase, runID, passID string, item *store.ClaimedItem) SyncPhase {
	if !ValidSyncPhase(from, to) {
		return from // defensive: don't record an illegal jump
	}
	e.logSyncPhase(to, runID, passID, item)
	return to
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
		e.storeUnhealthy(runID, "park", item.CatalogID, err)
	}
	if err := e.deps.Store.RecordFailure(ctx, item.CatalogID, item.IndexURL, "", report); err != nil {
		e.storeUnhealthy(runID, "record", item.CatalogID, err)
	}
	e.deps.Metrics.CatalogFailed(fc.String())
	e.logSyncFaulted(runID, passID, item, fc, report)
}

// scheduleRetry retries with capped backoff, and appends the pass report once
// MaxAttempts is reached. The cursor is NEVER advanced on failure (only
// Complete advances it), so the item keeps retrying until it succeeds.
func (e *Engine) scheduleRetry(ctx context.Context, item *store.ClaimedItem, report store.PassReport, fc catalog.FaultClass, runID, passID string) {
	attempts := item.Attempts + 1
	next := e.deps.Now().Add(Backoff(attempts))
	if err := e.deps.Store.RescheduleQueueItem(ctx, item.ID, item.ClaimID, next); err != nil {
		e.storeUnhealthy(runID, "reschedule", item.CatalogID, err)
	}
	if attempts >= e.cfg.MaxAttempts {
		// Record the failure WITHOUT advancing the version (queryable), and keep
		// the item queued for retry.
		if err := e.deps.Store.RecordFailure(ctx, item.CatalogID, item.IndexURL, "", report); err != nil {
			e.storeUnhealthy(runID, "record", item.CatalogID, err)
		}
		e.deps.Metrics.CatalogFailed(fc.String())
		e.logSyncRetryExhausted(runID, passID, item, attempts, fc, report)
		return
	}
	e.logSyncRetry(runID, passID, item, attempts, fc)
}
