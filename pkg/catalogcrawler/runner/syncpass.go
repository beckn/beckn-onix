package runner

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/store"
)

// pushEnvelopeReserve is headroom subtracted from the push-body cap to cover the
// fixed /push envelope (context + one publishDirective) that wraps each batch's
// catalog doc. visibleTo is accounted for separately at the call site.
const pushEnvelopeReserve = 4 << 10

// catalogPass drains the queue: claim -> handle -> repeat until empty.
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
		e.handleQueueItem(ctx, item)
	}
	if n, err := e.deps.Store.QueueDepth(ctx); err == nil {
		e.deps.Metrics.SetQueueDepth(n)
	}
}

// handleQueueItem dispatches a claimed item: a retire settles immediately; a
// sync goes through the full resolve+push flow.
func (e *Engine) handleQueueItem(ctx context.Context, item *store.ClaimedItem) {
	if item.Op == "retire" {
		if err := e.deps.Store.Complete(ctx, item.ID, item.ClaimID, item.ToVersion, store.CatalogState{
			CatalogID: item.CatalogID, IndexURL: item.IndexURL,
			Version: item.ToVersion, Status: string(catalog.CatalogRetired),
			Report: store.PassReport{
				At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
				Outcome: string(catalog.OutcomeRetired),
			},
		}); err != nil {
			e.deps.Log.Error("crawler.retire_failed", "catalogId", item.CatalogID, "err", err)
		} else {
			e.deps.Log.Info("crawler.catalog.retired", "catalogId", item.CatalogID)
		}
		return
	}
	e.syncCatalog(ctx, item)
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
func (e *Engine) completeSkipped(ctx context.Context, item *store.ClaimedItem, participantID, mode, reason string) {
	rep := store.PassReport{
		At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
		Mode: mode, Outcome: string(catalog.OutcomeSkipped), Reason: reason,
	}
	if err := e.deps.Store.Complete(ctx, item.ID, item.ClaimID, item.ToVersion, store.CatalogState{
		CatalogID: item.CatalogID, IndexURL: item.IndexURL, ParticipantID: participantID,
		Version: item.ToVersion, Status: string(catalog.CatalogActive), Report: rep,
	}); err != nil {
		e.deps.Log.Error("crawler.complete_failed", "catalogId", item.CatalogID, "err", err)
		return
	}
	e.deps.Log.Info("crawler.catalog.skipped", "catalogId", item.CatalogID, "reason", reason)
}

// syncCatalog resolves + pushes one claimed catalog, then settles it.
func (e *Engine) syncCatalog(ctx context.Context, item *store.ClaimedItem) {
	// The catalog job always needs the body to resolve the entry, so it fetches
	// unconditionally (empty cond) rather than risking a 304.
	res, err := e.deps.FetchIndex(ctx, item.IndexURL, catalog.IndexConditions{})
	if err != nil {
		e.routeFailure(ctx, item, e.newFailureReport(item, 0, "index_fetch: "+err.Error()), err)
		return
	}
	entry, ok := catalog.FindCatalog(res.Index, item.CatalogID)
	if !ok {
		e.parkPermanently(ctx, item, e.newFailureReport(item, 0, "catalog_absent_from_index"))
		return
	}

	fetch := func(f catalog.FileEntry) ([]byte, error) { return e.deps.FetchFile(ctx, f) }
	pushDoc, mode, cs, err := e.resolvePush(entry, item, fetch)
	if err != nil {
		e.routeFailure(ctx, item, e.newFailureReport(item, 0, "resolve: "+err.Error()), err)
		return
	}

	// This version applies upserts only; removals are recorded but deferred.
	if e.cfg.MergeOnly && cs.HasRemovals {
		e.deps.Log.Warn("crawler.catalog.removals_skipped", "catalogId", item.CatalogID,
			"resources", cs.RemovedResources, "offers", cs.RemovedOffers)
	}

	resCount, offCount := publish.DocCounts(pushDoc) // what this pass actually pushes
	// Nothing to upsert (e.g. a removal-only change while removals are deferred):
	// skip the push, still advance the cursor, and record a skipped pass.
	if resCount == 0 && offCount == 0 {
		e.completeSkipped(ctx, item, res.Index.ParticipantID, mode, "no upserts (removals deferred)")
		return
	}
	_, visibleTo := catalog.Select(entry, e.cfg.Networks)
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
		e.parkPermanently(ctx, item, e.newFailureReport(item, 0, "batch: "+err.Error()))
		return
	}

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
			e.parkPermanently(ctx, item, e.newFailureReport(item, 0, "build_push: "+err.Error()))
			return
		}
		if e.deps.Validate != nil {
			if err := e.deps.Validate(ctx, body); err != nil {
				e.parkPermanently(ctx, item, e.newFailureReport(item, 0, "schema: "+err.Error()))
				return
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
		}, nil)
		return
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
		e.deps.Log.Error("crawler.complete_failed", "catalogId", item.CatalogID, "err", err)
		return
	}
	e.deps.Metrics.CatalogPushed()
	e.deps.Log.Info("crawler.catalog.pushed", "catalogId", item.CatalogID, "version", item.ToVersion,
		"mode", mode, "resources", resCount, "offers", offCount, "batches", len(outcomes))
}

// routeFailure routes a failure: permanent (won't fix on retry) faults and 4xx
// rejections are parked; everything else is a transient retry. Either way the
// pass report is appended to the catalog's push_status history. The
// permanent-vs-transient axis is a single typed FaultClass (§6b).
func (e *Engine) routeFailure(ctx context.Context, item *store.ClaimedItem, report store.PassReport, err error) {
	if catalog.ClassifyFault(report.HTTPStatus, err).Permanent() {
		e.parkPermanently(ctx, item, report)
		return
	}
	e.scheduleRetry(ctx, item, report)
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
func (e *Engine) parkPermanently(ctx context.Context, item *store.ClaimedItem, report store.PassReport) {
	if err := e.deps.Store.ParkQueueItem(ctx, item.ID, item.ClaimID); err != nil {
		e.deps.Log.Error("crawler.park_failed", "catalogId", item.CatalogID, "err", err)
	}
	if err := e.deps.Store.RecordFailure(ctx, item.CatalogID, item.IndexURL, "", report); err != nil {
		e.deps.Log.Error("crawler.recordfailure_failed", "catalogId", item.CatalogID, "err", err)
	}
	e.deps.Metrics.CatalogFailed(reasonCategory(report.Reason))
	e.deps.Log.Error("crawler.catalog.permanent_failure", "catalogId", item.CatalogID, "reason", report.Reason, "httpStatus", report.HTTPStatus)
}

// scheduleRetry retries with capped backoff, and appends the pass report once
// MaxAttempts is reached. The cursor is NEVER advanced on failure (only
// Complete advances it), so the item keeps retrying until it succeeds.
func (e *Engine) scheduleRetry(ctx context.Context, item *store.ClaimedItem, report store.PassReport) {
	attempts := item.Attempts + 1
	next := e.deps.Now().Add(Backoff(attempts))
	if err := e.deps.Store.RescheduleQueueItem(ctx, item.ID, item.ClaimID, next); err != nil {
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

// reasonCategory reduces a full failure reason ("schema: ...") to a
// low-cardinality category ("schema") for the failed-total metric label.
func reasonCategory(reason string) string {
	if i := strings.IndexByte(reason, ':'); i > 0 {
		return reason[:i]
	}
	return reason
}
