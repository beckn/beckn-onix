package catalogcrawler

import (
	"context"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/state"
)

// Logger is the minimal structured-log sink the engine needs. It is
// injected (no process-global logger), so an onix plugin passes its own.
type Logger interface {
	Info(event string, kv ...any)
	Warn(event string, kv ...any)
	Error(event string, kv ...any)
}

// NopLogger discards all events (handy default / for tests).
type NopLogger struct{}

func (NopLogger) Info(string, ...any)  {}
func (NopLogger) Warn(string, ...any)  {}
func (NopLogger) Error(string, ...any) {}

// IndexFetcher fetches and parses a publisher's catalog index.
type IndexFetcher func(ctx context.Context, indexURL string) (Index, error)

// Validator schema-validates the /push request body before it is sent
// (Phase 1; reuses onix's schemav2validator against the catalog/publish
// action, which keys on the request path). A nil error means valid.
type Validator func(ctx context.Context, pushBody []byte) error

// Pusher pushes one /push body to Discovery and reports the outcome.
type Pusher func(ctx context.Context, body []byte) (PartOutcome, error)

// FileFetcher fetches + verifies one catalog file. It is context-aware (for
// real HTTP); Resolve wraps it into a ctx-free FetchFunc per catalog.
type FileFetcher func(ctx context.Context, f FileEntry) ([]byte, error)

// Config is the engine's tunables (all config-driven, no hardcodes).
type Config struct {
	Networks        []string      // crawler's networkIds (selection + on-demand default)
	BppURI          string        // publisher URI for the push context
	IndexInterval   time.Duration // index-job cadence
	CatalogInterval time.Duration // catalog-job cadence
	MaxAttempts     int           // give up a catalog after this many failed pushes
}

// Deps are the engine's injected collaborators.
type Deps struct {
	Store      *state.Store
	Source     Source
	FetchIndex IndexFetcher
	FetchFile  FileFetcher
	Validate   Validator
	Push       Pusher
	Log        Logger
	Now        func() time.Time
	NewID      func() string
}

// Engine runs the two scheduled jobs (index + catalog) linked by the queue.
type Engine struct {
	cfg  Config
	deps Deps
	wg   sync.WaitGroup
	stop context.CancelFunc
}

// New builds an Engine, filling in sane defaults for optional deps.
func New(cfg Config, deps Deps) *Engine {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Log == nil {
		deps.Log = NopLogger{}
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	return &Engine{cfg: cfg, deps: deps}
}

// Start launches the index and catalog jobs as two goroutines. It returns
// immediately; Stop() drains them.
func (e *Engine) Start(ctx context.Context) error {
	ctx, e.stop = context.WithCancel(ctx)
	e.loop(ctx, e.cfg.IndexInterval, e.indexPass)
	e.loop(ctx, e.cfg.CatalogInterval, e.catalogPass)
	return nil
}

// Stop signals both jobs and waits for the in-flight pass to drain.
func (e *Engine) Stop() error {
	if e.stop != nil {
		e.stop()
	}
	e.wg.Wait()
	return nil
}

// CrawlNow runs an immediate index crawl for one index URL (the /crawl
// supportability trigger).
func (e *Engine) CrawlNow(ctx context.Context, indexURL string) error {
	e.crawlIndex(ctx, IndexRef{IndexURL: indexURL, Source: SourceOnDemand})
	return nil
}

// loop runs fn once immediately, then every interval until ctx is done.
func (e *Engine) loop(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		fn(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fn(ctx)
			}
		}
	}()
}

// --- index job ---

// indexPass resolves every configured source and crawls each index.
func (e *Engine) indexPass(ctx context.Context) {
	refs, err := e.deps.Source.IndexURLs(ctx)
	if err != nil {
		e.deps.Log.Error("crawler.source.failed", "err", err)
		return
	}
	for _, ref := range refs {
		if ctx.Err() != nil {
			return
		}
		e.crawlIndex(ctx, ref)
	}
}

// crawlIndex fetches one index and, for each catalog, decides + enqueues.
func (e *Engine) crawlIndex(ctx context.Context, ref IndexRef) {
	idx, err := e.deps.FetchIndex(ctx, ref.IndexURL)
	if err != nil {
		e.deps.Log.Warn("crawler.index.fetch_failed", "indexUrl", ref.IndexURL, "err", err)
		return
	}
	e.deps.Log.Info("crawler.index.checked", "indexUrl", ref.IndexURL, "version", idx.Version)

	// Skip an unchanged index (version gate); per-catalog cursors + the
	// queue handle any still-pending work independently.
	if prev, err := e.deps.Store.GetIndex(ctx, ref.IndexURL); err != nil {
		e.deps.Log.Error("crawler.index.state_failed", "indexUrl", ref.IndexURL, "err", err)
		return
	} else if prev != nil && prev.IndexVersion == idx.Version {
		e.deps.Log.Info("crawler.index.unchanged", "indexUrl", ref.IndexURL, "version", idx.Version)
		return
	}

	for _, entry := range idx.Catalogs {
		take, _ := Select(entry, e.cfg.Networks)
		cursor, seen, err := e.deps.Store.GetCatalogVersion(ctx, entry.CatalogID)
		if err != nil {
			e.deps.Log.Error("crawler.catalog.state_failed", "catalogId", entry.CatalogID, "err", err)
			continue
		}
		switch d := Decide(entry, cursor, seen); d.Action {
		case ActionSync:
			if !take {
				continue // not for this crawler's networks
			}
			if err := e.deps.Store.Enqueue(ctx, state.QueueItem{
				CatalogID: entry.CatalogID, IndexURL: ref.IndexURL,
				FromVersion: cursor, ToVersion: d.ToVersion, Op: "sync",
			}); err != nil {
				e.deps.Log.Error("crawler.enqueue_failed", "catalogId", entry.CatalogID, "err", err)
				continue
			}
			e.deps.Log.Info("crawler.catalog.enqueued", "catalogId", entry.CatalogID, "toVersion", d.ToVersion)
		case ActionRetire:
			if !seen {
				continue // never had it; nothing to retire
			}
			if err := e.deps.Store.Enqueue(ctx, state.QueueItem{
				CatalogID: entry.CatalogID, IndexURL: ref.IndexURL, ToVersion: cursor, Op: "retire",
			}); err != nil {
				e.deps.Log.Error("crawler.enqueue_failed", "catalogId", entry.CatalogID, "err", err)
				continue
			}
			e.deps.Log.Info("crawler.catalog.retire_enqueued", "catalogId", entry.CatalogID)
		case ActionRollback:
			e.deps.Log.Warn("crawler.catalog.rollback", "catalogId", entry.CatalogID, "cursor", cursor, "indexVersion", d.ToVersion)
		case ActionSkipUnchanged:
			// nothing to do
		}
	}

	if err := e.deps.Store.UpsertIndex(ctx, ref.IndexURL, idx.ParticipantID, ref.Source, idx.Version, SyncOK, e.nextIndexCrawl()); err != nil {
		e.deps.Log.Error("crawler.index.record_failed", "indexUrl", ref.IndexURL, "err", err)
	}
}

func (e *Engine) nextIndexCrawl() time.Time {
	if e.cfg.IndexInterval <= 0 {
		return time.Time{}
	}
	return e.deps.Now().Add(e.cfg.IndexInterval)
}

// --- catalog job ---

// catalogPass drains the queue: claim -> process -> repeat until empty.
func (e *Engine) catalogPass(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		item, err := e.deps.Store.ClaimNext(ctx)
		if err != nil {
			e.deps.Log.Error("crawler.claim_failed", "err", err)
			return
		}
		if item == nil {
			return // queue drained
		}
		e.processItem(ctx, item)
	}
}

// processItem resolves + pushes one claimed catalog, then settles it.
func (e *Engine) processItem(ctx context.Context, item *state.ClaimedItem) {
	if item.Op == "retire" {
		if err := e.deps.Store.Complete(ctx, item.ID, state.CatalogState{
			CatalogID: item.CatalogID, IndexURL: item.IndexURL,
			Version: item.ToVersion, Status: "retired", PushStatus: "skipped",
		}); err != nil {
			e.deps.Log.Error("crawler.retire_failed", "catalogId", item.CatalogID, "err", err)
		} else {
			e.deps.Log.Info("crawler.catalog.retired", "catalogId", item.CatalogID)
		}
		return
	}

	idx, err := e.deps.FetchIndex(ctx, item.IndexURL)
	if err != nil {
		e.failItem(ctx, item, 0, "index_fetch: "+err.Error())
		return
	}
	entry, ok := findCatalog(idx, item.CatalogID)
	if !ok {
		e.failItem(ctx, item, 0, "catalog_absent_from_index")
		return
	}

	fetch := func(f FileEntry) ([]byte, error) { return e.deps.FetchFile(ctx, f) }
	catalog, err := Resolve(entry, item.ToVersion, fetch)
	if err != nil {
		e.failItem(ctx, item, 0, "resolve: "+err.Error())
		return
	}
	_, visibleTo := Select(entry, e.cfg.Networks)
	body, err := BuildPushBody(PushMeta{
		ParticipantID: idx.ParticipantID, BppURI: e.cfg.BppURI,
		MessageID: e.newID(), TransactionID: e.newID(),
		Timestamp:  e.deps.Now().UTC().Format(time.RFC3339),
		UpdateMode: UpdateModeFull, VisibleTo: visibleTo,
	}, catalog)
	if err != nil {
		e.failItem(ctx, item, 0, "build_push: "+err.Error())
		return
	}

	if e.deps.Validate != nil {
		if err := e.deps.Validate(ctx, body); err != nil {
			e.failItem(ctx, item, 0, "schema: "+err.Error())
			return
		}
	}

	outcome, err := e.deps.Push(ctx, body)
	if err != nil {
		e.failItem(ctx, item, outcome.HTTPStatus, "push: "+err.Error())
		return
	}
	if !outcome.Acked {
		e.failItem(ctx, item, outcome.HTTPStatus, "push_nack: "+outcome.Reason)
		return
	}

	if err := e.deps.Store.Complete(ctx, item.ID, state.CatalogState{
		CatalogID: item.CatalogID, IndexURL: item.IndexURL, ParticipantID: idx.ParticipantID,
		Version: item.ToVersion, Status: "active", PushStatus: "pushed", HTTPStatus: outcome.HTTPStatus,
	}); err != nil {
		e.deps.Log.Error("crawler.complete_failed", "catalogId", item.CatalogID, "err", err)
		return
	}
	e.deps.Log.Info("crawler.catalog.pushed", "catalogId", item.CatalogID, "version", item.ToVersion)
}

// failItem retries with backoff, or gives up after MaxAttempts — recording
// the failure and advancing the cursor so it isn't re-enqueued until the
// catalog's version advances again (no hot loop).
func (e *Engine) failItem(ctx context.Context, item *state.ClaimedItem, httpStatus int, reason string) {
	attempts := item.Attempts + 1
	if attempts >= e.cfg.MaxAttempts {
		pushStatus := "failed"
		if httpStatus >= 400 && httpStatus < 500 {
			pushStatus = "rejected"
		}
		if err := e.deps.Store.Complete(ctx, item.ID, state.CatalogState{
			CatalogID: item.CatalogID, IndexURL: item.IndexURL,
			Version: item.ToVersion, Status: "active",
			PushStatus: pushStatus, Reason: reason, HTTPStatus: httpStatus,
		}); err != nil {
			e.deps.Log.Error("crawler.giveup_failed", "catalogId", item.CatalogID, "err", err)
		}
		e.deps.Log.Warn("crawler.catalog.failed", "catalogId", item.CatalogID, "reason", reason, "attempts", attempts, "httpStatus", httpStatus)
		return
	}
	next := e.deps.Now().Add(Backoff(attempts))
	if err := e.deps.Store.FailQueueItem(ctx, item.ID, next); err != nil {
		e.deps.Log.Error("crawler.fail_failed", "catalogId", item.CatalogID, "err", err)
	}
	e.deps.Log.Warn("crawler.catalog.retry", "catalogId", item.CatalogID, "reason", reason, "attempts", attempts)
}

func (e *Engine) newID() string {
	if e.deps.NewID != nil {
		return e.deps.NewID()
	}
	return ""
}

func findCatalog(idx Index, catalogID string) (CatalogEntry, bool) {
	for _, c := range idx.Catalogs {
		if c.CatalogID == catalogID {
			return c, true
		}
	}
	return CatalogEntry{}, false
}
