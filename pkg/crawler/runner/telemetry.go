package runner

// telemetry.go — the runner's co-located log vocabulary (see docs/crawler-logs.md).
// Every crawler event the orchestration emits is minted by exactly one helper
// here, so the event catalog (components, stages, messages, levels, fields)
// lives in one readable place rather than scattered across the jobs.
//
// The model is one process running two jobs, so there are three log components:
// `daemon` (the process — owned by the composition root, not here), `crawl`
// (job 1: poll indexes, queue changed catalogs), and `sync` (job 2: sync one
// queued catalog to Discovery). Every line carries the natural message (the
// logger's first arg) plus base fields `component` and `stage`; runner-emitted
// lines also carry `run_id` (daemon lines, from the composition root, carry
// `component`/`stage` only). Sync per-catalog lines add `pass_id`,
// `catalog_id`, `from`, `to`. The event key is always
// `crawler.<component>.<stage>`. The persisted status/outcome enums
// Discovery/DB care about live in catalog/status.go.

import (
	"fmt"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/crawler/store"
)

// errStr renders an error for the `error` field (empty when nil).
func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// triggerStr renders a crawl trigger for the `trigger` field: every
// component=crawl line carries this, so a scheduled tick and an on-demand
// /crawl can be told apart without cross-referencing run_id against the
// HTTP response that minted it.
func triggerStr(trig trigger) string {
	if trig == onDemand {
		return "on_demand"
	}
	return "scheduled"
}

// syncKV is the mandatory field set every component=sync per-catalog line
// carries: the two correlation ids plus the catalog and the version jump.
func syncKV(stage, runID, passID string, item *store.ClaimedItem) []any {
	return []any{
		"component", "sync", "stage", stage,
		"run_id", runID, "pass_id", passID,
		"catalog_id", item.CatalogID,
		"from", item.FromVersion, "to", item.ToVersion,
	}
}

// --- crawl (job 1: find work) -----------------------------------------------

// logPolled is the DEBUG trace of one index poll: result is `updated`,
// `unchanged`, or `not_modified` (version is 0 for a 304, which had no body).
func (e *Engine) logPolled(runID string, trig trigger, indexURL string, version int64, result string) {
	msg := "index unchanged"
	switch result {
	case "updated":
		msg = fmt.Sprintf("index updated to v%d", version)
	case "not_modified":
		msg = "index not modified (304)"
	}
	e.deps.Log.Debug(msg,
		"component", "crawl", "stage", "polled", "run_id", runID, "trigger", triggerStr(trig),
		"index_url", indexURL, "version", version, "result", result)
}

// logPollFailed fires when one index can't be reached/parsed (WARN — the crawl
// will try again next tick). Keyed on the index URL: the participant is still
// unknown pre-fetch.
func (e *Engine) logPollFailed(runID string, trig trigger, indexURL string, err error) {
	e.deps.Log.Warn("couldn't reach the index: "+errStr(err),
		"component", "crawl", "stage", "polled", "run_id", runID, "trigger", triggerStr(trig),
		"index_url", indexURL, "result", "unreachable", "error", errStr(err))
}

// logRollback flags a catalog whose index version went backwards — not applied,
// recorded for an operator (WARN).
func (e *Engine) logRollback(runID string, trig trigger, catalogID string, cursorVersion, indexVersion int64) {
	e.deps.Log.Warn("index version went backwards — ignored",
		"component", "crawl", "stage", "polled", "run_id", runID, "trigger", triggerStr(trig),
		"catalog_id", catalogID, "result", "rollback",
		"cursor_version", cursorVersion, "index_version", indexVersion)
}

// logQueued (DEBUG) records that the crawl enqueued one catalog: op is `sync`
// or `retire`, from/to are the version jump.
func (e *Engine) logQueued(runID string, trig trigger, catalogID, op string, from, to int64) {
	msg := fmt.Sprintf("queued this catalog to sync (v%d → v%d)", from, to)
	if op == "retire" {
		msg = "queued this catalog to retire"
	}
	e.deps.Log.Debug(msg,
		"component", "crawl", "stage", "queued", "run_id", runID, "trigger", triggerStr(trig),
		"catalog_id", catalogID, "op", op, "from", from, "to", to)
}

// logCrawlFinished closes one crawl tick with its tally (INFO).
func (e *Engine) logCrawlFinished(runID string, trig trigger, indexes, updated, queued int, dur time.Duration) {
	e.deps.Log.Info(fmt.Sprintf("crawl finished — polled %d index(es), %d updated, queued %d catalog(s)", indexes, updated, queued),
		"component", "crawl", "stage", "finished", "run_id", runID, "trigger", triggerStr(trig),
		"indexes", indexes, "updated", updated, "queued", queued, "dur_ms", dur.Milliseconds())
}

// logCrawlFailed fires when the source can't even be resolved — no refs to
// crawl this tick (ERROR). Only a scheduled tick resolves sources this way;
// an on-demand /crawl targets one URL directly and never hits this path.
func (e *Engine) logCrawlFailed(runID string, trig trigger, at string, err error) {
	e.deps.Log.Error("crawl failed to resolve its sources: "+errStr(err),
		"component", "crawl", "stage", "failed", "run_id", runID, "trigger", triggerStr(trig),
		"at", at, "error", errStr(err))
}

// --- sync (job 2: do the work) ----------------------------------------------

// logSyncing (DEBUG) marks the start of one catalog's sync.
func (e *Engine) logSyncing(runID, passID string, item *store.ClaimedItem) {
	msg := fmt.Sprintf("syncing catalog (v%d → v%d)", item.FromVersion, item.ToVersion)
	e.deps.Log.Debug(msg, syncKV("syncing", runID, passID, item)...)
}

// logSynced is the success terminal: the catalog landed in Discovery (DEBUG).
func (e *Engine) logSynced(runID, passID string, item *store.ClaimedItem, mode string, resources, offers, batches int) {
	kv := append(syncKV("synced", runID, passID, item),
		"mode", mode, "resources", resources, "offers", offers, "batches", batches)
	e.deps.Log.Debug("sent the catalog update to Discovery", kv...)
}

// logSkipped is the terminal for a claimed item with nothing to send (e.g. a
// removal-only change while removals are deferred) (DEBUG).
func (e *Engine) logSkipped(runID, passID string, item *store.ClaimedItem, reason string) {
	kv := append(syncKV("skipped", runID, passID, item), "reason", reason)
	e.deps.Log.Debug("nothing to send — this update only removed items, and removals aren't applied yet", kv...)
}

// logRetired is the terminal for a retire settle: recorded locally, Discovery
// not notified yet (Phase 2) (DEBUG).
func (e *Engine) logRetired(runID, passID string, item *store.ClaimedItem) {
	e.deps.Log.Debug("recorded the catalog as retired locally — Discovery not notified yet (Phase 2)",
		syncKV("retired", runID, passID, item)...)
}

// logFailed is the single failure terminal. It explains WHERE it broke (from
// the fault, via stepPhrase) and WHETHER it recovers (willRetry): WARN when the
// sync will retry, ERROR when it's parked (won't retry until a new version).
func (e *Engine) logFailed(runID, passID string, item *store.ClaimedItem, fc catalog.FaultClass, report store.PassReport, willRetry bool, attempt int) {
	step := stepPhrase(fc.String())
	detail := faultDetail(fc, report)
	var msg string
	switch {
	case !willRetry:
		msg = fmt.Sprintf("couldn't %s — %s; parked, won't retry until the publisher publishes a new version", step, detail)
	case attempt >= e.cfg.MaxAttempts:
		// Transient fault past the attempt budget: the failure is now recorded,
		// but the item keeps retrying with backoff (it is NOT parked).
		msg = fmt.Sprintf("couldn't %s — %s; still retrying after %d attempts (past the limit)", step, detail, attempt)
	default:
		msg = fmt.Sprintf("couldn't %s — %s; will retry (attempt %d of %d)", step, detail, attempt, e.cfg.MaxAttempts)
	}
	kv := append(syncKV("failed", runID, passID, item),
		"step", step, "fault", fc.String(), "will_retry", willRetry, "attempt", attempt)
	if report.HTTPStatus != 0 {
		kv = append(kv, "http_status", report.HTTPStatus)
	}
	kv = append(kv, "error", report.Reason)
	if willRetry {
		e.deps.Log.Warn(msg, kv...)
		return
	}
	e.deps.Log.Error(msg, kv...)
}

// logSyncFinished closes a sync tick that did work, with its tally (INFO).
func (e *Engine) logSyncFinished(runID string, synced, skipped, failed, retrying, queue int, dur time.Duration) {
	q := "queue empty"
	if queue != 0 {
		q = fmt.Sprintf("%d in queue", queue)
	}
	e.deps.Log.Info(fmt.Sprintf("sync finished — %d sent, %d skipped, %d failed, %d retrying; %s", synced, skipped, failed, retrying, q),
		"component", "sync", "stage", "finished", "run_id", runID,
		"synced", synced, "skipped", skipped, "failed", failed, "retrying", retrying,
		"queue", queue, "dur_ms", dur.Milliseconds())
}

// stepPhrase maps a fault class to the "couldn't <…>" phrase that says where a
// sync broke, per docs/crawler-logs.md §4.
func stepPhrase(fault string) string {
	switch fault {
	case "index_fetch", "absent":
		return "resolve the catalog"
	case "decode", "gap":
		return "unpack the files"
	case "digest_mismatch":
		return "verify the downloaded files"
	case "oversize":
		return "batch the catalog"
	case "store":
		return "save progress"
	case "content_invalid":
		return "build the push request"
	case "ssrf":
		return "download the files"
	default: // push_schema / push_rejected / transient (5xx) / anything else
		return "send the catalog to Discovery"
	}
}

// faultDetail is the short "what went wrong" clause after the em dash: the HTTP
// status when there is one, else the fault class.
func faultDetail(fc catalog.FaultClass, report store.PassReport) string {
	if report.HTTPStatus != 0 {
		return fmt.Sprintf("%d", report.HTTPStatus)
	}
	return fc.String()
}

// --- shared -----------------------------------------------------------------

// storeUnhealthy is the single collapsed event for every failed store op: the
// crawler's DB is momentarily unhealthy for `operation` (ERROR). component is
// the job that hit it ("crawl" or "sync"). operation uses the short vocab:
// read_index, advance_cadence, read_cursor, enqueue, upsert_index, claim,
// complete, park, reschedule, record.
func (e *Engine) storeUnhealthy(component, runID, operation, catalogID string, err error) {
	kv := []any{
		"component", component, "stage", "failed", "fault", "store",
		"run_id", runID, "operation", operation,
	}
	if catalogID != "" {
		kv = append(kv, "catalog_id", catalogID)
	}
	kv = append(kv, "error", errStr(err))
	e.deps.Log.Error("database error while "+operation+": "+errStr(err), kv...)
}
