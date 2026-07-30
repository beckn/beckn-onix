package runner

// crawl.go — the CRAWL job: resolve every configured source, crawl each index,
// and (when the index version advanced) decide + enqueue per catalog. A crawl
// *feeds* the work queue; the sync job (sync.go) drains it, one catalog per item.
//
// Two levels read top to bottom: crawlIndex is the per-index recipe (load state
// → cadence gate → conditional fetch → process → record); decideCatalog is the
// per-catalog decision (scope → cursor → detect change → route).

import (
	"context"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/source"
)

// trigger says why a crawl runs. onDemand (the /crawl endpoint) bypasses the
// per-index next_crawl_at cadence gate; scheduled (a ticker) honours it.
type trigger int

const (
	scheduled trigger = iota
	onDemand
)

// crawlResult is what one crawlIndex reports back so indexPass can tally the
// heartbeat: whether the index was actually fetched (fetched — the conditional
// GET was made, incl. a 304), whether its version advanced (changed), how many
// catalogs were enqueued, and whether a store op failed on the way (degraded).
//
// degraded is the crawl job's "I could not do my job" flag. It is set by STORE
// failures only — the crawler's own state is unreachable, so this index was
// neither fully decided nor fully recorded. A publisher we could not reach is
// NOT degraded: that is the remote's fault, it is already counted by
// RecordIndexPoll("unreachable"), and it must not blank the crawler's own
// liveness signal. This is the same line sync.go draws, where only a failed
// ClaimNext (the store) aborts the pass while a failed push does not.
type crawlResult struct {
	fetched  bool
	changed  bool
	enqueued int
	degraded bool
}

// indexPass resolves every configured source and crawls each index. It mints
// one run_id for the whole tick and emits the crawl finished summary.
//
// MarkPassSuccess is withheld when any step could not reach the store. Same
// rule as catalogPass on a failed claim: "a queue we can't claim from is
// exactly the wedged state that crawler_seconds_since_last_success must keep
// reporting". A store the crawl job cannot read its index cursors from is that
// same wedged state — every crawlIndex fails at GetIndex, nothing is polled,
// nothing is queued, and marking the pass successful would keep the liveness
// gauge fresh while the crawler does nothing at all.
//
// A tick with nothing to do IS successful: no configured sources, or every
// index still inside its next_crawl_at cadence gate, leaves degraded false and
// marks the pass. Idle is not wedged.
func (e *Engine) indexPass(ctx context.Context) {
	runID := e.newID()
	start := e.deps.Now()
	refs, err := e.deps.Source.IndexRefs(ctx)
	if err != nil {
		e.logCrawlFailed(runID, scheduled, "source_resolve", err)
		return
	}
	refs, degraded := e.withPersistedIndexes(ctx, refs, runID)
	var fetched, changed, enqueued int
	for _, ref := range refs {
		if ctx.Err() != nil {
			return
		}
		r := e.crawlIndex(ctx, ref, scheduled, runID)
		if r.fetched {
			fetched++
		}
		if r.changed {
			changed++
		}
		enqueued += r.enqueued
		degraded = degraded || r.degraded
	}
	e.logCrawlFinished(runID, scheduled, fetched, changed, enqueued, e.deps.Now().Sub(start))
	if degraded {
		return // the store failed somewhere in this pass; it was not a successful tick
	}
	e.deps.Metrics.MarkPassSuccess("crawl")
}

// withPersistedIndexes unions the configured/registry refs with every index
// already recorded in crawler_index (deduped by URL, configured refs win) — so
// an index crawled once on demand keeps being re-polled on the normal cadence
// and picks up later change files with no second /crawl. Each is still gated by
// its own next_crawl_at in crawlIndex. A store error here does not abort the
// pass — it is logged and the pass proceeds with just the given refs — but it
// IS reported as degraded, because a listing we could not read means some
// known index was silently dropped from this tick.
func (e *Engine) withPersistedIndexes(ctx context.Context, refs []source.IndexRef, runID string) ([]source.IndexRef, bool) {
	seen := make(map[string]bool, len(refs))
	for _, r := range refs {
		seen[r.IndexURL] = true
	}
	known, err := e.deps.Store.KnownIndexes(ctx)
	if err != nil {
		e.storeUnhealthy("crawl", runID, "list_indexes", "", err)
		return refs, true
	}
	for _, k := range known {
		if seen[k.IndexURL] {
			continue
		}
		seen[k.IndexURL] = true
		refs = append(refs, source.IndexRef{
			IndexURL:      k.IndexURL,
			ParticipantID: k.ParticipantID,
			Source:        k.Source,
		})
	}
	return refs, false
}

// crawlIndex crawls one publisher index. The steps read top to bottom:
//
//	take the index lock → load stored state → cadence gate → conditional fetch
//	→ (304? done) → process (decide + enqueue if the version advanced)
//	→ record new state
//
// The lock is per index URL: an on-demand /crawl and the scheduled ticker can
// otherwise read the same index's version cursor, both decide it advanced, and
// both write next_crawl_at — a lost update. Crawls of other indexes are not
// blocked.
//
// Record is conditional on process having COMPLETED. processIndex reports
// whether every catalog in the index was decided and enqueued; only then is the
// new version recorded as a finished pass. See recordIndex for why a partial
// pass must not be written as a complete one.
func (e *Engine) crawlIndex(ctx context.Context, ref source.IndexRef, trig trigger, runID string) crawlResult {
	release, ok := e.lockIndex(ctx, ref.IndexURL)
	if !ok {
		return crawlResult{} // stopping before we got the index lock
	}
	defer release()

	prev, err := e.deps.Store.GetIndex(ctx, ref.IndexURL)
	if err != nil {
		e.storeUnhealthy("crawl", runID, "read_index", "", err)
		return crawlResult{degraded: true}
	}
	now := e.deps.Now()
	if !e.dueForCrawl(prev, trig, now) {
		return crawlResult{} // not due yet (per-index cadence via next_crawl_at)
	}

	res, err := e.deps.FetchIndex(ctx, ref.IndexURL, conditionsFrom(prev))
	if err != nil {
		e.logPollFailed(runID, trig, ref.IndexURL, err)
		e.deps.Metrics.RecordIndexPoll("unreachable")
		return crawlResult{}
	}
	if res.NotModified {
		return e.indexUnchanged(ctx, ref, trig, runID, now)
	}

	out := e.processIndex(ctx, ref, trig, prev, res.Index, runID)
	if !e.recordIndex(ctx, ref, prev, res, !out.degraded, runID, now) {
		out.degraded = true
	}
	return out
}

// dueForCrawl reports whether this index should be crawled now: an on-demand
// trigger always is; a scheduled one honours the per-index next_crawl_at gate.
func (e *Engine) dueForCrawl(prev *catalog.IndexState, trig trigger, now time.Time) bool {
	if trig == onDemand || prev == nil || prev.NextCrawlAt.IsZero() {
		return true
	}
	return !prev.NextCrawlAt.After(now)
}

// conditionsFrom builds the conditional-GET validators from the last stored
// crawl — both empty (an unconditional GET) when we've never fetched this index.
func conditionsFrom(prev *catalog.IndexState) catalog.IndexConditions {
	if prev == nil {
		return catalog.IndexConditions{}
	}
	return catalog.IndexConditions{ETag: prev.ETag, LastModified: prev.LastModified}
}

// indexUnchanged handles a 304 Not Modified: the host confirmed nothing changed
// — no body downloaded, no parse. Just advance the crawl cadence and record the
// (cheap) crawl duration.
func (e *Engine) indexUnchanged(ctx context.Context, ref source.IndexRef, trig trigger, runID string, since time.Time) crawlResult {
	e.logPolled(runID, trig, ref.IndexURL, 0, "not_modified")
	e.deps.Metrics.RecordIndexPoll("not_modified")
	res := crawlResult{fetched: true}
	if err := e.deps.Store.AdvanceIndexCadence(ctx, ref.IndexURL, e.nextIndexCrawl()); err != nil {
		e.storeUnhealthy("crawl", runID, "advance_cadence", "", err)
		res.degraded = true
	}
	e.deps.Metrics.ObserveIndexSeconds(e.deps.Now().Sub(since).Seconds())
	return res
}

// processIndex handles a freshly-fetched index body: it always counts as
// checked, and when the version advanced it decides + enqueues per catalog.
// A same-version index is logged and left for the per-catalog cursors + queue.
//
// It sets degraded when ANY catalog in the index could not be decided or
// enqueued because the store failed. That flag is the caller's signal that this
// was a PARTIAL pass, not a complete one.
func (e *Engine) processIndex(ctx context.Context, ref source.IndexRef, trig trigger, prev *catalog.IndexState, idx catalog.Index, runID string) crawlResult {
	out := crawlResult{fetched: true}
	if prev != nil && prev.IndexVersion == idx.Version {
		e.logPolled(runID, trig, ref.IndexURL, idx.Version, "unchanged")
		e.deps.Metrics.RecordIndexPoll("unchanged")
		return out
	}
	e.logPolled(runID, trig, ref.IndexURL, idx.Version, "updated")
	e.deps.Metrics.RecordIndexPoll("updated")
	out.changed = true
	out.enqueued, out.degraded = e.decideCatalogs(ctx, ref, trig, idx, runID)
	return out
}

// recordIndex persists the crawled index's new state — version, participant,
// conditional-GET validators, and the next crawl time — and records the crawl
// duration. It reports whether the write itself succeeded.
//
// complete says every catalog in this index was decided and enqueued. When it
// is false the pass was PARTIAL, and the new version and the conditional-GET
// validators are BOTH withheld:
//
//   - Withholding the version keeps the change gate open. Recording it would
//     close the gate on work that was never queued: the next pass reads
//     prev.IndexVersion == idx.Version, calls the index unchanged, and skips
//     decideCatalogs entirely. The catalog whose Enqueue failed then stays at
//     its old version until the publisher happens to bump the index again,
//     which can be days.
//   - Withholding the validators keeps the next poll a real GET. A stored ETag
//     turns every later poll into a 304, and the 304 path never decides
//     anything either, so a recorded validator would wedge the same update shut
//     even harder.
//
// The row is still written, with the PREVIOUS version and a non-OK sync status,
// so the failed pass is visible to an operator and next_crawl_at still advances
// (one retry per cadence, not a hot loop).
func (e *Engine) recordIndex(ctx context.Context, ref source.IndexRef, prev *catalog.IndexState, res catalog.IndexResult, complete bool, runID string, since time.Time) bool {
	idx := res.Index
	version, status, etag, lastModified := idx.Version, publish.SyncOK, res.ETag, res.LastModified
	if !complete {
		version, status, etag, lastModified = storedVersion(prev), publish.SyncFailed, "", ""
	}
	ok := true
	if err := e.deps.Store.UpsertIndex(ctx, ref.IndexURL, idx.ParticipantID, ref.Source, version, status, e.nextIndexCrawl(), etag, lastModified); err != nil {
		e.storeUnhealthy("crawl", runID, "upsert_index", "", err)
		ok = false
	}
	e.deps.Metrics.ObserveIndexSeconds(e.deps.Now().Sub(since).Seconds())
	return ok
}

// storedVersion is the index version already on record — 0 when this index has
// never been crawled, which keeps the change gate open against any real
// publisher version.
func storedVersion(prev *catalog.IndexState) int64 {
	if prev == nil {
		return 0
	}
	return prev.IndexVersion
}

// decideCatalogs walks an advanced index and, per catalog, decides + enqueues.
// It returns how many catalogs were enqueued (for the crawl finished tally) and
// whether any catalog failed on a store op, so the caller can withhold the
// index version advance rather than record a partial pass as a complete one.
func (e *Engine) decideCatalogs(ctx context.Context, ref source.IndexRef, trig trigger, idx catalog.Index, runID string) (enqueued int, failed bool) {
	for _, entry := range idx.Catalogs {
		queued, err := e.decideCatalog(ctx, ref, trig, entry, runID)
		if queued {
			enqueued++
		}
		if err {
			failed = true
		}
	}
	return enqueued, failed
}

// decideCatalog decides what to do with ONE catalog in an advanced index and
// acts on it: scope it, read its cursor, detect the change, then route — enqueue
// a sync (if in this crawler's networks), enqueue a retire (if we ever had it),
// flag a rollback, or skip.
//
// It returns two independent facts. queued says work reached the queue (the
// crawl finished tally). failed says a store op failed, so this catalog's
// update was NOT queued and the caller must not let the index version advance
// past it — logging storeUnhealthy and returning a bare false is what let a
// mid-pass store blip silently drop a catalog update for a whole publish cycle.
//
// A decision that legitimately queues nothing (out of scope, nothing to retire,
// a rollback, an unchanged catalog) returns queued=false, failed=false: there
// is nothing lost, so the index version may advance.
func (e *Engine) decideCatalog(ctx context.Context, ref source.IndexRef, trig trigger, entry catalog.CatalogEntry, runID string) (queued, failed bool) {
	take, _ := catalog.ResolveScope(entry, e.cfg.Networks)
	cursor, seen, err := e.deps.Store.GetCatalogVersion(ctx, entry.CatalogID)
	if err != nil {
		// We do not know this catalog's cursor, so we cannot know whether it
		// needed work. Treat it as unfinished, never as "nothing to do".
		e.storeUnhealthy("crawl", runID, "read_cursor", entry.CatalogID, err)
		return false, true
	}
	d := catalog.DetectChange(entry, cursor, seen)
	switch d.Action {
	case catalog.ActionSync:
		if !take {
			return false, false // not for this crawler's networks
		}
		if err := e.deps.Store.Enqueue(ctx, catalog.QueueItem{
			CatalogID: entry.CatalogID, IndexURL: ref.IndexURL,
			FromVersion: cursor, ToVersion: d.ToVersion, Op: "sync",
		}); err != nil {
			e.storeUnhealthy("crawl", runID, "enqueue", entry.CatalogID, err)
			return false, true
		}
		e.logQueued(runID, trig, entry.CatalogID, "sync", cursor, d.ToVersion)
		return true, false
	case catalog.ActionRetire:
		if !seen {
			return false, false // never had it; nothing to retire
		}
		if err := e.deps.Store.Enqueue(ctx, catalog.QueueItem{
			CatalogID: entry.CatalogID, IndexURL: ref.IndexURL, ToVersion: cursor, Op: "retire",
		}); err != nil {
			e.storeUnhealthy("crawl", runID, "enqueue", entry.CatalogID, err)
			return false, true
		}
		e.logQueued(runID, trig, entry.CatalogID, "retire", cursor, cursor)
		return true, false
	case catalog.ActionRollback:
		e.logRollback(runID, trig, entry.CatalogID, cursor, d.ToVersion)
	case catalog.ActionSkipUnchanged:
		// nothing to do
	}
	return false, false
}

func (e *Engine) nextIndexCrawl() time.Time {
	if e.cfg.IndexInterval <= 0 {
		return time.Time{}
	}
	return e.deps.Now().Add(e.cfg.IndexInterval)
}
