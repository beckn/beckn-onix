package runner

// crawl.go — the index job: resolve every configured source, crawl each
// index, and (when the index version advanced) decide + enqueue per catalog.
// An index crawl *feeds* the queue; the Catalog Sync (sync.go) drains it.

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
// heartbeat: whether the index was actually fetched (checked), whether its
// version advanced (changed), how many catalogs were enqueued, and the terminal
// IndexOutcome.
type crawlResult struct {
	outcome  IndexOutcome
	checked  bool
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
		e.logIndexPassFailed(runID, "source_resolve", err)
		return
	}
	var checked, changed, enqueued int
	for _, ref := range refs {
		if ctx.Err() != nil {
			return
		}
		r := e.crawlIndex(ctx, ref, scheduled, runID)
		if r.checked {
			checked++
		}
		if r.changed {
			changed++
		}
		enqueued += r.enqueued
	}
	e.logIndexPassCompleted(runID, checked, changed, enqueued, e.deps.Now().Sub(start))
}

// crawlIndex fetches one index and, for each catalog, decides + enqueues.
func (e *Engine) crawlIndex(ctx context.Context, ref source.IndexRef, trig trigger, runID string) crawlResult {
	prev, err := e.deps.Store.GetIndex(ctx, ref.IndexURL)
	if err != nil {
		e.storeUnhealthy(runID, "read_index", "", err)
		return crawlResult{outcome: IndexFailed}
	}
	now := e.deps.Now()
	if trig != onDemand && prev != nil && !prev.NextCrawlAt.IsZero() && prev.NextCrawlAt.After(now) {
		return crawlResult{} // not due yet (per-index cadence via next_crawl_at)
	}

	cond := catalog.IndexConditions{}
	if prev != nil {
		cond = catalog.IndexConditions{ETag: prev.ETag, LastModified: prev.LastModified}
	}
	res, err := e.deps.FetchIndex(ctx, ref.IndexURL, cond)
	if err != nil {
		e.logIndexFetchFailed(runID, ref.IndexURL, catalog.ClassifyFault(0, err), err)
		return crawlResult{outcome: IndexFailed}
	}
	if res.NotModified {
		// 304 Not Modified: the host confirmed nothing changed — no body
		// downloaded, no parse. Just advance the crawl cadence.
		e.logIndexNotModified(runID, ref.IndexURL)
		if err := e.deps.Store.AdvanceIndexCadence(ctx, ref.IndexURL, e.nextIndexCrawl()); err != nil {
			e.storeUnhealthy(runID, "advance_cadence", "", err)
		}
		e.deps.Metrics.ObserveIndexSeconds(e.deps.Now().Sub(now).Seconds())
		return crawlResult{outcome: IndexUnchanged, checked: true}
	}
	idx := res.Index
	e.logIndexChecked(runID, ref.IndexURL, idx.Version)

	out := crawlResult{outcome: IndexUnchanged, checked: true}
	// Process only when the index version advanced; per-catalog cursors + the
	// queue handle any still-pending work independently.
	if prev != nil && prev.IndexVersion == idx.Version {
		e.logIndexUnchanged(runID, ref.IndexURL, idx.Version)
	} else {
		out.changed = true
		out.enqueued = e.decideCatalogs(ctx, ref, idx, runID)
		if out.enqueued > 0 {
			out.outcome = IndexEnqueued
		}
	}

	if err := e.deps.Store.UpsertIndex(ctx, ref.IndexURL, idx.ParticipantID, ref.Source, idx.Version, publish.SyncOK, e.nextIndexCrawl(), res.ETag, res.LastModified); err != nil {
		e.storeUnhealthy(runID, "upsert_index", "", err)
	}
	e.deps.Metrics.ObserveIndexSeconds(e.deps.Now().Sub(now).Seconds())
	return out
}

// decideCatalogs walks an advanced index and, per catalog, decides + enqueues.
// It returns how many catalogs were enqueued (for the index_pass tally).
func (e *Engine) decideCatalogs(ctx context.Context, ref source.IndexRef, idx catalog.Index, runID string) int {
	enqueued := 0
	for _, entry := range idx.Catalogs {
		take, _ := catalog.ResolveScope(entry, e.cfg.Networks)
		cursor, seen, err := e.deps.Store.GetCatalogVersion(ctx, entry.CatalogID)
		if err != nil {
			e.storeUnhealthy(runID, "read_cursor", entry.CatalogID, err)
			continue
		}
		d := catalog.DetectChange(entry, cursor, seen)
		e.logCatalogDecided(runID, entry.CatalogID, string(d.Action), cursor, d.ToVersion)
		switch d.Action {
		case catalog.ActionSync:
			if !take {
				continue // not for this crawler's networks
			}
			if err := e.deps.Store.Enqueue(ctx, store.QueueItem{
				CatalogID: entry.CatalogID, IndexURL: ref.IndexURL,
				FromVersion: cursor, ToVersion: d.ToVersion, Op: "sync",
			}); err != nil {
				e.storeUnhealthy(runID, "enqueue", entry.CatalogID, err)
				continue
			}
			enqueued++
			e.logCatalogEnqueued(runID, entry.CatalogID, d.ToVersion)
		case catalog.ActionRetire:
			if !seen {
				continue // never had it; nothing to retire
			}
			if err := e.deps.Store.Enqueue(ctx, store.QueueItem{
				CatalogID: entry.CatalogID, IndexURL: ref.IndexURL, ToVersion: cursor, Op: "retire",
			}); err != nil {
				e.storeUnhealthy(runID, "enqueue", entry.CatalogID, err)
				continue
			}
			enqueued++
			e.logCatalogRetireEnqueued(runID, entry.CatalogID)
		case catalog.ActionRollback:
			e.logIndexRollback(runID, entry.CatalogID, cursor, d.ToVersion)
		case catalog.ActionSkipUnchanged:
			// nothing to do
		}
	}
	return enqueued
}

func (e *Engine) nextIndexCrawl() time.Time {
	if e.cfg.IndexInterval <= 0 {
		return time.Time{}
	}
	return e.deps.Now().Add(e.cfg.IndexInterval)
}
