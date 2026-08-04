package runner

// runner.go — the Engine type and its process supervisor: New (defaults), Start
// / Stop (launch + drain the two scheduled jobs), CrawlRegistry (the on-demand
// /crawl trigger, which discovers indexes from a registry), the ticker loop, and
// the correlation-id minter. The per-job logic lives in crawl.go / sync.go; the
// log vocabulary in telemetry.go.

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DaemonState is the crawler process lifecycle — the supervisor (the "driver").
// It has NO "completed": a supervisor never completes, it just launches Catalog
// Syncs until stopped.
type DaemonState string

const (
	DaemonReady    DaemonState = "ready"
	DaemonStopping DaemonState = "stopping"
	DaemonStopped  DaemonState = "stopped"
)

func (d DaemonState) String() string { return string(d) }

// Engine runs the two scheduled jobs (index + catalog) linked by the queue.
type Engine struct {
	cfg  EngineConfig
	deps Deps
	wg   sync.WaitGroup

	mu         sync.Mutex
	ctx        context.Context
	stop       context.CancelFunc
	state      DaemonState
	indexLocks map[string]*indexLock // per index URL, guarded by mu
}

// indexLock serialises crawls of ONE index URL. ch is a one-token semaphore
// (rather than a sync.Mutex) so a waiter can honour ctx and give up when the
// engine is stopping. refs counts holders + waiters so the map entry can be
// dropped once the last one leaves and the map cannot grow without bound as
// indexes come and go.
type indexLock struct {
	ch   chan struct{}
	refs int
}

// New builds an Engine, filling in sane defaults for optional deps.
func New(cfg EngineConfig, deps Deps) *Engine {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Log == nil {
		deps.Log = NopLogger{}
	}
	if deps.Metrics == nil {
		deps.Metrics = NopMetrics{}
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.MaxPushBytes <= 0 {
		cfg.MaxPushBytes = 10 << 20
	}
	if cfg.IndexInterval <= 0 {
		cfg.IndexInterval = 5 * time.Minute
	}
	if cfg.CatalogInterval <= 0 {
		cfg.CatalogInterval = 30 * time.Second
	}
	// Inert until Start() flips it to DaemonReady (there is no "starting" state;
	// the supervisor vocabulary is ready → stopping → stopped).
	return &Engine{cfg: cfg, deps: deps, state: DaemonStopped, indexLocks: map[string]*indexLock{}}
}

// Start launches the index and catalog jobs as two goroutines. It returns
// immediately; Stop() drains them.
func (e *Engine) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.ctx, e.stop, e.state = ctx, cancel, DaemonReady
	e.mu.Unlock()
	e.loop(ctx, e.cfg.IndexInterval, e.indexPass)
	e.loop(ctx, e.cfg.CatalogInterval, e.catalogPass)
	return nil
}

// Stop signals all jobs (scheduled + in-flight /crawl) and waits for them to
// drain, so a caller can safely close the DB afterwards.
func (e *Engine) Stop() error {
	e.mu.Lock()
	e.state = DaemonStopping
	stop := e.stop
	e.mu.Unlock()
	if stop != nil {
		stop()
	}
	e.wg.Wait()
	e.mu.Lock()
	e.state = DaemonStopped
	e.mu.Unlock()
	return nil
}

// CrawlRegistry runs an immediate registry-backed crawl: it asks the injected
// registry-source factory (a DeDi /query client) for the providers of the given
// networks under registryURL, then crawls every discovered index on the
// on_demand trigger — the same discovery the scheduled index pass uses, so an
// on-demand /crawl and the background pass take the SAME registry-based input.
// Like the scheduled pass it launches one tracked goroutine on the engine's own
// context (drained by Stop) and returns the run_id synchronously.
func (e *Engine) CrawlRegistry(_ context.Context, registryURL string, networkIDs []string) (string, error) {
	e.mu.Lock()
	if e.state != DaemonReady || e.ctx == nil {
		e.mu.Unlock()
		return "", fmt.Errorf("crawler: engine not running")
	}
	if e.deps.NewRegistrySource == nil {
		e.mu.Unlock()
		return "", fmt.Errorf("crawler: registry source factory not configured")
	}
	ctx := e.ctx
	e.wg.Add(1)
	e.mu.Unlock()

	runID := e.newID()
	start := e.deps.Now()
	src := e.deps.NewRegistrySource(registryURL, networkIDs)

	go func() {
		defer e.wg.Done()
		refs, err := src.IndexRefs(ctx)
		if err != nil {
			e.logCrawlFailed(runID, onDemand, "source_resolve", err)
			return
		}
		fetched, changed, enqueued := 0, 0, 0
		for _, ref := range refs {
			if ctx.Err() != nil {
				return
			}
			r := e.crawlIndex(ctx, ref, onDemand, runID)
			if r.fetched {
				fetched++
			}
			if r.changed {
				changed++
			}
			enqueued += r.enqueued
		}
		e.logCrawlFinished(runID, onDemand, fetched, changed, enqueued, e.deps.Now().Sub(start))
	}()
	return runID, nil
}

// lockIndex takes the per-index crawl lock for indexURL, blocking only callers
// aimed at that SAME index — every other index stays crawlable, so one slow
// publisher never stalls the pass. This is what keeps the on-demand /crawl
// trigger and the scheduled ticker from racing each other on one index's
// version cursor and next_crawl_at. It reports ok=false when ctx ended before
// the lock was taken (the engine is stopping), in which case the caller must
// not crawl. The returned release must be called when ok is true.
func (e *Engine) lockIndex(ctx context.Context, indexURL string) (release func(), ok bool) {
	e.mu.Lock()
	if e.indexLocks == nil {
		e.indexLocks = map[string]*indexLock{}
	}
	l := e.indexLocks[indexURL]
	if l == nil {
		l = &indexLock{ch: make(chan struct{}, 1)}
		e.indexLocks[indexURL] = l
	}
	l.refs++ // claimed under e.mu, so the entry cannot be dropped under us
	e.mu.Unlock()

	select {
	case l.ch <- struct{}{}:
		return func() { e.releaseIndex(indexURL, l, true) }, true
	case <-ctx.Done():
		e.releaseIndex(indexURL, l, false)
		return func() {}, false
	}
}

// releaseIndex hands the token back (when held) and drops the map entry once no
// holder or waiter is left, so the lock table stays the size of the indexes
// being crawled right now, not every index ever crawled.
func (e *Engine) releaseIndex(indexURL string, l *indexLock, held bool) {
	if held {
		<-l.ch
	}
	e.mu.Lock()
	l.refs--
	if l.refs == 0 {
		delete(e.indexLocks, indexURL)
	}
	e.mu.Unlock()
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

func (e *Engine) newID() string {
	if e.deps.NewID != nil {
		return e.deps.NewID()
	}
	return ""
}
