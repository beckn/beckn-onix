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

	"github.com/beckn-one/beckn-onix/pkg/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/crawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/crawler/source"
	"github.com/beckn-one/beckn-onix/pkg/crawler/store"
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
// GET was made, incl. a 304), whether its version advanced (changed), and how
// many catalogs were enqueued.
type crawlResult struct {
	fetched  bool
	changed  bool
	enqueued int
}

// indexPass resolves every configured source and crawls each index. It mints
// one run_id for the whole tick and emits the index_pass heartbeat.
func (e *Engine) indexPass(ctx context.Context) {
	runID := e.newID()
	start := e.deps.Now()
	refs, err := e.deps.Source.IndexRefs(ctx)
	if err != nil {
		e.logCrawlFailed(runID, "source_resolve", err)
		return
	}
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
	}
	e.logCrawlFinished(runID, fetched, changed, enqueued, e.deps.Now().Sub(start))
}

// crawlIndex crawls one publisher index. The steps read top to bottom:
//
//	load stored state → cadence gate → conditional fetch → (304? done)
//	→ process (decide + enqueue if the version advanced) → record new state
func (e *Engine) crawlIndex(ctx context.Context, ref source.IndexRef, trig trigger, runID string) crawlResult {
	prev, err := e.deps.Store.GetIndex(ctx, ref.IndexURL)
	if err != nil {
		e.storeUnhealthy("crawl", runID, "read_index", "", err)
		return crawlResult{}
	}
	now := e.deps.Now()
	if !e.dueForCrawl(prev, trig, now) {
		return crawlResult{} // not due yet (per-index cadence via next_crawl_at)
	}

	res, err := e.deps.FetchIndex(ctx, ref.IndexURL, conditionsFrom(prev))
	if err != nil {
		e.logPollFailed(runID, ref.IndexURL, err)
		return crawlResult{}
	}
	if res.NotModified {
		return e.indexUnchanged(ctx, ref, runID, now)
	}

	out := e.processIndex(ctx, ref, prev, res.Index, runID)
	e.recordIndex(ctx, ref, res, runID, now)
	return out
}

// dueForCrawl reports whether this index should be crawled now: an on-demand
// trigger always is; a scheduled one honours the per-index next_crawl_at gate.
func (e *Engine) dueForCrawl(prev *store.IndexState, trig trigger, now time.Time) bool {
	if trig == onDemand || prev == nil || prev.NextCrawlAt.IsZero() {
		return true
	}
	return !prev.NextCrawlAt.After(now)
}

// conditionsFrom builds the conditional-GET validators from the last stored
// crawl — both empty (an unconditional GET) when we've never fetched this index.
func conditionsFrom(prev *store.IndexState) catalog.IndexConditions {
	if prev == nil {
		return catalog.IndexConditions{}
	}
	return catalog.IndexConditions{ETag: prev.ETag, LastModified: prev.LastModified}
}

// indexUnchanged handles a 304 Not Modified: the host confirmed nothing changed
// — no body downloaded, no parse. Just advance the crawl cadence and record the
// (cheap) crawl duration.
func (e *Engine) indexUnchanged(ctx context.Context, ref source.IndexRef, runID string, since time.Time) crawlResult {
	e.logPolled(runID, ref.IndexURL, 0, "not_modified")
	if err := e.deps.Store.AdvanceIndexCadence(ctx, ref.IndexURL, e.nextIndexCrawl()); err != nil {
		e.storeUnhealthy("crawl", runID, "advance_cadence", "", err)
	}
	e.deps.Metrics.ObserveIndexSeconds(e.deps.Now().Sub(since).Seconds())
	return crawlResult{fetched: true}
}

// processIndex handles a freshly-fetched index body: it always counts as
// checked, and when the version advanced it decides + enqueues per catalog.
// A same-version index is logged and left for the per-catalog cursors + queue.
func (e *Engine) processIndex(ctx context.Context, ref source.IndexRef, prev *store.IndexState, idx catalog.Index, runID string) crawlResult {
	out := crawlResult{fetched: true}
	if prev != nil && prev.IndexVersion == idx.Version {
		e.logPolled(runID, ref.IndexURL, idx.Version, "unchanged")
		return out
	}
	e.logPolled(runID, ref.IndexURL, idx.Version, "updated")
	out.changed = true
	out.enqueued = e.decideCatalogs(ctx, ref, idx, runID)
	return out
}

// recordIndex persists the crawled index's new state — version, participant,
// conditional-GET validators, and the next crawl time — and records the crawl
// duration.
func (e *Engine) recordIndex(ctx context.Context, ref source.IndexRef, res catalog.IndexResult, runID string, since time.Time) {
	idx := res.Index
	if err := e.deps.Store.UpsertIndex(ctx, ref.IndexURL, idx.ParticipantID, ref.Source, idx.Version, publish.SyncOK, e.nextIndexCrawl(), res.ETag, res.LastModified); err != nil {
		e.storeUnhealthy("crawl", runID, "upsert_index", "", err)
	}
	e.deps.Metrics.ObserveIndexSeconds(e.deps.Now().Sub(since).Seconds())
}

// decideCatalogs walks an advanced index and, per catalog, decides + enqueues.
// It returns how many catalogs were enqueued (for the index_pass tally).
func (e *Engine) decideCatalogs(ctx context.Context, ref source.IndexRef, idx catalog.Index, runID string) int {
	enqueued := 0
	for _, entry := range idx.Catalogs {
		if e.decideCatalog(ctx, ref, entry, runID) {
			enqueued++
		}
	}
	return enqueued
}

// decideCatalog decides what to do with ONE catalog in an advanced index and
// acts on it: scope it, read its cursor, detect the change, then route — enqueue
// a sync (if in this crawler's networks), enqueue a retire (if we ever had it),
// flag a rollback, or skip. It reports whether it enqueued work.
func (e *Engine) decideCatalog(ctx context.Context, ref source.IndexRef, entry catalog.CatalogEntry, runID string) bool {
	take, _ := catalog.ResolveScope(entry, e.cfg.Networks)
	cursor, seen, err := e.deps.Store.GetCatalogVersion(ctx, entry.CatalogID)
	if err != nil {
		e.storeUnhealthy("crawl", runID, "read_cursor", entry.CatalogID, err)
		return false
	}
	d := catalog.DetectChange(entry, cursor, seen)
	switch d.Action {
	case catalog.ActionSync:
		if !take {
			return false // not for this crawler's networks
		}
		if err := e.deps.Store.Enqueue(ctx, store.QueueItem{
			CatalogID: entry.CatalogID, IndexURL: ref.IndexURL,
			FromVersion: cursor, ToVersion: d.ToVersion, Op: "sync",
		}); err != nil {
			e.storeUnhealthy("crawl", runID, "enqueue", entry.CatalogID, err)
			return false
		}
		e.logQueued(runID, entry.CatalogID, "sync", cursor, d.ToVersion)
		return true
	case catalog.ActionRetire:
		if !seen {
			return false // never had it; nothing to retire
		}
		if err := e.deps.Store.Enqueue(ctx, store.QueueItem{
			CatalogID: entry.CatalogID, IndexURL: ref.IndexURL, ToVersion: cursor, Op: "retire",
		}); err != nil {
			e.storeUnhealthy("crawl", runID, "enqueue", entry.CatalogID, err)
			return false
		}
		e.logQueued(runID, entry.CatalogID, "retire", cursor, cursor)
		return true
	case catalog.ActionRollback:
		e.logRollback(runID, entry.CatalogID, cursor, d.ToVersion)
	case catalog.ActionSkipUnchanged:
		// nothing to do
	}
	return false
}

func (e *Engine) nextIndexCrawl() time.Time {
	if e.cfg.IndexInterval <= 0 {
		return time.Time{}
	}
	return e.deps.Now().Add(e.cfg.IndexInterval)
}
