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

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/publish"
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
func (e *Engine) handleQueueItem(ctx context.Context, item *catalog.ClaimedItem, runID string) catalog.SyncOutcome {
	passID := e.newID()
	if item.Op == "retire" {
		if err := e.settle(ctx, item, "", catalog.CatalogRetired, catalog.PassReport{
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
	item          *catalog.ClaimedItem
	runID, passID string

	entry         catalog.CatalogEntry
	nodeID        string
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
func (e *Engine) syncCatalog(ctx context.Context, item *catalog.ClaimedItem, runID, passID string) catalog.SyncOutcome {
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
	s.nodeID = res.Index.NodeID
	return "", false
}

// fetchContent (resolving): pull the baseline/change files and unpack them into
// the push doc + updateMode. Each file's digest and self-signature are verified
// inside FetchFile as it is pulled.
func (e *Engine) fetchContent(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool) {
	fetch := func(f catalog.FileEntry) ([]byte, error) { return e.deps.FetchFile(ctx, s.nodeID, f) }
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
//
// A zero count is the dangerous case, because publish.DocCounts is best-effort:
// a doc that will not parse also counts zero. Settling on that count alone would
// let corrupt content advance the cursor as a clean success, with nothing pushed
// and no alert. So before a zero-count doc may settle, its shape is confirmed:
// corrupt content is a permanent content_invalid fault that parks, while a doc
// that really does carry an empty catalog skips cleanly.
func (e *Engine) verifyContent(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool) {
	s.resCount, s.offCount = publish.DocCounts(s.pushDoc)
	if s.resCount == 0 && s.offCount == 0 {
		if err := catalog.ValidateCatalogDoc(s.pushDoc); err != nil {
			e.routeClassified(ctx, s.item, e.newFailureReport(s.item, 0, "verify: "+err.Error()),
				catalog.FaultContentInvalid, s.runID, s.passID)
			return catalog.OutcomeFaulted, true
		}
		e.completeSkipped(ctx, s.item, s.nodeID, s.mode, skipReason(s.cs), s.runID, s.passID)
		return catalog.OutcomeSkipped, true
	}
	return "", false
}

// skipReason names why a pass had nothing to push, so the recorded reason is the
// true one. The two cases are different states and an operator reading the pass
// history must be able to tell them apart: a change that only removed items
// (removals are deferred in this version) versus a catalog that is genuinely
// empty at this version.
func skipReason(cs catalog.Changeset) string {
	if cs.HasRemovals {
		return "no upserts (removals deferred)"
	}
	return "catalog carries no resources or offers at this version"
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
//
// Because the loop stops at the first un-acked batch, a failure can carry
// BatchesAcked > 0: Discovery durably applied a PREFIX of this version while our
// cursor still points at the previous one. That partial state is what
// decideFailureAction keys on — such a failure must be retried (resent from the
// cursor), never parked, or the divergence is silent and permanent.
//
// This is why NO failure site inside the loop may park directly. A build or
// schema failure on batch 2 happens after batch 1 was already acked, so it
// carries the same partial state as a remote 4xx and must go through the same
// policy with the same acked count. ackedSoFar is that count, and every exit
// below hands it to failPublish.
func (e *Engine) publish(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool) {
	start := e.deps.Now()
	var outcomes []publish.BatchOutcome
	ackedSoFar := 0 // batches Discovery has durably applied in THIS pass, so far
	for _, b := range s.batches {
		body, err := publish.BuildPushBody(publish.PushMeta{
			ParticipantID: s.nodeID, BppURI: e.cfg.BppURI,
			MessageID: e.newID(), TransactionID: e.newID(),
			Timestamp:  e.deps.Now().UTC().Format(time.RFC3339),
			UpdateMode: b.UpdateMode, CatalogType: s.entry.CatalogType,
			VisibleTo: s.visibleTo,
		}, b.Doc)
		if err != nil {
			return e.failPublish(ctx, s, outcomes, ackedSoFar, catalog.FaultContentInvalid, "build_push: "+err.Error())
		}
		if e.deps.Validate != nil {
			if err := e.deps.Validate(ctx, body); err != nil {
				return e.failPublish(ctx, s, outcomes, ackedSoFar, catalog.FaultPushSchema, "schema: "+err.Error())
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
		ackedSoFar++
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
		report := e.publishFailureReport(s, acked, httpStatus, "push: "+reason)
		e.routeFailure(ctx, s.item, report, nil, s.runID, s.passID)
		return catalog.SyncOutcome(report.Outcome), true
	}
	return "", false
}

// failPublish ends the batch loop on a LOCAL failure (the push body could not be
// built, or it failed schema validation). fc is known at the call site, so the
// failure is routed with that class instead of re-deriving one from an error
// string. It exists so those sites cannot park directly: acked is the real
// number of batches Discovery already applied, and decideFailureAction turns
// acked > 0 into a retry, which is the rule that keeps a half-applied catalog
// self-healing.
func (e *Engine) failPublish(ctx context.Context, s *syncState, outcomes []publish.BatchOutcome, acked int, fc catalog.FaultClass, reason string) (catalog.SyncOutcome, bool) {
	s.outcomes, s.acked = outcomes, acked
	report := e.publishFailureReport(s, acked, 0, reason)
	e.routeClassified(ctx, s.item, report, fc, s.runID, s.passID)
	return catalog.SyncOutcome(report.Outcome), true
}

// publishFailureReport builds the pass report for a failure raised during the
// publish stage. It is the ONE builder for those reports, so BatchesAcked always
// carries the true count: a report that understates it (the old hardcoded 0)
// hides a half-applied catalog from the operator and from any later audit.
func (e *Engine) publishFailureReport(s *syncState, acked, httpStatus int, reason string) catalog.PassReport {
	return catalog.PassReport{
		At: e.deps.Now().UTC(), FromVersion: s.item.FromVersion, ToVersion: s.item.ToVersion,
		Mode: s.mode, Resources: s.resCount, Offers: s.offCount,
		Removals:     s.cs.RemovedResources + s.cs.RemovedOffers,
		BatchesAcked: acked, BatchesTotal: len(s.batches),
		Outcome: string(classifyOutcome(httpStatus, acked, nil)), HTTPStatus: httpStatus, Reason: reason,
	}
}

// complete (publishing): settle a fully-acked sync — advance the cursor, record
// the pushed pass report, and emit the success terminal.
func (e *Engine) complete(ctx context.Context, s *syncState) (catalog.SyncOutcome, bool) {
	if err := e.settle(ctx, s.item, s.nodeID, catalog.CatalogActive, catalog.PassReport{
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
// delta straight from the change files, fetching the baseline only for its
// id/descriptor/provider (never its resources/offers) on the one-time case
// where no change file in range carries that metadata itself (see
// catalog.ResolveDelta). A first sync (cursor behind the baseline) always
// falls back to a full resolve, still pushed as MERGE. Removals are recorded
// in the changeset but not applied (deferred).
//
// With MergeOnly=false the dormant mode-by-changeset path runs: only-upserts ->
// MERGE (just the changed resources); any removal / new / re-baseline -> FULL.
func (e *Engine) buildPushDoc(entry catalog.CatalogEntry, item *catalog.ClaimedItem, fetch catalog.FetchFunc) ([]byte, string, catalog.Changeset, error) {
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

	// Incremental update: try the delta straight from the change files first.
	// If none of them carry the catalog metadata envelope (id/descriptor/
	// provider), ResolveDelta itself falls back to a one-time baseline fetch
	// for that envelope only — still pushed as MERGE, still just the changed
	// resources/offers, never the baseline's own. firstSync (cursor behind the
	// baseline) can't use this path at all — the baseline is the only content —
	// so it falls through to the full resolve below.
	if item.FromVersion >= entry.Baseline.Version {
		delta, cs, ok, err := catalog.ResolveDelta(entry, item.FromVersion, item.ToVersion, fetch)
		if err != nil {
			return nil, "", cs, err
		}
		if !ok {
			// Defensive only: ResolveDelta returns ok=false exclusively alongside a
			// non-nil error now that it self-resolves the metadata envelope, so this
			// should be unreachable. Guard it anyway rather than push a doc whose
			// envelope was never actually verified as present.
			return nil, "", cs, catalog.PermanentFaultf(catalog.FaultContentInvalid,
				"crawler: could not resolve a catalog metadata envelope for %s", entry.CatalogID)
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
func (e *Engine) settle(ctx context.Context, item *catalog.ClaimedItem, participantID string, status catalog.CatalogStatus, report catalog.PassReport) error {
	return e.deps.Store.Complete(ctx, item.ID, item.ClaimID, item.ToVersion, catalog.CatalogState{
		CatalogID: item.CatalogID, IndexURL: item.IndexURL, ParticipantID: participantID,
		Version: item.ToVersion, EntryVersion: item.EntryVersion, Status: string(status), Report: report,
	})
}

// completeSkipped settles a claimed item that had nothing to push (e.g. a
// removal-only change while removals are deferred): the cursor still advances
// and a skipped pass report is recorded, so it isn't re-processed.
func (e *Engine) completeSkipped(ctx context.Context, item *catalog.ClaimedItem, participantID, mode, reason, runID, passID string) {
	rep := catalog.PassReport{
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

// routeFailure classifies the fault from the report's HTTP status and err (the
// taxonomy is a single typed FaultClass, §6b) and routes it onto the
// park-vs-retry policy.
//
// Where the pass report lands, precisely: a PARK always appends it to the
// catalog's push_status history. A RETRY appends it only once the attempt
// budget is spent (scheduleRetry's past-budget branch, which in practice only a
// partial push reaches); a transient retry below the cap is log-only, so the
// history is not filled with noise that the next attempt is about to resolve.
func (e *Engine) routeFailure(ctx context.Context, item *catalog.ClaimedItem, report catalog.PassReport, err error, runID, passID string) {
	e.routeClassified(ctx, item, report, catalog.ClassifyFault(report.HTTPStatus, err), runID, passID)
}

// routeClassified is routeFailure for a fault the caller ALREADY knows (a local
// build or schema failure, where there is no HTTP status and no wrapped error to
// classify from). It is the single choke point for the park-vs-retry decision:
// every failure site reaches parkPermanently / scheduleRetry through here, so
// none of them can bypass the acked > 0 rule.
func (e *Engine) routeClassified(ctx context.Context, item *catalog.ClaimedItem, report catalog.PassReport, fc catalog.FaultClass, runID, passID string) {
	if decideFailureAction(fc, report.BatchesAcked, item.Attempts+1, e.cfg.MaxAttempts) == actionPark {
		e.parkPermanently(ctx, item, report, fc, runID, passID)
		return
	}
	e.scheduleRetry(ctx, item, report, fc, runID, passID)
}

// failureAction is what the park-vs-retry policy decided for one failed pass.
type failureAction int

const (
	actionRetry failureAction = iota // reschedule behind the backoff
	actionPark                       // terminal until a version bump: ERROR + counted
)

// decideFailureAction is the ONE place the park-vs-retry policy lives. attempts
// is the attempt this failure just consumed (item.Attempts+1); ackedBatches is
// how many batches Discovery durably applied before the failure. The three rules,
// in precedence order:
//
//  1. A partial push (ackedBatches > 0) ALWAYS retries. Discovery holds a prefix
//     of a version our cursor has not reached, so parking would freeze that split
//     brain: no retry, no cursor advance, and nothing self-heals until the
//     publisher happens to bump the version. This outranks rule 2 because a 4xx
//     classifies as FaultPushRejected (permanent) even when earlier batches were
//     acked — the rejection is about the batch that failed, not about the ones
//     already applied.
//  2. A permanent fault parks. It won't fix itself on retry (corrupt artifact,
//     schema rejection, a genuine 4xx with nothing applied), so hot-retrying it
//     only burns the queue.
//  3. A transient fault parks once it has consumed the attempt budget
//     (attempts >= maxAttempts). This is what makes MaxAttempts mean what
//     EngineConfig says it means: give up on a catalog after this many failed
//     pushes, so a permanently unreachable catalog becomes an operator-actionable
//     ERROR instead of retrying forever. maxAttempts <= 0 disables the cap (a
//     zero-value config must not park on the first failure).
func decideFailureAction(fc catalog.FaultClass, ackedBatches, attempts, maxAttempts int) failureAction {
	switch {
	case ackedBatches > 0:
		return actionRetry
	case fc.Permanent():
		return actionPark
	case maxAttempts > 0 && attempts >= maxAttempts:
		return actionPark
	default:
		return actionRetry
	}
}

// newFailureReport builds the minimal pass report for a pre-push failure (no
// push counts): the outcome is derived from the HTTP status.
func (e *Engine) newFailureReport(item *catalog.ClaimedItem, httpStatus int, reason string) catalog.PassReport {
	return catalog.PassReport{
		At: e.deps.Now().UTC(), FromVersion: item.FromVersion, ToVersion: item.ToVersion,
		Outcome: string(classifyOutcome(httpStatus, 0, nil)), HTTPStatus: httpStatus, Reason: reason,
	}
}

// parkPermanently parks a permanently-failed item (no hot retry; re-activates on
// a version bump), appends the pass report WITHOUT advancing the cursor, counts
// it, and logs at error level (the operator alert). "Too big" / "corrupt" /
// "unsupported encoding" becomes visible and actionable, never silently lost.
func (e *Engine) parkPermanently(ctx context.Context, item *catalog.ClaimedItem, report catalog.PassReport, fc catalog.FaultClass, runID, passID string) {
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

// scheduleRetry reschedules the item behind a capped backoff. The cursor is
// NEVER advanced on failure (only Complete advances it), so the next attempt
// resends the WHOLE push from the cursor — batch 1 onward — rather than
// resuming mid-stream.
//
// The MaxAttempts contract (this is the accurate one; EngineConfig's field
// comment is the authority on intent, not on behaviour): a transient fault gets
// at most MaxAttempts attempts and is then parked by decideFailureAction, so it
// becomes operator-actionable instead of retrying forever. The one case that
// reaches this function past the cap is a PARTIAL push (rule 1): it is exempt
// from parking, so past the cap it records the failure (queryable) on every
// attempt and keeps retrying with backoff.
//
// Residual risk on a partial: resending from the cursor is what re-baselines
// Discovery, and it converges because both FULL and MERGE directives are
// idempotent upserts. What we cannot do here is FORCE the baseline path — that
// would mean rewinding the queue row's from_version below the entry's baseline
// version, which needs a store method this package does not have. So under
// MergeOnly a partially-applied version is repaired by re-sending the same
// delta, not by re-downloading the baseline; a Discovery state a delta cannot
// express (e.g. a removal that landed) stays divergent until the publisher
// republishes a baseline.
func (e *Engine) scheduleRetry(ctx context.Context, item *catalog.ClaimedItem, report catalog.PassReport, fc catalog.FaultClass, runID, passID string) {
	attempts := item.Attempts + 1
	next := e.deps.Now().Add(Backoff(attempts))
	if err := e.deps.Store.RescheduleQueueItem(ctx, item.ID, item.ClaimID, next); err != nil {
		e.storeUnhealthy("sync", runID, "reschedule", item.CatalogID, err)
	}
	// Same guard decideFailureAction uses: MaxAttempts <= 0 means "no cap", so
	// there is no past-the-budget state to record. The two must agree or a
	// zero-value config would record a failure on every single retry.
	if e.cfg.MaxAttempts > 0 && attempts >= e.cfg.MaxAttempts {
		// Past the budget and still retrying: only a partial push gets here.
		// Record the failure (queryable) WITHOUT advancing the version so the
		// split brain is visible to an operator while it keeps retrying.
		if err := e.deps.Store.RecordFailure(ctx, item.CatalogID, item.IndexURL, "", report); err != nil {
			e.storeUnhealthy("sync", runID, "record", item.CatalogID, err)
		}
		e.deps.Metrics.RecordSyncOutcome(report.Outcome, fc.String())
	}
	// Will retry with backoff (WARN, not ERROR — it is not parked).
	e.logFailed(runID, passID, item, fc, report, true, attempts)
}

// classifyOutcome is the ONE place the push-outcome rule lives (it replaced the
// duplicated logic at the old engine fail sites): a 4xx push rejection or any
// error is `faulted` (this only names the PERSISTED outcome; park-vs-retry is
// decided separately by decideFailureAction, which is why a 4xx that acked some
// batches reads `faulted` here and still retries); a push that
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
