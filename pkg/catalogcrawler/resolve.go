package catalogcrawler

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/beckn-one/beckn-onix/pkg/catalogfile"
)

// FetchFunc fetches and verifies one file's bytes. The digest check lives
// inside the fetcher, which is injected so resolve stays pure and testable.
type FetchFunc func(f FileEntry) ([]byte, error)

// Changeset records what changed for a catalog since our cursor: which
// resource/offer ids were upserted, whether any removal happened, and whether
// we had to fall back to the baseline (new catalog, or our cursor is behind
// the current baseline). It decides the push mode: only-upserts -> MERGE;
// any removal or a baseline fetch -> FULL (the only mode Discovery deletes in).
type Changeset struct {
	UpsertedResources map[string]bool
	UpsertedOffers    map[string]bool
	HasRemovals       bool
	FromBaseline      bool
}

// Resolve builds the complete catalog at toVersion (baseline + change files
// folded). It is a thin wrapper over ResolveWithChangeset.
func Resolve(entry CatalogEntry, toVersion int64, fetch FetchFunc) ([]byte, error) {
	catalog, _, err := ResolveWithChangeset(entry, 0, false, toVersion, fetch)
	return catalog, err
}

// ResolveWithChangeset folds the catalog to its complete current content AND
// records what changed since cursor. seen=false means the catalog is new.
func ResolveWithChangeset(entry CatalogEntry, cursor int64, seen bool, toVersion int64, fetch FetchFunc) ([]byte, Changeset, error) {
	cs := Changeset{
		UpsertedResources: map[string]bool{},
		UpsertedOffers:    map[string]bool{},
		FromBaseline:      !seen || cursor < entry.Baseline.Version,
	}

	current, err := fetch(entry.Baseline)
	if err != nil {
		return nil, cs, fmt.Errorf("catalogcrawler: fetching baseline v%d: %w", entry.Baseline.Version, err)
	}

	changes := append([]FileEntry(nil), entry.Changes...)
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Version < changes[j].Version })

	running := entry.Baseline.Version
	for _, c := range changes {
		if c.URL == "" {
			continue // not-yet-published placeholder entry (no content)
		}
		if c.Version <= entry.Baseline.Version || c.Version > toVersion {
			continue
		}
		b, err := fetch(c)
		if err != nil {
			return nil, cs, fmt.Errorf("catalogcrawler: fetching change v%d: %w", c.Version, err)
		}
		var cf catalogfile.ChangeFileDoc
		if err := json.Unmarshal(b, &cf); err != nil {
			return nil, cs, fmt.Errorf("catalogcrawler: parsing change v%d: %w", c.Version, err)
		}
		// Continuity: each change must start where the previous one ended, or
		// the fold silently mis-composes (a gap => a wrong catalog).
		if int64(cf.FromVersion) != running {
			return nil, cs, fmt.Errorf("catalogcrawler: change v%d fromVersion=%d, expected %d (gap in change files)", c.Version, cf.FromVersion, running)
		}
		if c.Version > cursor { // accumulate the changeset only past our cursor
			accumulateChangeset(&cs, cf)
		}
		current, err = catalogfile.Apply(current, b)
		if err != nil {
			return nil, cs, fmt.Errorf("catalogcrawler: folding change v%d: %w", c.Version, err)
		}
		running = int64(cf.ToVersion)
	}
	return current, cs, nil
}

func accumulateChangeset(cs *Changeset, cf catalogfile.ChangeFileDoc) {
	for _, u := range cf.Resources.Upserts {
		if id, err := catalogfile.ItemID(u); err == nil {
			cs.UpsertedResources[id] = true
		}
	}
	for _, u := range cf.Offers.Upserts {
		if id, err := catalogfile.ItemID(u); err == nil {
			cs.UpsertedOffers[id] = true
		}
	}
	if len(cf.Resources.Removals) > 0 || len(cf.Offers.Removals) > 0 {
		cs.HasRemovals = true
	}
}

// filterCatalog returns the catalog keeping only the given resource/offer ids
// (catalog metadata preserved), for a MERGE push of just the changed items.
func filterCatalog(catalog []byte, keepResources, keepOffers map[string]bool) ([]byte, error) {
	var doc catalogfile.Doc
	if err := json.Unmarshal(catalog, &doc); err != nil {
		return nil, fmt.Errorf("catalogcrawler: reading catalog for filter: %w", err)
	}
	doc.Resources = filterByID(doc.Resources, keepResources)
	doc.Offers = filterByID(doc.Offers, keepOffers)
	return json.Marshal(doc)
}

func filterByID(items []json.RawMessage, keep map[string]bool) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(keep))
	for _, it := range items {
		id, err := catalogfile.ItemID(it)
		if err != nil {
			continue
		}
		if keep[id] {
			out = append(out, it)
		}
	}
	return out
}
