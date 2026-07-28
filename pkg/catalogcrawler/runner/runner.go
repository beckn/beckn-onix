package runner

// runner.go — the Engine type and its process supervisor: New (defaults), Start
// / Stop (launch + drain the two scheduled jobs), CrawlNow (the on-demand /crawl
// trigger), the ticker loop, and the correlation-id minter. The per-pass logic
// lives in indexpass.go / syncpass.go; the log vocabulary in telemetry.go.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/source"
)

// Engine runs the two scheduled jobs (index + catalog) linked by the queue.
type Engine struct {
	cfg  EngineConfig
	deps Deps
	wg   sync.WaitGroup

	mu    sync.Mutex
	ctx   context.Context
	stop  context.CancelFunc
	state DaemonState
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
	return &Engine{cfg: cfg, deps: deps, state: DaemonStopped}
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

// CrawlNow runs an immediate index crawl for one index URL (the /crawl
// supportability trigger). It launches a tracked goroutine on the engine's own
// context, so Stop() drains it before the DB is closed.
func (e *Engine) CrawlNow(_ context.Context, indexURL string) error {
	e.mu.Lock()
	if e.state != DaemonReady || e.ctx == nil {
		e.mu.Unlock()
		return fmt.Errorf("catalogcrawler: engine not running")
	}
	ctx := e.ctx
	e.wg.Add(1)
	e.mu.Unlock()

	go func() {
		defer e.wg.Done()
		// An on-demand crawl is its own single-index run; mint a run_id so its
		// log lines correlate just like a scheduled pass.
		e.crawlIndex(ctx, source.IndexRef{IndexURL: indexURL, Source: source.KindOnDemand}, onDemand, e.newID())
	}()
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

func (e *Engine) newID() string {
	if e.deps.NewID != nil {
		return e.deps.NewID()
	}
	return ""
}
