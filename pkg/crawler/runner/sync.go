package runner

// sync.go — the SYNC job: drain the work queue, and for each claimed catalog
// run the Catalog Sync as a pipeline of named stages
// (resolve → pull+unpack → verify → scope → batch → push → settle), or a
// retire. It owns the retry/park routing (routeFailure → parkPermanently /
// scheduleRetry) and drives the SyncPhase breadcrumbs + terminal SyncOutcome.
// The log vocabulary itself lives in telemetry.go.
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
// one run_id per tick and emits the sync_pass heartbeat only when there was
// work (an empty queue is silent, so the CatalogInterval never spams logs).
func (e *Engine) catalogPass(ctx context.Context) {
	runID := e.newID()
	start := e.deps.Now()
	var synced, skipped, dropped, faulted, retried int
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
		case catalog.OutcomePartial:
			retried++ // partial push is a retry-in-progress, not a permanent fault
		default: // faulted
			faulted++
		}
	}
	depthAfter, derr := e.deps.Store.QueueDepth(ctx)
	if derr == nil {
		e.deps.Metrics.SetQueueDepth(depthAfter)
	}
	if started {
		e.logSyncPassCompleted(runID, synced, skipped, dropped, faulted, retried, depthAfter, e.deps.Now().Sub(start))
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
			e.storeUnhealthy(runID, "complete", item.CatalogID, err)
			return catalog.OutcomeFaulted
		}
		e.logSyncRetired(runID, passID, item)
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
	phase         SyncPhase // current running sub-state (drives the breadcrumb)

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

// syncStage is one step in the Catalog Sync pipeline. run does the step against
// the shared state and returns (outcome, stop): stop=true ends the sync here —
// either a terminal success/skip, or a failure the step already routed
// (park/retry). phase is the running sub-state this step belongs to.
type syncStage struct {
	phase SyncPhase
	what  string
	run   func(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool)
}

// syncCatalog runs ONE claimed catalog through the sync pipeline and returns its
// terminal SyncOutcome. This list is the whole per-catalog recipe — read it top
// to bottom. To add a step (e.g. §9a content-schema validation, which would be
// {SyncValidating, "validate content schema", e.validateContent}), insert one
// line; the runner drives the phase breadcrumb from each stage's phase.
func (e *Engine) syncCatalog(ctx context.Context, item *store.ClaimedItem, runID, passID string) catalog.SyncOutcome {
	s := &syncState{item: item, runID: runID, passID: passID}

	pipeline := []syncStage{
		{SyncResolving, "fetch index & find catalog", e.resolveEntry},
		{SyncResolving, "pull & unpack files", e.fetchContent},
		{SyncVerifying, "verify digests & decide upserts", e.verifyContent},
		{SyncScoping, "resolve who may see it (visibleTo)", e.resolveVisibility},
		{SyncScoping, "batch by size cap", e.batch},
		{SyncPublishing, "push each batch to Discovery", e.publish},
		{SyncPublishing, "record & advance cursor", e.complete},
	}
	return e.runPipeline(ctx, s, pipeline)
}

// runPipeline walks the stages in order. Before each stage it enters that
// stage's phase (emitting the breadcrumb once per phase); a stage returning
// stop=true ends the sync with its outcome.
func (e *Engine) runPipeline(ctx context.Context, s *syncState, stages []syncStage) catalog.SyncOutcome {
	for _, st := range stages {
		e.enterPhase(s, st.phase)
		if outcome, stop := st.run(ctx, s); stop {
			return outcome
		}
	}
	return catalog.OutcomePushed
}

// enterPhase moves the sync to a running sub-state and emits the DEBUG
// breadcrumb once per phase: it no-ops when the phase is unchanged (so adjacent
// same-phase stages don't re-log) and ignores an illegal jump (ValidSyncPhase),
// exactly as the old advancePhase did — the first phase (from "") always logs.
func (e *Engine) enterPhase(s *syncState, to SyncPhase) {
	if s.phase == to {
		return
	}
	if s.phase != "" && !ValidSyncPhase(s.phase, to) {
		return // defensive: don't record an illegal transition
	}
	s.phase = to
	e.logSyncPhase(to, s.runID, s.passID, s.item)
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
	if e.cfg.MergeOnly && s.cs.HasRemovals {
		e.logCatalogRemovalsSkipped(s.runID, s.item.CatalogID, s.cs.RemovedResources, s.cs.RemovedOffers)
	}
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
		e.storeUnhealthy(s.runID, "complete", s.item.CatalogID, err)
		return catalog.OutcomeFaulted, true
	}
	e.deps.Metrics.CatalogPushed()
	e.logSyncPushed(s.runID, s.passID, s.item, s.mode, s.resCount, s.offCount, len(s.outcomes))
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
		if ok {
			return delta, publish.UpdateModeMerge, cs, nil
		}
		// No metadata envelope in the change files -> fall back to a full resolve.
	}
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
		e.storeUnhealthy(runID, "complete", item.CatalogID, err)
		return
	}
	e.logSyncSkipped(runID, passID, item, mode, reason)
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
