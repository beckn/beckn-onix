package catalogpublisher

// storeadapter.go bridges this package's own definition.CatalogSubmission/
// PriorCatalogState/PublishRequest/PublishResult shapes to
// pkg/catalog/publisher's/pkg/catalog/store's. definition's types stay
// self-contained (not importing pkg/catalog/store directly) so the onix
// plugin contract doesn't hard-bind to a storage layer -- this is the one
// place that glue happens.

import (
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/publisher"
	"github.com/beckn/catalog-core/pkg/catalog/store"
)

// ToPriorState converts pkg/catalog/store's reconstructed catalog state
// (e.g. from Store.LoadCatalogs) into the map[string]PriorCatalogState
// shape definition.PublishRequest needs -- the two are already isomorphic
// field-for-field; this is purely the type-boundary conversion a caller
// (the catalogPublish HTTP handler, catalogpublisherctl) needs since
// definition deliberately doesn't import pkg/catalog/store.
func ToPriorState(states map[string]store.CatalogState) map[string]definition.PriorCatalogState {
	out := make(map[string]definition.PriorCatalogState, len(states))
	for id, s := range states {
		out[id] = definition.PriorCatalogState{
			Catalog:         s.Catalog,
			BaselineFile:    toDefinitionFileRef(s.BaselineFile),
			ChangeFiles:     toDefinitionFileRefs(s.ChangeFiles),
			EntryVersion:    int(s.EntryVersion),
			CatalogType:     s.CatalogType,
			NetworkIds:      s.NetworkIds,
			SchemaTypes:     s.SchemaTypes,
			IsActive:        s.IsActive,
			Dependencies:    toDefinitionDependencies(s.Dependencies),
			CrawlHint:       s.CrawlHint,
			LatestPublished: s.LatestPublished,
		}
	}
	return out
}

// toDefinitionFileRefValue is toCatalogFileEntryValue's inverse.
func toDefinitionFileRefValue(fe catalog.FileEntry) definition.FileRef {
	fr := definition.FileRef{URL: fe.URL, Size: fe.Size, Digest: fe.Digest, Encoding: fe.Encoding}
	if fe.FromVersion != 0 {
		fr.FromVersion, fr.Version = int(fe.FromVersion), int(fe.ToVersion)
	} else {
		fr.Version = int(fe.Version)
	}
	return fr
}

func toDefinitionFileRef(fe *catalog.FileEntry) *definition.FileRef {
	if fe == nil {
		return nil
	}
	fr := toDefinitionFileRefValue(*fe)
	return &fr
}

func toDefinitionFileRefs(fes []catalog.FileEntry) []definition.FileRef {
	if len(fes) == 0 {
		return nil
	}
	out := make([]definition.FileRef, len(fes))
	for i, fe := range fes {
		out[i] = toDefinitionFileRefValue(fe)
	}
	return out
}

func toDefinitionDependencies(deps []catalog.MasterDependency) []definition.MasterDependency {
	if len(deps) == 0 {
		return nil
	}
	out := make([]definition.MasterDependency, len(deps))
	for i, d := range deps {
		out[i] = definition.MasterDependency{CatalogID: d.CatalogID, Version: int(d.Version), IndexURL: d.IndexURL}
	}
	return out
}

// ToStorePublishRequest converts a Publish call's definition.PublishResult
// into the request store.Store.Publish expects: each outcome's
// already-signed entry, plus whatever new baseline/change/latest content
// it produced, repackaged as a store.CatalogUpdate. This is the caller's
// (the catalogPublish HTTP handler, catalogpublisherctl) counterpart to
// toDefinitionResult -- a caller only ever sees definition.PublishResult
// (via the definition.CatalogPublisher interface), never the internal
// publisher.Result that produced it.
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

func toCatalogStates(states map[string]definition.PriorCatalogState) map[string]store.CatalogState {
	out := make(map[string]store.CatalogState, len(states))
	for id, s := range states {
		out[id] = store.CatalogState{
			Catalog:         s.Catalog,
			BaselineFile:    toCatalogFileEntry(s.BaselineFile),
			ChangeFiles:     toCatalogFileEntries(s.ChangeFiles),
			EntryVersion:    int64(s.EntryVersion),
			CatalogType:     s.CatalogType,
			NetworkIds:      s.NetworkIds,
			SchemaTypes:     s.SchemaTypes,
			IsActive:        s.IsActive,
			Dependencies:    toCatalogMasterDependencies(s.Dependencies),
			CrawlHint:       s.CrawlHint,
			LatestPublished: s.LatestPublished,
		}
	}
	return out
}

// toCatalogFileEntryValue converts one definition.FileRef to a
// catalog.FileEntry. definition.FileRef reuses its own Version field for
// what is a change file's ToVersion (FromVersion != 0 marks that case,
// matching FileRef's own doc comment: FromVersion is zero for a
// baseline/latest ref) -- catalog.FileEntry instead carries these as two
// mutually-exclusive shapes (Version vs. FromVersion/ToVersion), so which
// one gets populated here depends on FromVersion.
func toCatalogFileEntryValue(fr definition.FileRef) catalog.FileEntry {
	fe := catalog.FileEntry{URL: fr.URL, Size: fr.Size, Digest: fr.Digest, Encoding: fr.Encoding}
	if fr.FromVersion != 0 {
		fe.FromVersion, fe.ToVersion = int64(fr.FromVersion), int64(fr.Version)
	} else {
		fe.Version = int64(fr.Version)
	}
	return fe
}

func toCatalogFileEntry(fr *definition.FileRef) *catalog.FileEntry {
	if fr == nil {
		return nil
	}
	fe := toCatalogFileEntryValue(*fr)
	return &fe
}

func toCatalogFileEntries(frs []definition.FileRef) []catalog.FileEntry {
	if len(frs) == 0 {
		return nil
	}
	out := make([]catalog.FileEntry, len(frs))
	for i, fr := range frs {
		out[i] = toCatalogFileEntryValue(fr)
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
