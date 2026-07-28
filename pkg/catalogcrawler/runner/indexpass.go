package runner

import (
	"context"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/publish"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/source"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/store"
)

// trigger says why a crawl runs. onDemand (the /crawl endpoint) bypasses the
// per-index next_crawl_at cadence gate; scheduled (a ticker) honours it.
type trigger int

const (
	scheduled trigger = iota
	onDemand
)

// indexPass resolves every configured source and crawls each index.
func (e *Engine) indexPass(ctx context.Context) {
	refs, err := e.deps.Source.IndexRefs(ctx)
	if err != nil {
		e.deps.Log.Error("crawler.source.failed", "err", err)
		return
	}
	for _, ref := range refs {
		if ctx.Err() != nil {
			return
		}
		e.crawlIndex(ctx, ref, scheduled)
	}
}

// crawlIndex fetches one index and, for each catalog, decides + enqueues.
func (e *Engine) crawlIndex(ctx context.Context, ref source.IndexRef, trig trigger) {
	prev, err := e.deps.Store.GetIndex(ctx, ref.IndexURL)
	if err != nil {
		e.deps.Log.Error("crawler.index.state_failed", "indexUrl", ref.IndexURL, "err", err)
		return
	}
	now := e.deps.Now()
	if trig != onDemand && prev != nil && !prev.NextCrawlAt.IsZero() && prev.NextCrawlAt.After(now) {
		return // not due yet (per-index cadence via next_crawl_at)
	}

	cond := catalog.IndexConditions{}
	if prev != nil {
		cond = catalog.IndexConditions{ETag: prev.ETag, LastModified: prev.LastModified}
	}
	res, err := e.deps.FetchIndex(ctx, ref.IndexURL, cond)
	if err != nil {
		e.deps.Log.Warn("crawler.index.fetch_failed", "indexUrl", ref.IndexURL, "err", err)
		return
	}
	if res.NotModified {
		// 304 Not Modified: the host confirmed nothing changed — no body
		// downloaded, no parse. Just advance the crawl cadence.
		e.deps.Log.Info("crawler.index.not_modified", "indexUrl", ref.IndexURL)
		if err := e.deps.Store.AdvanceIndexCadence(ctx, ref.IndexURL, e.nextIndexCrawl()); err != nil {
			e.deps.Log.Error("crawler.index.touch_failed", "indexUrl", ref.IndexURL, "err", err)
		}
		e.deps.Metrics.ObserveIndexSeconds(e.deps.Now().Sub(now).Seconds())
		return
	}
	idx := res.Index
	e.deps.Log.Info("crawler.index.checked", "indexUrl", ref.IndexURL, "version", idx.Version)

	// Process only when the index version advanced; per-catalog cursors + the
	// queue handle any still-pending work independently.
	if prev != nil && prev.IndexVersion == idx.Version {
		e.deps.Log.Info("crawler.index.unchanged", "indexUrl", ref.IndexURL, "version", idx.Version)
	} else {
		for _, entry := range idx.Catalogs {
			take, _ := catalog.Select(entry, e.cfg.Networks)
			cursor, seen, err := e.deps.Store.GetCatalogVersion(ctx, entry.CatalogID)
			if err != nil {
				e.deps.Log.Error("crawler.catalog.state_failed", "catalogId", entry.CatalogID, "err", err)
				continue
			}
			switch d := catalog.Decide(entry, cursor, seen); d.Action {
			case catalog.ActionSync:
				if !take {
					continue // not for this crawler's networks
				}
				if err := e.deps.Store.Enqueue(ctx, store.QueueItem{
					CatalogID: entry.CatalogID, IndexURL: ref.IndexURL,
					FromVersion: cursor, ToVersion: d.ToVersion, Op: "sync",
				}); err != nil {
					e.deps.Log.Error("crawler.enqueue_failed", "catalogId", entry.CatalogID, "err", err)
					continue
				}
				e.deps.Log.Info("crawler.catalog.enqueued", "catalogId", entry.CatalogID, "toVersion", d.ToVersion)
			case catalog.ActionRetire:
				if !seen {
					continue // never had it; nothing to retire
				}
				if err := e.deps.Store.Enqueue(ctx, store.QueueItem{
					CatalogID: entry.CatalogID, IndexURL: ref.IndexURL, ToVersion: cursor, Op: "retire",
				}); err != nil {
					e.deps.Log.Error("crawler.enqueue_failed", "catalogId", entry.CatalogID, "err", err)
					continue
				}
				e.deps.Log.Info("crawler.catalog.retire_enqueued", "catalogId", entry.CatalogID)
			case catalog.ActionRollback:
				e.deps.Log.Warn("crawler.catalog.rollback", "catalogId", entry.CatalogID, "cursor", cursor, "indexVersion", d.ToVersion)
			case catalog.ActionSkipUnchanged:
				// nothing to do
			}
		}
	}

	if err := e.deps.Store.UpsertIndex(ctx, ref.IndexURL, idx.ParticipantID, ref.Source, idx.Version, publish.SyncOK, e.nextIndexCrawl(), res.ETag, res.LastModified); err != nil {
		e.deps.Log.Error("crawler.index.record_failed", "indexUrl", ref.IndexURL, "err", err)
	}
	e.deps.Metrics.ObserveIndexSeconds(e.deps.Now().Sub(now).Seconds())
}

func (e *Engine) nextIndexCrawl() time.Time {
	if e.cfg.IndexInterval <= 0 {
		return time.Time{}
	}
	return e.deps.Now().Add(e.cfg.IndexInterval)
}
