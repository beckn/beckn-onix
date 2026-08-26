package catalogpublisher

// verify.go — post-write publish verification: for every catalog actually
// touched (Changed, or retired) this call, re-fetch and re-verify its
// published index entry and newly-written file(s) using catalog-core's own
// Fetcher -- the same reachability + digest + decompression +
// self-signature checks a real crawler runs, against a REAL registry key
// lookup (never the local signing key Publish just used). Not a
// reimplementation of any of that here, deliberately: the file spec
// mandates exactly one correct way to verify these documents, and Fetcher
// already is that way (see catalog.Fetcher's own doc comment).
//
// A failure here is a real failure, not a warning: a file that isn't
// reachable, doesn't match its digest, or doesn't verify against the
// registered key is as good as unpublished, however cleanly it wrote
// locally. This runs after catalogStore.Publish has already committed the
// write, so a failure can't roll storage back -- it only changes how this
// call reports that catalog (REJECTED via a non-fatal
// definition.PublishError{Stage: "verify"}, the same vocabulary every
// other per-catalog failure uses) instead of leaving it silently reported
// as ACCEPTED when it isn't actually crawlable.
//
// An unchanged (Changed == false) outcome is skipped: nothing new was
// written for it this call, so there's nothing new to verify -- verifying
// it again every single publish call would be pure waste.
//
// registryKeySource, below, adapts this onix deployment's own
// definition.RegistryLookup to catalog-core's crawler.KeySource -- the
// generic key-lookup function Fetcher calls internally. The actual
// registry-call/classification logic lives in the shared
// pkg/plugin/implementation/internal/registrykey package (catalogcrawler's
// own keysource.go needs the exact same thing).
//
// Verification for every touched catalog/retirement runs concurrently
// (bounded, see verifyConcurrency) rather than one at a time: each is an
// independent HTTP round trip against the same freshly re-fetched index, so
// there is nothing to serialize them for.

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/internal/registrykey"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"

	"golang.org/x/sync/errgroup"
)

// verifyConcurrency bounds how many FetchFile/FetchIndex round trips
// verifyPublished runs at once. Verifying each touched catalog is
// independent I/O against the same freshly re-fetched index -- capped
// rather than unbounded so one huge batch doesn't open dozens of
// simultaneous connections against the blob store/CDN it just wrote to.
const verifyConcurrency = 8

// verifyRetryAttempts/verifyRetryBaseDelay bound the retries verifyPublished
// gives a blob store/CDN with read-after-write consistency lag before
// reporting a catalog REJECTED: the write in Publish already committed by
// the time verification runs, so an entry that's merely not visible *yet*
// (a plain "not found" on re-fetch, or a network/5xx fetch error) deserves a
// couple of short, backed-off retries rather than an immediate false
// rejection. A genuinely wrong publish -- bad digest, bad signature, SSRF,
// oversize -- is a crawler.PermanentError and is never retried: retrying it
// would only add latency, never a different answer. Total added worst-case
// latency across all attempts is bounded (~1.4s at these defaults), which is
// small next to the FetchFile round trips verification already makes.
const (
	verifyRetryAttempts  = 3
	verifyRetryBaseDelay = 200 * time.Millisecond
)

// verifyPublished checks every outcome actually Changed this call, plus
// every retirement, against the freshly re-fetched index -- one index
// fetch per attempt, since they all share it. Retries (see
// verifyRetryAttempts) while at least one failure looks transient, so a
// still-propagating write gets a fair chance to become visible before
// being reported REJECTED. Returns one PublishError per catalog that still
// fails after the final attempt.
func (p *Publisher) verifyPublished(ctx context.Context, result definition.PublishResult) []definition.PublishError {
	var changed []definition.CatalogPublishOutcome
	for _, o := range result.Catalogs {
		if o.Changed {
			changed = append(changed, o)
		}
	}
	if len(changed) == 0 && len(result.Retirements) == 0 {
		return nil
	}

	retiredLatest := make(map[string]bool, len(result.RetiredLatest))
	for _, rl := range result.RetiredLatest {
		retiredLatest[rl.CatalogID] = true
	}

	var errs []definition.PublishError
	for attempt := 0; attempt < verifyRetryAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepOrDone(ctx, verifyRetryBaseDelay*time.Duration(1<<(attempt-1))); err != nil {
				return errs
			}
		}

		idxRes, err := p.fetcher.FetchIndex(ctx, p.IndexURL(), catalog.IndexConditions{})
		if err != nil {
			err = fmt.Errorf("fetching published index: %w", err)
			errs = nil
			for _, o := range changed {
				errs = append(errs, definition.PublishError{CatalogID: o.CatalogID, Stage: "verify", Reason: err.Error(), Fatal: false})
			}
			for _, r := range result.Retirements {
				errs = append(errs, definition.PublishError{CatalogID: r.CatalogID, Stage: "verify", Reason: err.Error(), Fatal: false})
			}
			continue // an index fetch failure is never a PermanentError -- always worth a retry.
		}

		changedErrs := runConcurrent(len(changed), verifyConcurrency, func(i int) error {
			return p.verifyOutcome(ctx, result.NodeID, idxRes.Index, changed[i])
		})
		retireErrs := runConcurrent(len(result.Retirements), verifyConcurrency, func(i int) error {
			r := result.Retirements[i]
			return p.verifyRetirement(ctx, result.NodeID, idxRes.Index, r, retiredLatest[r.CatalogID])
		})

		errs = nil
		anyTransient := false
		for i, err := range changedErrs {
			if err != nil {
				errs = append(errs, definition.PublishError{CatalogID: changed[i].CatalogID, Stage: "verify", Reason: err.Error(), Fatal: false})
				anyTransient = anyTransient || !crawler.IsPermanent(err)
			}
		}
		for i, err := range retireErrs {
			if err != nil {
				errs = append(errs, definition.PublishError{CatalogID: result.Retirements[i].CatalogID, Stage: "verify", Reason: err.Error(), Fatal: false})
				anyTransient = anyTransient || !crawler.IsPermanent(err)
			}
		}
		if len(errs) == 0 || !anyTransient {
			return errs
		}
	}
	return errs
}

// runConcurrent runs work(0), work(1), ..., work(n-1) concurrently, at most
// limit at a time, and returns each call's error at its own index -- so a
// caller can still report per-item failures against the original slice
// they came from, in the original order, despite the concurrent execution.
func runConcurrent(n, limit int, work func(i int) error) []error {
	if n == 0 {
		return nil
	}
	errs := make([]error, n)
	var g errgroup.Group
	g.SetLimit(limit)
	for i := range n {
		g.Go(func() error {
			errs[i] = work(i)
			return nil
		})
	}
	g.Wait()
	return errs
}

// sleepOrDone waits d, or returns ctx.Err() early if ctx is done first.
func sleepOrDone(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// verifyOutcome verifies o's own index entry is present (which itself
// proves its self-signature verified against the real registry key --
// FetchIndex drops any entry that doesn't) and re-fetches whichever
// file(s) this call actually wrote for it.
func (p *Publisher) verifyOutcome(ctx context.Context, nodeID string, idx catalog.Index, o definition.CatalogPublishOutcome) error {
	entry, ok := catalog.FindCatalog(idx, o.CatalogID)
	if !ok {
		return fmt.Errorf("published entry not found in the re-fetched index (dropped for a bad signature, or missing outright)")
	}

	switch o.Mode {
	case "baseline":
		if _, err := p.fetcher.FetchFile(ctx, nodeID, o.CatalogID, entry.Baseline); err != nil {
			return fmt.Errorf("baseline file: %w", err)
		}
	case "change":
		change, ok := changeAtVersion(entry.Changes, int64(o.Version))
		if !ok {
			return fmt.Errorf("published change file for version %d not found in the re-fetched index entry", o.Version)
		}
		if _, err := p.fetcher.FetchFile(ctx, nodeID, o.CatalogID, change); err != nil {
			return fmt.Errorf("change file: %w", err)
		}
	}

	if len(o.LatestContent) > 0 {
		if entry.Latest == nil {
			return fmt.Errorf("latest file expected but missing from the re-fetched index entry")
		}
		if _, err := p.fetcher.FetchFile(ctx, nodeID, o.CatalogID, *entry.Latest); err != nil {
			return fmt.Errorf("latest file: %w", err)
		}
	}
	return nil
}

// verifyRetirement verifies a retired catalog's tombstone entry is present
// (same signature-implies-presence reasoning as verifyOutcome) and, if
// this call wrote a final "latest" tombstone for it, that it's fetchable
// too.
func (p *Publisher) verifyRetirement(ctx context.Context, nodeID string, idx catalog.Index, r definition.RetirementOutcome, wroteLatest bool) error {
	entry, ok := catalog.FindCatalog(idx, r.CatalogID)
	if !ok {
		return fmt.Errorf("retired entry not found in the re-fetched index (dropped for a bad signature, or missing outright)")
	}
	if wroteLatest {
		if entry.Latest == nil {
			return fmt.Errorf("final latest file expected but missing from the re-fetched index entry")
		}
		if _, err := p.fetcher.FetchFile(ctx, nodeID, r.CatalogID, *entry.Latest); err != nil {
			return fmt.Errorf("retired latest file: %w", err)
		}
	}
	return nil
}

func changeAtVersion(changes []catalog.FileEntry, version int64) (catalog.FileEntry, bool) {
	for _, c := range changes {
		if c.EffectiveVersion() == version {
			return c, true
		}
	}
	return catalog.FileEntry{}, false
}

// registryKeySource adapts a definition.RegistryLookup to a
// crawler.KeySource. See pkg/plugin/implementation/internal/registrykey for
// the actual lookup/classification logic, shared with catalogcrawler's own
// keysource.go.
func registryKeySource(reg definition.RegistryLookup) crawler.KeySource {
	return registrykey.Source("catalogpublisher", reg)
}

// resolveRegistryKey asks the registry for {subscriberID, keyID} and turns
// the answer into a usable Ed25519 key, or into a correctly classified
// failure.
func resolveRegistryKey(ctx context.Context, reg definition.RegistryLookup, nodeID, keyID string) (ed25519.PublicKey, error) {
	return registrykey.Resolve(ctx, "catalogpublisher", reg, nodeID, keyID)
}
