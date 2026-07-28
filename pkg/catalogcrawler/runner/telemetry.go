package runner

// telemetry.go — the runner's co-located §9b log vocabulary. Every crawler
// event the orchestration emits is minted by exactly one helper here, so the
// event catalog (names, levels, mandatory fields) lives in one readable place
// rather than scattered across the passes. Two fields are mandatory on every
// line: `lifecycle` (which state machine — sync / sync_pass / index_pass /
// index_crawl / catalog / store) and `state` (where in it); the event key is
// always `crawler.<lifecycle>.<state>`. Typed values only: fault classes and
// decisions are rendered via their String(), never a free-form error phrase
// (the raw cause travels in `error`). The daemon lifecycle (crawler.daemon.*)
// is owned by the composition root, not here.

import (
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/store"
)

// errStr renders an error for the `error` field (empty when nil).
func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// syncKV is the mandatory field set every lifecycle=sync line carries: the two
// correlation ids plus the catalog and the version jump it is making.
func syncKV(state, runID, passID string, item *store.ClaimedItem) []any {
	return []any{
		"lifecycle", "sync", "state", state,
		"run_id", runID, "pass_id", passID,
		"catalog_id", item.CatalogID,
		"from_version", item.FromVersion, "to_version", item.ToVersion,
	}
}

// --- Catalog Sync (lifecycle=sync) ------------------------------------------

// logSyncPhase emits the DEBUG breadcrumb for a running sub-state the sync
// actually reached (resolving/verifying/scoping/publishing).
func (e *Engine) logSyncPhase(phase SyncPhase, runID, passID string, item *store.ClaimedItem) {
	e.deps.Log.Debug("crawler.sync."+phase.String(), syncKV(phase.String(), runID, passID, item)...)
}

// logSyncPushed is the success terminal: the catalog landed in Discovery.
func (e *Engine) logSyncPushed(runID, passID string, item *store.ClaimedItem, mode string, resources, offers, batches int) {
	kv := append(syncKV("pushed", runID, passID, item),
		"mode", mode, "resources", resources, "offers", offers, "batches", batches)
	e.deps.Log.Debug("crawler.sync.pushed", kv...)
}

// logSyncSkipped is the terminal for a claimed item with nothing to push (e.g.
// a removal-only change while removals are deferred).
func (e *Engine) logSyncSkipped(runID, passID string, item *store.ClaimedItem, mode, reason string) {
	kv := append(syncKV("skipped", runID, passID, item), "mode", mode, "reason", reason)
	e.deps.Log.Debug("crawler.sync.skipped", kv...)
}

// logSyncRetired is the terminal for a retire settle (catalog tombstoned).
func (e *Engine) logSyncRetired(runID, passID string, item *store.ClaimedItem) {
	e.deps.Log.Debug("crawler.sync.retired", syncKV("retired", runID, passID, item)...)
}

// logSyncRetry is a transient retry attempt BEFORE exhaustion (DEBUG trace).
func (e *Engine) logSyncRetry(runID, passID string, item *store.ClaimedItem, attempts int, fc catalog.FaultClass) {
	kv := append(syncKV("retry", runID, passID, item), "attempts", attempts, "fault_class", fc.String())
	e.deps.Log.Debug("crawler.sync.retry", kv...)
}

// logSyncFaulted is the operator alert: the sync failed permanently and is
// parked (no hot retry; re-activates on a version bump).
func (e *Engine) logSyncFaulted(runID, passID string, item *store.ClaimedItem, fc catalog.FaultClass, report store.PassReport) {
	kv := append(syncKV("faulted", runID, passID, item),
		"fault_class", fc.String(), "permanent", true)
	if report.HTTPStatus != 0 {
		kv = append(kv, "http_status", report.HTTPStatus)
	}
	kv = append(kv, "error", report.Reason)
	e.deps.Log.Error("crawler.sync.faulted", kv...)
}

// logSyncRetryExhausted fires ONLY when a transient fault has burned through
// MaxAttempts: the failure is now recorded (queryable) though the item stays
// queued.
func (e *Engine) logSyncRetryExhausted(runID, passID string, item *store.ClaimedItem, attempts int, fc catalog.FaultClass, report store.PassReport) {
	kv := append(syncKV("retry_exhausted", runID, passID, item),
		"attempts", attempts, "fault_class", fc.String(), "error", report.Reason)
	e.deps.Log.Warn("crawler.sync.retry_exhausted", kv...)
}

// --- Sync batch heartbeat (lifecycle=sync_pass) -----------------------------

// logSyncPassStarted marks a catalog tick that found work (silent when idle).
func (e *Engine) logSyncPassStarted(runID string, queueDepth int) {
	e.deps.Log.Info("crawler.sync_pass.started",
		"lifecycle", "sync_pass", "state", "started",
		"run_id", runID, "trigger", "schedule", "queue_depth", queueDepth)
}

// logSyncPassCompleted closes a catalog tick that did work, with its tally.
func (e *Engine) logSyncPassCompleted(runID string, synced, skipped, dropped, faulted, queueDepthAfter int, dur time.Duration) {
	e.deps.Log.Info("crawler.sync_pass.completed",
		"lifecycle", "sync_pass", "state", "completed", "run_id", runID,
		"synced", synced, "skipped", skipped, "dropped", dropped, "faulted", faulted,
		"queue_depth_after", queueDepthAfter, "duration_ms", dur.Milliseconds())
}

// --- Index crawl (lifecycle=index_pass / index_crawl) -----------------------

// logIndexPassCompleted closes one index tick with its tally.
func (e *Engine) logIndexPassCompleted(runID string, checked, changed, enqueued int, dur time.Duration) {
	e.deps.Log.Info("crawler.index_pass.completed",
		"lifecycle", "index_pass", "state", "completed", "run_id", runID,
		"indexes_checked", checked, "indexes_changed", changed,
		"catalogs_enqueued", enqueued, "duration_ms", dur.Milliseconds())
}

// logIndexPassFailed fires when the source can't even be resolved (no refs to
// crawl this tick).
func (e *Engine) logIndexPassFailed(runID, stage string, err error) {
	e.deps.Log.Error("crawler.index_pass.failed",
		"lifecycle", "index_pass", "state", "failed",
		"run_id", runID, "stage", stage, "error", errStr(err))
}

// logIndexFetchFailed fires when one index can't be fetched/parsed. The
// participant is still unknown pre-fetch, so this is keyed on the index URL.
func (e *Engine) logIndexFetchFailed(runID, indexURL string, fc catalog.FaultClass, err error) {
	e.deps.Log.Warn("crawler.index_crawl.fetch_failed",
		"lifecycle", "index_crawl", "state", "fetch_failed",
		"run_id", runID, "index_url", indexURL, "fault_class", fc.String(), "error", errStr(err))
}

// logIndexRollback flags a catalog whose index version went backwards (not
// applied — recorded for an operator).
func (e *Engine) logIndexRollback(runID, catalogID string, cursorVersion, indexVersion int64) {
	e.deps.Log.Warn("crawler.index_crawl.rollback",
		"lifecycle", "index_crawl", "state", "rollback",
		"run_id", runID, "catalog_id", catalogID,
		"cursor_version", cursorVersion, "index_version", indexVersion)
}

// logIndexChecked / logIndexUnchanged / logIndexNotModified are the DEBUG
// traces of a single index crawl's shape (200 processed / 200 same-version /
// 304).
func (e *Engine) logIndexChecked(runID, indexURL string, version int64) {
	e.deps.Log.Debug("crawler.index_crawl.checked",
		"lifecycle", "index_crawl", "state", "checked",
		"run_id", runID, "index_url", indexURL, "version", version)
}

func (e *Engine) logIndexUnchanged(runID, indexURL string, version int64) {
	e.deps.Log.Debug("crawler.index_crawl.unchanged",
		"lifecycle", "index_crawl", "state", "unchanged",
		"run_id", runID, "index_url", indexURL, "version", version)
}

func (e *Engine) logIndexNotModified(runID, indexURL string) {
	e.deps.Log.Debug("crawler.index_crawl.not_modified",
		"lifecycle", "index_crawl", "state", "not_modified",
		"run_id", runID, "index_url", indexURL)
}

// --- Catalog decisions (lifecycle=catalog) ----------------------------------

// logCatalogDecided traces what the index crawl decided to do with one catalog.
func (e *Engine) logCatalogDecided(runID, catalogID, decision string, cursor, toVersion int64) {
	e.deps.Log.Debug("crawler.catalog.decided",
		"lifecycle", "catalog", "state", "decided",
		"run_id", runID, "catalog_id", catalogID,
		"decision", decision, "cursor", cursor, "to_version", toVersion)
}

func (e *Engine) logCatalogEnqueued(runID, catalogID string, toVersion int64) {
	e.deps.Log.Debug("crawler.catalog.enqueued",
		"lifecycle", "catalog", "state", "enqueued",
		"run_id", runID, "catalog_id", catalogID, "to_version", toVersion)
}

func (e *Engine) logCatalogRetireEnqueued(runID, catalogID string) {
	e.deps.Log.Debug("crawler.catalog.retire_enqueued",
		"lifecycle", "catalog", "state", "retire_enqueued",
		"run_id", runID, "catalog_id", catalogID)
}

func (e *Engine) logCatalogRemovalsSkipped(runID, catalogID string, resources, offers int) {
	e.deps.Log.Debug("crawler.catalog.removals_skipped",
		"lifecycle", "catalog", "state", "removals_skipped",
		"run_id", runID, "catalog_id", catalogID, "resources", resources, "offers", offers)
}

// --- Store (lifecycle=store) ------------------------------------------------

// storeUnhealthy is the single collapsed event for every failed store op: the
// crawler's DB is momentarily unhealthy for `operation`. It replaced ~a dozen
// bespoke *_failed events. operation uses the short vocab: read_index,
// advance_cadence, read_cursor, enqueue, upsert_index, claim, complete, park,
// reschedule, record.
func (e *Engine) storeUnhealthy(runID, operation, catalogID string, err error) {
	kv := []any{
		"lifecycle", "store", "state", "unhealthy",
		"run_id", runID, "operation", operation,
	}
	if catalogID != "" {
		kv = append(kv, "catalog_id", catalogID)
	}
	kv = append(kv, "error", errStr(err))
	e.deps.Log.Error("crawler.store.unhealthy", kv...)
}
