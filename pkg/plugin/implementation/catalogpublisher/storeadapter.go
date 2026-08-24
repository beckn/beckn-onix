package catalogpublisher

// storeadapter.go bridges this package's own definition.CatalogSubmission/
// PublishResult shapes to pkg/catalog/publisher's/pkg/catalog/store's.
// definition's types stay self-contained (not importing pkg/catalog/store
// directly) so the onix plugin contract doesn't hard-bind to a storage
// layer -- this is the one place that glue happens. Prior state itself
// needs no conversion in either direction: store.CatalogState is exactly
// what publisher.Params.PriorState expects, and Publish (catalogpublisher.go)
// loads it straight from its own catalogStore and passes it through
// unconverted.

import (
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/publisher"
	"github.com/beckn/catalog-core/pkg/catalog/store"
)

// ToStorePublishRequest converts a Publish call's definition.PublishResult
// into the request store.Store.Publish expects: each outcome's
// already-signed entry, plus whatever new baseline/change/latest content
// it produced, repackaged as a store.CatalogUpdate. This is Publish's own
// (catalogpublisher.go) counterpart to toDefinitionResult, used to persist
// the result it just produced back to its catalogStore.
func ToStorePublishRequest(result definition.PublishResult) store.PublishRequest {
	req := store.PublishRequest{NodeID: result.NodeID, NextUpdate: result.NextUpdate}

	for _, outcome := range result.Catalogs {
		u := store.CatalogUpdate{CatalogID: outcome.CatalogID, SignedEntry: outcome.SignedEntry}
		if outcome.Content != nil {
			fw := &store.FileWrite{
				Version: int64(outcome.Version), Content: outcome.Content,
				ServedContent: outcome.ServedContent, Compressed: outcome.Compressed,
			}
			switch outcome.Mode {
			case "baseline":
				u.Baseline = fw
			case "change":
				u.Change = fw
			}
		}
		if outcome.LatestContent != nil {
			u.Latest = &store.FileWrite{
				Version: int64(outcome.Version), Content: outcome.LatestContent,
				ServedContent: outcome.LatestServedContent, Compressed: outcome.Compressed,
			}
		}
		req.Updates = append(req.Updates, u)
	}

	retiredLatest := make(map[string]definition.RetiredCatalogFile, len(result.RetiredLatest))
	for _, rl := range result.RetiredLatest {
		retiredLatest[rl.CatalogID] = rl
	}
	for _, r := range result.Retirements {
		u := store.CatalogUpdate{CatalogID: r.CatalogID, SignedEntry: r.SignedEntry}
		if rl, ok := retiredLatest[r.CatalogID]; ok {
			u.Latest = &store.FileWrite{Content: rl.Content, ServedContent: rl.ServedContent, Compressed: rl.Compressed}
		}
		req.Retirements = append(req.Retirements, u)
	}

	return req
}

func toSubmissions(subs []definition.CatalogSubmission) []publisher.Submission {
	if len(subs) == 0 {
		return nil
	}
	out := make([]publisher.Submission, len(subs))
	for i, s := range subs {
		out[i] = publisher.Submission{
			CatalogID:    s.CatalogID,
			CatalogType:  s.CatalogType,
			SchemaTypes:  s.SchemaTypes,
			NetworkIds:   s.NetworkIds,
			Dependencies: toCatalogMasterDependencies(s.Dependencies),
			CrawlHint:    s.CrawlHint,
			Catalog:      s.Catalog,
		}
	}
	return out
}

func toCatalogMasterDependencies(deps []definition.MasterDependency) []catalog.MasterDependency {
	if len(deps) == 0 {
		return nil
	}
	out := make([]catalog.MasterDependency, len(deps))
	for i, d := range deps {
		out[i] = catalog.MasterDependency{CatalogID: d.CatalogID, Version: int64(d.Version), IndexURL: d.IndexURL}
	}
	return out
}

// toDefinitionResult converts pkg/catalog/publisher's Result into this
// package's own definition.PublishResult: Result.Reports carries the
// human-readable detail (Version/EntryVersion/Mode/Digest), Result.Publish
// carries the store-ready signed entries and file content -- a
// CatalogPublishOutcome is the union of both, matched by CatalogID, since
// definition's own contract reports them together.
func toDefinitionResult(result publisher.Result) definition.PublishResult {
	out := definition.PublishResult{
		PublishedAt: result.PublishedAt,
		NodeID:      result.Publish.NodeID,
		NextUpdate:  result.Publish.NextUpdate,
	}
	for _, e := range result.Errors {
		out.Errors = append(out.Errors, definition.PublishError{CatalogID: e.CatalogID, Stage: e.Stage, Reason: e.Reason, Fatal: e.Fatal})
	}

	updatesByID := make(map[string]store.CatalogUpdate, len(result.Publish.Updates))
	for _, u := range result.Publish.Updates {
		updatesByID[u.CatalogID] = u
	}
	for _, r := range result.Reports {
		u := updatesByID[r.CatalogID]
		outcome := definition.CatalogPublishOutcome{
			CatalogID: r.CatalogID, SignedEntry: u.SignedEntry,
			Version: int(r.Version), EntryVersion: int(r.EntryVersion), Changed: r.Changed, Digest: r.Digest, Mode: r.Mode,
			Content: r.Content, LatestContent: r.LatestContent, LatestDigest: r.LatestDigest,
		}
		if fw := fileWriteOf(u); fw != nil {
			outcome.ServedContent, outcome.Compressed = fw.ServedContent, fw.Compressed
		}
		if u.Latest != nil {
			outcome.LatestServedContent = u.Latest.ServedContent
		}
		out.Catalogs = append(out.Catalogs, outcome)
	}

	for _, ret := range result.Publish.Retirements {
		out.Retirements = append(out.Retirements, definition.RetirementOutcome{CatalogID: ret.CatalogID, SignedEntry: ret.SignedEntry})
		if ret.Latest != nil {
			out.RetiredLatest = append(out.RetiredLatest, definition.RetiredCatalogFile{
				CatalogID: ret.CatalogID, Content: ret.Latest.Content, ServedContent: ret.Latest.ServedContent, Compressed: ret.Latest.Compressed,
			})
		}
	}
	return out
}

// fileWriteOf returns whichever of Baseline/Change is set on u (a
// submitted catalog's update carries exactly one, matching its Mode) --
// nil for a metadata-only/unchanged update, which carries neither.
func fileWriteOf(u store.CatalogUpdate) *store.FileWrite {
	if u.Baseline != nil {
		return u.Baseline
	}
	return u.Change
}
