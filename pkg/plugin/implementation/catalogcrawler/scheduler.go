package catalogcrawler

// scheduler.go — Scheduler drives crawlmanager.Params' two entry points on a
// fixed cadence: PollIndexes on a ticker, and SyncNext looped to drain the
// queue on its own ticker. This is ONE way to drive them -- a ticker-based
// scheduler -- not the only correct one: an on-demand HTTP trigger, a cron
// invocation, or an event off a queue are equally valid ways to decide WHEN
// to poll/sync, so this lives in the plugin (a deployment choice), not in
// crawlmanager itself (which only owns WHAT one poll pass / one sync attempt
// does, not when it runs). Ported in spirit from the catalog-crawler
// prototype's Engine (runner.go's loop/Start/Stop shape).

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
)

// DefaultIndexInterval, DefaultCatalogInterval, and DefaultParkSweepInterval
// are used when SchedulerConfig leaves the corresponding field at zero.
const (
	DefaultIndexInterval     = 5 * time.Minute
	DefaultCatalogInterval   = 30 * time.Second
	DefaultParkSweepInterval = 15 * time.Minute
)

// SchedulerConfig is Scheduler's tunable cadence.
type SchedulerConfig struct {
	IndexInterval   time.Duration // 0 => DefaultIndexInterval
	CatalogInterval time.Duration // 0 => DefaultCatalogInterval

	// ParkSweepInterval is how often RequeueOrAbandonParked runs -- a third,
	// independent cadence from IndexInterval/CatalogInterval (see
	// crawlmanager.Params.RequeueOrAbandonParked's own doc comment for why
	// it's deliberately decoupled from PollIndexes/SyncNext). 0 =>
	// DefaultParkSweepInterval.
	ParkSweepInterval time.Duration
	// ParkOlderThan is how long a catalog must have been sitting parked
	// before this sweep will revive or abandon it. Zero (the default) means
	// no extra grace period beyond the sweep cadence itself -- each tick
	// acts on anything currently parked.
	ParkOlderThan time.Duration
}

func (c SchedulerConfig) indexInterval() time.Duration {
	if c.IndexInterval > 0 {
		return c.IndexInterval
	}
	return DefaultIndexInterval
}

func (c SchedulerConfig) catalogInterval() time.Duration {
	if c.CatalogInterval > 0 {
		return c.CatalogInterval
	}
	return DefaultCatalogInterval
}

func (c SchedulerConfig) parkSweepInterval() time.Duration {
	if c.ParkSweepInterval > 0 {
		return c.ParkSweepInterval
	}
	return DefaultParkSweepInterval
}

// Scheduler runs Params.PollIndexes and Params.SyncNext on their own
// intervals until Stop.
type Scheduler struct {
	params crawlmanager.Params
	cfg    SchedulerConfig
	log    *slog.Logger // nil => slog.Default()

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewScheduler builds a Scheduler over params, driven at cfg's cadence. log
// may be nil.
func NewScheduler(params crawlmanager.Params, cfg SchedulerConfig, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{params: params, cfg: cfg, log: log}
}

// Start launches the index-poll, catalog-sync, and park-sweep loops as
// three goroutines and returns immediately; Stop drains them.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.ctx = ctx
	s.cancel = cancel
	s.mu.Unlock()
	s.loop(ctx, s.cfg.indexInterval(), s.pollTick)
	s.loop(ctx, s.cfg.catalogInterval(), s.syncTick)
	s.loop(ctx, s.cfg.parkSweepInterval(), s.parkSweepTick)
}

// Stop signals both loops and waits for the in-flight tick (if any), and any
// RunOnce call already in flight, to finish before returning. Holds s.mu for
// its entire cancel+Wait -- not just the cancel -- so it can never
// interleave with RunOnce's own check+Add critical section: either a RunOnce
// call's Add happens-before Stop acquires the lock (so Wait below correctly
// blocks on it), or it acquires the lock only after Stop has already
// canceled ctx under the same lock, in which case it observes ctx.Err() !=
// nil and never launches anything. Without holding the lock across Wait too,
// Stop's cancel-then-Wait could otherwise run entirely between a RunOnce
// call's not-done check and its Add, letting Wait return on a still-zero
// counter before that call ever adds itself -- orphaning it after Stop has
// already returned to its caller.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// RunOnce launches fn once in a goroutine tied to the scheduler's OWN
// lifecycle context (from Start), not the caller's -- so an on-demand crawl
// triggered from a request-scoped context outlives that request, and is
// tracked by the same WaitGroup Stop waits on, so shutdown never orphans it.
// Reports false without launching fn if the scheduler hasn't been started
// (or has already been stopped) -- see Stop's doc comment for why the
// not-done check and the wg.Add below must happen as one atomic step under
// s.mu, the same lock Stop holds across its own cancel+Wait.
func (s *Scheduler) RunOnce(fn func(context.Context)) bool {
	s.mu.Lock()
	ctx := s.ctx
	if ctx == nil || ctx.Err() != nil {
		s.mu.Unlock()
		return false
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		fn(ctx)
	}()
	return true
}

// loop runs fn immediately, then again every interval, until ctx is done.
func (s *Scheduler) loop(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn(ctx)
		t := time.NewTicker(interval)
		defer t.Stop()
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

// parkSweepTick drives RequeueOrAbandonParked on its own independent
// cadence -- Params.RequeueOrAbandonParked itself logs revived/abandoned
// counts, so there's nothing further to log here beyond a transport-level
// error.
func (s *Scheduler) parkSweepTick(ctx context.Context) {
	if err := s.params.RequeueOrAbandonParked(ctx, s.cfg.ParkOlderThan); err != nil {
		s.log.ErrorContext(ctx, "catalogcrawler: park sweep failed", "error", err)
	}
}

func (s *Scheduler) pollTick(ctx context.Context) {
	if err := s.params.PollIndexes(ctx); err != nil {
		s.log.ErrorContext(ctx, "catalogcrawler: poll tick failed", "error", err)
	}
}

// syncTick drains the queue: SyncNext until it reports nothing left to claim
// (claimed=false, whether because the queue is empty or because ClaimNext
// itself errored -- either way there is nothing more to usefully do this
// tick, and erroring on every loop iteration would spin hot on a wedged
// store instead of waiting for the next tick).
func (s *Scheduler) syncTick(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		claimed, err := s.params.SyncNext(ctx)
		if !claimed {
			if err != nil {
				s.log.ErrorContext(ctx, "catalogcrawler: claiming next queue item failed", "error", err)
			}
			return
		}
		// SyncNext already logs and records a per-catalog sync failure itself
		// (see SyncNext's RecordFailure/Park/Reschedule handling); nothing
		// further to do here beyond continuing to drain the queue.
	}
}
