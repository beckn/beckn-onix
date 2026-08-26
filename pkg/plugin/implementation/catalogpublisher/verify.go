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
// generic key-lookup function Fetcher calls internally. catalog-core knows
// nothing about onix's Subscription/status model, so the base64 decoding,
// usable-status check, and fault classification live here, not there. A
// near-duplicate of the same handful of lines in catalogcrawler's own
// keysource.go -- deliberately not imported from there (plugin
// implementation packages don't cross-import each other in this codebase);
// hoisting it into a shared internal package is a reasonable follow-up if a
// third plugin ever needs it, not before. No caching is added at this
// layer either: definition.RegistryLookup implementations (registry,
// dediregistry) already cache Lookup results themselves; a second,
// independently-expiring cache here would risk serving a revoked/rotated
// key past the registry plugin's own cache invalidation.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"
)

// verifyPublished checks every outcome actually Changed this call, plus
// every retirement, against the freshly re-fetched index -- one index
// fetch for the whole call, since they all share it. Returns one
// PublishError per catalog that fails.
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

	idxRes, err := p.fetcher.FetchIndex(ctx, p.IndexURL(), catalog.IndexConditions{})
	if err != nil {
		err = fmt.Errorf("fetching published index: %w", err)
		var errs []definition.PublishError
		for _, o := range changed {
			errs = append(errs, definition.PublishError{CatalogID: o.CatalogID, Stage: "verify", Reason: err.Error(), Fatal: false})
		}
		for _, r := range result.Retirements {
			errs = append(errs, definition.PublishError{CatalogID: r.CatalogID, Stage: "verify", Reason: err.Error(), Fatal: false})
		}
		return errs
	}

	var errs []definition.PublishError
	for _, o := range changed {
		if err := p.verifyOutcome(ctx, result.NodeID, idxRes.Index, o); err != nil {
			errs = append(errs, definition.PublishError{CatalogID: o.CatalogID, Stage: "verify", Reason: err.Error(), Fatal: false})
		}
	}
	retiredLatest := make(map[string]bool, len(result.RetiredLatest))
	for _, rl := range result.RetiredLatest {
		retiredLatest[rl.CatalogID] = true
	}
	for _, r := range result.Retirements {
		if err := p.verifyRetirement(ctx, result.NodeID, idxRes.Index, r, retiredLatest[r.CatalogID]); err != nil {
			errs = append(errs, definition.PublishError{CatalogID: r.CatalogID, Stage: "verify", Reason: err.Error(), Fatal: false})
		}
	}
	return errs
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
// crawler.KeySource.
func registryKeySource(reg definition.RegistryLookup) crawler.KeySource {
	return func(ctx context.Context, nodeID, keyID string) (ed25519.PublicKey, error) {
		if reg == nil {
			// Fail closed. Wired without a registry, there is no way to learn any
			// key, so nothing can be verified.
			return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: no registry configured, cannot resolve key %q", keyID)
		}
		if nodeID == "" {
			return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: no nodeId given, cannot resolve key %q", keyID)
		}
		return resolveRegistryKey(ctx, reg, nodeID, keyID)
	}
}

// resolveRegistryKey asks the registry for {subscriberID, keyID} and turns
// the answer into a usable Ed25519 key, or into a correctly classified
// failure.
func resolveRegistryKey(ctx context.Context, reg definition.RegistryLookup, nodeID, keyID string) (ed25519.PublicKey, error) {
	subs, err := reg.Lookup(ctx, &model.Subscription{
		Subscriber: model.Subscriber{SubscriberID: nodeID},
		KeyID:      keyID,
	})
	if err != nil {
		// TRANSIENT: a registry that is down, rate limiting, or timing out says
		// nothing about this key. Returned unclassified, so ClassifyFault reports
		// FaultTransient.
		return nil, fmt.Errorf("catalogpublisher: registry lookup for node %q key %q: %w", nodeID, keyID, err)
	}
	if len(subs) == 0 {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: registry has no key %q for node %q", keyID, nodeID)
	}
	sub := subs[0]
	if !model.IsKeyStatusUsable(sub.Status) {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: registry key %q for node %q has unusable status %q", keyID, nodeID, sub.Status)
	}
	if sub.SigningPublicKey == "" {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: registry key %q for node %q has no signing public key", keyID, nodeID)
	}
	raw, err := base64.StdEncoding.DecodeString(sub.SigningPublicKey)
	if err != nil {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: registry key %q for node %q is not valid base64: %v", keyID, nodeID, err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: registry key %q for node %q decodes to %d bytes, want %d",
			keyID, nodeID, len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
