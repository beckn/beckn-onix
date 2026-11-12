package catalogpublisher

// verify.go — post-write publish verification: for every catalog in this
// call (submitted or retired, changed or not), re-fetch and re-verify its
// published index entry and the files a consumer would fetch, using
// catalog-core's own Fetcher -- the same reachability + digest + decompression +
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
// What is checked is what a consumer fetches, not what this call believes
// it wrote:
//   - The entry in the re-fetched index must be the entry this call signed
//     (same entryVersion and signature). File references are then taken
//     from the signed entry. An index that still serves an earlier entry
//     is a propagation delay and is retried. An index that serves a later
//     one is another adapter replica having published the catalog again
//     in the meantime; this call's publish was committed and then built
//     on, which is not a failure (see verifyOutcome).
//   - A baseline or change outcome: that file.
//   - An unchanged or metadata-only outcome: the baseline and every change
//     file after it. Such a call may be the repeat of one that was REJECTED
//     here after it had already committed, and must give the same answer
//     until the files verify.
//   - Every outcome whose entry lists "latest", changed or not: the
//     "latest" file, against the entry's digest (NFH-014 CON-TBD-36). An
//     unchanged outcome is not skipped: the index is rewritten on every
//     call, "latest" may have been repaired, and a caller repeating a
//     publish that was REJECTED at this stage must get a real answer.
//   - A retirement, first or repeated: the index entry is a tombstone, the
//     one this call reports, and every "latest" file of the catalog serves
//     a CatalogFile carrying retiredAt (CON-TBD-38).
//
// "latest" is the one file overwritten in place, so a cache in front of the
// blob store can serve the previous copy for a while after the write. A
// digest mismatch on it is therefore retried like any propagation delay
// (the specification lets a consumer do the same, CON-TBD-37) and only
// reported when it persists. Baseline and change files live at immutable
// URLs: a digest mismatch there is never retried.
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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/internal/registrykey"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"
	"github.com/beckn/catalog-core/pkg/catalog/store"

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
// would only add latency, never a different answer. The one exception is a
// digest mismatch on "latest" or on a stale index entry, see the top of
// this file. The waits double from verifyRetryBaseDelay: 0.2, 0.4, 0.8 and
// 1.6s, so a write that becomes visible within 3s of the first check
// passes, and a failure that stays is reported after 3s. Publish holds its
// lock for that long, so this is also how long a persistent failure delays
// the next publish.
const (
	verifyRetryAttempts  = 5
	verifyRetryBaseDelay = 200 * time.Millisecond
)

// logVerifyOutcome logs a summary of verifyPublished's result -- which
// catalogs (every outcome, plus retirements) passed and which
// were rejected, named individually -- so a log stream alone answers
// "did this call's writes actually verify" without requiring a caller to
// inspect the HTTP response body's per-catalog results. Silent when
// nothing was touched (verifyPublished itself returns nil in that case).
func (p *Publisher) logVerifyOutcome(result definition.PublishResult, verifyErrs []definition.PublishError) {
	var touched []string
	for _, o := range result.Catalogs {
		touched = append(touched, o.CatalogID)
	}
	for _, r := range result.Retirements {
		touched = append(touched, r.CatalogID)
	}
	if len(touched) == 0 {
		return
	}

	rejected := make(map[string]bool, len(verifyErrs))
	for _, e := range verifyErrs {
		rejected[e.CatalogID] = true
	}
	var passed, failed []string
	for _, id := range touched {
		if rejected[id] {
			failed = append(failed, id)
		} else {
			passed = append(passed, id)
		}
	}

	if len(failed) == 0 {
		p.log.Info("catalogpublisher: post-write verification passed", "catalogs", passed)
		return
	}
	p.log.Warn("catalogpublisher: post-write verification failed for one or more catalogs", "passed", passed, "rejected", failed)
}

// verifyPublished checks every outcome of this call, plus
// every retirement, against the freshly re-fetched index -- one index
// fetch per attempt, since they all share it. Retries (see
// verifyRetryAttempts) while at least one failure looks transient, so a
// still-propagating write gets a fair chance to become visible before
// being reported REJECTED. Returns one PublishError per catalog that still
// fails after the final attempt, and a note for each outcome that passed
// because a later publish had already superseded it (see verifyOutcome).
func (p *Publisher) verifyPublished(ctx context.Context, result definition.PublishResult) (errs []definition.PublishError, notes []string) {
	outcomes := result.Catalogs
	if len(outcomes) == 0 && len(result.Retirements) == 0 {
		return nil, nil
	}

	retiredLatest := make(map[string][]definition.RetiredCatalogFile, len(result.RetiredLatest))
	for _, rl := range result.RetiredLatest {
		retiredLatest[rl.CatalogID] = append(retiredLatest[rl.CatalogID], rl)
	}

	for attempt := 0; attempt < verifyRetryAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepOrDone(ctx, verifyRetryBaseDelay*time.Duration(1<<(attempt-1))); err != nil {
				return errs, notes
			}
		}

		idxRes, err := p.fetcher.FetchIndex(ctx, p.IndexURL(), catalog.IndexConditions{})
		if err != nil {
			err = fmt.Errorf("fetching published index: %w", err)
			errs, notes = nil, nil
			for _, o := range outcomes {
				errs = append(errs, definition.PublishError{CatalogID: o.CatalogID, Stage: "verify", Reason: err.Error(), Fatal: false})
			}
			for _, r := range result.Retirements {
				errs = append(errs, definition.PublishError{CatalogID: r.CatalogID, Stage: "verify", Reason: err.Error(), Fatal: false})
			}
			continue // an index fetch failure is never a PermanentError -- always worth a retry.
		}

		outcomeNotes := make([]string, len(outcomes))
		changedErrs := runConcurrent(len(outcomes), verifyConcurrency, func(i int) (err error) {
			outcomeNotes[i], err = p.verifyOutcome(ctx, result.NodeID, idxRes.Index, outcomes[i])
			return err
		})
		retireErrs := runConcurrent(len(result.Retirements), verifyConcurrency, func(i int) error {
			r := result.Retirements[i]
			return p.verifyRetirement(ctx, result.NodeID, idxRes.Index, r, retiredLatest[r.CatalogID])
		})

		errs, notes = nil, nil
		anyTransient := false
		for _, note := range outcomeNotes {
			if note != "" {
				notes = append(notes, note)
			}
		}
		for i, err := range changedErrs {
			if err != nil {
				errs = append(errs, definition.PublishError{CatalogID: outcomes[i].CatalogID, Stage: "verify", Reason: err.Error(), Fatal: false})
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
			return errs, notes
		}
	}
	return errs, notes
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
// FetchIndex drops any entry that doesn't), that it is the entry this call
// signed, and re-fetches the file this call wrote for it plus the "latest"
// file its entry lists, written this call or not.
//
// The entry the index serves need not be the one this call signed:
//   - An older one (lower entryVersion) is a copy of the index from before
//     this call's write. Retryable.
//   - A different one at the same entryVersion is not this call's and not
//     a successor of it. Retryable, and REJECTED if it stays.
//   - A newer one (higher entryVersion) means a later publish has already
//     replaced this call's entry: another adapter replica sharing the blob
//     store, which the store only lets commit on top of the entry it
//     loaded. This call's publish was committed and then built on, so it
//     is not a failure. See verifySuperseded for what is still checked.
//     The returned note says so, for PublishResult.Warnings.
func (p *Publisher) verifyOutcome(ctx context.Context, nodeID string, idx catalog.Index, o definition.CatalogPublishOutcome) (note string, err error) {
	entry, ok := catalog.FindCatalog(idx, o.CatalogID)
	if !ok {
		return "", fmt.Errorf("published entry not found in the re-fetched index (dropped for a bad signature, or missing outright)")
	}
	if len(o.SignedEntry) > 0 {
		var signed catalog.CatalogEntry
		if err := json.Unmarshal(o.SignedEntry, &signed); err != nil {
			return "", crawler.Permanentf("the entry this call signed does not parse: %v", err)
		}
		switch {
		case entry.EntryVersion > signed.EntryVersion:
			return p.verifySuperseded(ctx, nodeID, entry, signed, o)
		case entry.EntryVersion < signed.EntryVersion:
			// Not permanent: an index that still serves the previous entry is
			// what a cache in front of the blob store looks like.
			return "", fmt.Errorf("the re-fetched index does not serve the entry this call published (it has entryVersion %d, published %d); an earlier copy of the index may still be cached",
				entry.EntryVersion, signed.EntryVersion)
		case entry.Signature.Value != signed.Signature.Value:
			return "", fmt.Errorf("the re-fetched index serves a different entry at entryVersion %d than the one this call published; another publisher is writing this catalog without seeing this one's writes",
				entry.EntryVersion)
		}
		entry = signed
	}

	switch o.Mode {
	case "baseline":
		if _, err := p.fetcher.FetchFile(ctx, nodeID, o.CatalogID, entry.Baseline); err != nil {
			return "", fmt.Errorf("baseline file: %w", err)
		}
	case "change":
		change, ok := changeAtVersion(entry.Changes, int64(o.Version))
		if !ok {
			return "", fmt.Errorf("published change file for version %d not found in the re-fetched index entry", o.Version)
		}
		if _, err := p.fetcher.FetchFile(ctx, nodeID, o.CatalogID, change); err != nil {
			return "", fmt.Errorf("change file: %w", err)
		}
	default:
		// "unchanged" or "metadata": this call wrote no baseline or change
		// file, so there is no one file to point at. It may be the repeat
		// of a call that was REJECTED here after it had committed, and the
		// answer must not turn into ACCEPTED while the file that failed is
		// still not what the entry lists. So fetch what a consumer starting
		// from this entry fetches: the baseline and every change after it.
		// (Changes at or below the baseline's version are only listed for
		// consumers part-way through the older lineage.)
		if _, err := p.fetcher.FetchFile(ctx, nodeID, o.CatalogID, entry.Baseline); err != nil {
			return "", fmt.Errorf("baseline file: %w", err)
		}
		for _, change := range entry.Changes {
			if change.EffectiveVersion() <= entry.Baseline.Version {
				continue
			}
			if _, err := p.fetcher.FetchFile(ctx, nodeID, o.CatalogID, change); err != nil {
				return "", fmt.Errorf("change file for version %d: %w", change.EffectiveVersion(), err)
			}
		}
	}

	if entry.Latest == nil {
		if len(o.LatestContent) > 0 {
			return "", fmt.Errorf("latest file expected but missing from the re-fetched index entry")
		}
		return "", nil
	}
	return "", p.fetchOverwritten(ctx, nodeID, o.CatalogID, *entry.Latest, "latest file")
}

// verifySuperseded is verifyOutcome for a call whose entry (signed) has
// already been replaced in the index by a newer one (served).
//
// The baseline or change file this call wrote sits at an immutable URL. If
// the newer entry still lists that version, it must list it with the
// digest this call signed, and the file must still verify: anything else
// means the file this call published was replaced, which no later publish
// may do. If the newer entry no longer lists it (a re-baseline past it, or
// a retirement), nothing this call wrote is advertised any more and there
// is nothing left to fetch.
//
// "latest" is not checked. It is one file overwritten in place and now
// belongs to the newer entry; the call that published that entry verifies
// it.
func (p *Publisher) verifySuperseded(ctx context.Context, nodeID string, served, signed catalog.CatalogEntry, o definition.CatalogPublishOutcome) (string, error) {
	if served.IsRetired() {
		return fmt.Sprintf("catalog %s was retired by a later request (entryVersion %d) before this one (entryVersion %d) was verified", o.CatalogID, served.EntryVersion, signed.EntryVersion), nil
	}
	note := fmt.Sprintf("catalog %s was published again by a later request (entryVersion %d) before this one (entryVersion %d) was verified; the index now serves the later content", o.CatalogID, served.EntryVersion, signed.EntryVersion)

	var mine, listed catalog.FileEntry
	var found bool
	switch o.Mode {
	case "baseline":
		mine, listed, found = signed.Baseline, served.Baseline, served.Baseline.Version == int64(o.Version)
	case "change":
		mine, _ = changeAtVersion(signed.Changes, int64(o.Version))
		listed, found = changeAtVersion(served.Changes, int64(o.Version))
	}
	if !found {
		return note, nil
	}
	if listed.Digest != mine.Digest {
		return "", crawler.Permanentf("the index now lists version %d of this catalog with digest %s, but this call published it with digest %s: a file at an immutable URL was replaced",
			o.Version, listed.Digest, mine.Digest)
	}
	if _, err := p.fetcher.FetchFile(ctx, nodeID, o.CatalogID, listed); err != nil {
		return "", fmt.Errorf("%s file: %w", o.Mode, err)
	}
	return note, nil
}

// fetchOverwritten fetches and verifies a file that is overwritten in
// place (only "latest" is). Unlike a file at an immutable URL, a digest or
// version mismatch here can be the previous copy still being served, so it
// is returned as a retryable error rather than a permanent one. Every other
// failure -- signature, undecodable content, oversize, SSRF -- keeps its
// class.
func (p *Publisher) fetchOverwritten(ctx context.Context, nodeID, catalogID string, fe catalog.FileEntry, what string) error {
	_, err := p.fetcher.FetchFile(ctx, nodeID, catalogID, fe)
	if err == nil {
		return nil
	}
	if crawler.PermanentClass(err) == crawler.FaultDigestMismatch {
		// %v, not %w: the permanent class must not survive the wrapping.
		return fmt.Errorf("%s: the copy being served is not the one just published (an earlier copy may still be cached, or the file was changed): %v", what, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// verifyRetirement verifies a retired catalog as a consumer sees it: the
// index entry is a tombstone -- the one this call reports, whether it was
// signed now or by an earlier call -- and every "latest" file of the
// catalog (files) serves a CatalogFile carrying retiredAt.
//
// Unlike verifyOutcome, a final "latest" file can't be looked up through
// the re-fetched index: a tombstone entry never carries a "latest" pointer
// (catalog-core drops it once retiredAt is set). Each is checked at its own
// stable URL instead -- the URL a consumer that only ever fetches "latest"
// already holds. That includes files this call did not write: on a repeat
// retire nothing is written, and this check is all that happens.
func (p *Publisher) verifyRetirement(ctx context.Context, nodeID string, idx catalog.Index, r definition.RetirementOutcome, files []definition.RetiredCatalogFile) error {
	entry, ok := catalog.FindCatalog(idx, r.CatalogID)
	if !ok {
		return fmt.Errorf("retired entry not found in the re-fetched index (dropped for a bad signature, or missing outright)")
	}
	if !entry.IsRetired() {
		return fmt.Errorf("the re-fetched index still lists the catalog as live; an earlier copy of the index may still be cached")
	}
	if r.RetiredAt != "" && (entry.RetiredAt != r.RetiredAt || int(entry.EntryVersion) != r.EntryVersion) {
		return fmt.Errorf("the re-fetched index serves a different tombstone (entryVersion %d, retiredAt %s) than this call's (entryVersion %d, retiredAt %s)",
			entry.EntryVersion, entry.RetiredAt, r.EntryVersion, r.RetiredAt)
	}
	for _, latest := range files {
		fe, err := p.retiredLatestEntry(latest)
		if err != nil {
			return fmt.Errorf("retired latest file: %w", err)
		}
		if err := p.fetchOverwritten(ctx, nodeID, r.CatalogID, fe, "retired latest file "+fe.URL); err != nil {
			return err
		}
	}
	return nil
}

// retiredLatestEntry builds the file reference for a retired catalog's
// final "latest" file, the same way catalog-core builds a live catalog's
// "latest" index pointer: the stable URL under PublicBaseURL, and a digest
// over the canonical (uncompressed) signed content. FetchFile's digest
// check against it therefore proves the bytes now served at that URL are
// exactly that tombstone. A document without retiredAt is a permanent
// failure -- re-fetching can't change what was signed.
func (p *Publisher) retiredLatestEntry(latest definition.RetiredCatalogFile) (catalog.FileEntry, error) {
	var doc catalog.CatalogFileDoc
	if err := json.Unmarshal(latest.Content, &doc); err != nil {
		return catalog.FileEntry{}, crawler.Permanentf("not a valid catalog file: %v", err)
	}
	if doc.RetiredAt == nil {
		return catalog.FileEntry{}, crawler.Permanentf("final latest file does not carry retiredAt")
	}
	sum := sha256.Sum256(latest.Content)
	entry := catalog.FileEntry{
		Version: doc.Version,
		URL:     strings.TrimRight(p.config.PublicBaseURL, "/") + "/" + store.LatestFilePath(latest.CatalogID, latest.Compressed),
		Size:    int64(len(latest.ServedContent)),
		Digest:  "sha-256:" + hex.EncodeToString(sum[:]),
	}
	if latest.AlreadyStored {
		entry.Size = latest.ServedSize
	}
	if latest.Compressed {
		entry.Encoding = "gzip"
	}
	return entry, nil
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
