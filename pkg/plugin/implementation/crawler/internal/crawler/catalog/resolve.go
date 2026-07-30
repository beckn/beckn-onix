package catalog

// resolve.go — baseline + change-file composition: folds a catalog to its
// content at a target version, records what changed since the cursor
// (Changeset), and builds MERGE delta / filtered payloads. The only file in
// this package that fetches bytes (via an injected FetchFunc); the fold logic
// itself stays pure.

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/beckn-one/beckn-onix/pkg/catalogfile"
)

// FetchFunc fetches and verifies one file's bytes. The digest check lives
// inside the fetcher, which is injected so composition stays pure and testable.
type FetchFunc func(f FileEntry) ([]byte, error)

// Changeset records what changed for a catalog since our cursor: which
// resource/offer ids were upserted, whether any removal happened, and whether
// we had to fall back to the baseline. It decides the push mode: only-upserts
// -> MERGE; any removal or a baseline fetch -> FULL (the only mode Discovery
// deletes in).
type Changeset struct {
	UpsertedResources map[string]bool
	UpsertedOffers    map[string]bool
	RemovedResources  int
	RemovedOffers     int
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
		return nil, cs, fmt.Errorf("crawler: fetching baseline v%d: %w", entry.Baseline.Version, err)
	}
	// The baseline is the one file that can reach the push doc WITHOUT ever being
	// parsed (a first sync with no applicable change files returns it verbatim),
	// so it is validated here. Downstream counting is best-effort — a doc that
	// will not parse counts as zero resources — so an unchecked corrupt baseline
	// would settle as a clean "nothing to push" skip and advance the cursor.
	if err := ValidateCatalogDoc(current); err != nil {
		return nil, cs, fmt.Errorf("crawler: baseline v%d: %w", entry.Baseline.Version, err)
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
			return nil, cs, fmt.Errorf("crawler: fetching change v%d: %w", c.Version, err)
		}
		var cf catalogfile.ChangeFileDoc
		if err := json.Unmarshal(b, &cf); err != nil {
			return nil, cs, Permanentf("crawler: parsing change v%d: %v", c.Version, err)
		}
		// Continuity: each change must start where the previous one ended, or the
		// fold silently mis-composes (a gap => a wrong catalog). A gap is a
		// publisher-side data problem — it won't fix on retry, so it's permanent.
		if int64(cf.FromVersion) != running {
			return nil, cs, PermanentFaultf(FaultGap, "crawler: change v%d fromVersion=%d, expected %d (gap in change files)", c.Version, cf.FromVersion, running)
		}
		if c.Version > cursor { // accumulate the changeset only past our cursor
			accumulateChangeset(&cs, cf)
		}
		current, err = catalogfile.Apply(current, b)
		if err != nil {
			return nil, cs, Permanentf("crawler: folding change v%d: %v", c.Version, err)
		}
		running = int64(cf.ToVersion)
	}
	return current, cs, nil
}

// ResolveDelta builds a MERGE payload for an incremental update WITHOUT fetching
// the baseline: it takes the catalog envelope (id/descriptor/provider) from a
// change file's `catalog` block and the union of resource/offer upserts across
// the change files in (cursor, toVersion] (latest wins per id, order preserved).
// Removals are recorded in the changeset but NOT applied (deferred to the
// FULL/removals version). Returns ok=false when no change file carried the
// metadata envelope, so the caller can fall back to a full resolve.
//
// Continuity is enforced exactly as ResolveWithChangeset enforces it: the first
// change file in the range must start at the cursor and each later one must
// start where the previous ended. Without that check a delta composed from
// whatever change files happen to exist silently drops a missing version's
// upserts, the push succeeds, the cursor advances, and Discovery is divergent
// with no signal. A gap parks (FaultGap) instead.
func ResolveDelta(entry CatalogEntry, cursor, toVersion int64, fetch FetchFunc) ([]byte, Changeset, bool, error) {
	cs := Changeset{UpsertedResources: map[string]bool{}, UpsertedOffers: map[string]bool{}}
	changes := append([]FileEntry(nil), entry.Changes...)
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Version < changes[j].Version })

	resByID, offByID := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	var resOrder, offOrder []string
	var envelope json.RawMessage

	running := cursor
	for _, c := range changes {
		if c.Version <= cursor || c.Version > toVersion {
			continue
		}
		if c.URL == "" {
			// A not-yet-published placeholder OUTSIDE the range is skipped above and
			// is harmless. Inside the range it is a hole: skipping it would compose a
			// delta that is missing that version's upserts, which is the same silent
			// divergence a gap causes, so it is reported as one.
			return nil, cs, false, PermanentFaultf(FaultGap,
				"crawler: change v%d has no url (unpublished placeholder inside the delta range %d..%d)", c.Version, cursor, toVersion)
		}
		b, err := fetch(c)
		if err != nil {
			return nil, cs, false, fmt.Errorf("crawler: fetching change v%d: %w", c.Version, err)
		}
		var cf catalogfile.ChangeFileDoc
		if err := json.Unmarshal(b, &cf); err != nil {
			return nil, cs, false, Permanentf("crawler: parsing change v%d: %v", c.Version, err)
		}
		if int64(cf.FromVersion) != running {
			return nil, cs, false, PermanentFaultf(FaultGap,
				"crawler: change v%d fromVersion=%d, expected %d (gap in change files)", c.Version, cf.FromVersion, running)
		}
		if len(cf.Catalog) > 0 {
			envelope = cf.Catalog // metadata envelope; latest wins
		}
		// Changeset side (upsert-id set, removal counts, HasRemovals) is the same
		// accumulation ResolveWithChangeset does; the id->raw maps below are the
		// delta-only bit that carries the actual bytes to push (latest wins per id,
		// order preserved).
		accumulateChangeset(&cs, cf)
		for _, u := range cf.Resources.Upserts {
			if id, err := catalogfile.ItemID(u); err == nil {
				if _, seen := resByID[id]; !seen {
					resOrder = append(resOrder, id)
				}
				resByID[id] = u
			}
		}
		for _, u := range cf.Offers.Upserts {
			if id, err := catalogfile.ItemID(u); err == nil {
				if _, seen := offByID[id]; !seen {
					offOrder = append(offOrder, id)
				}
				offByID[id] = u
			}
		}
		running = int64(cf.ToVersion)
	}
	if len(envelope) == 0 {
		return nil, cs, false, nil // no metadata envelope -> caller treats it as a content fault (no baseline fallback)
	}

	var doc catalogfile.Doc
	if err := json.Unmarshal(envelope, &doc); err != nil {
		return nil, cs, false, Permanentf("crawler: reading change catalog envelope: %v", err)
	}
	doc.Resources = orderedValues(resByID, resOrder)
	doc.Offers = orderedValues(offByID, offOrder)
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, cs, false, err
	}
	return out, cs, true, nil
}

func orderedValues(m map[string]json.RawMessage, order []string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(order))
	for _, id := range order {
		out = append(out, m[id])
	}
	return out
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
	cs.RemovedResources += len(cf.Resources.Removals)
	cs.RemovedOffers += len(cf.Offers.Removals)
	if cs.RemovedResources > 0 || cs.RemovedOffers > 0 {
		cs.HasRemovals = true
	}
}

// ValidateCatalogDoc structurally checks bytes that claim to be a Beckn catalog
// document. It answers one question: can this content be trusted to say how many
// resources and offers it carries?
//
// Three outcomes, and the difference between the last two is the whole point:
//   - not a JSON object, or a resources/offers field that is not an array: the
//     content is corrupt, a PERMANENT content_invalid fault that parks;
//   - no resources key and no offers key at all: there is no container to read a
//     count from, so "zero resources" would be an assumption, not an
//     observation. Also content_invalid;
//   - an empty (or absent-item) resources/offers array: a legitimately EMPTY
//     catalog. That is a real published state, so it is valid here and settles
//     as a clean skip.
//
// It does not validate the items themselves; that is the push schema's job.
func ValidateCatalogDoc(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return PermanentFaultf(FaultContentInvalid, "crawler: catalog content is not a JSON object: %v", err)
	}
	_, hasResources := fields["resources"]
	_, hasOffers := fields["offers"]
	if !hasResources && !hasOffers {
		return PermanentFaultf(FaultContentInvalid, "crawler: catalog content carries no resources or offers container")
	}
	var doc catalogfile.Doc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return PermanentFaultf(FaultContentInvalid, "crawler: catalog content has a malformed resources/offers container: %v", err)
	}
	return nil
}

// FilterCatalog returns the catalog keeping only the given resource/offer ids
// (catalog metadata preserved), for a MERGE push of just the changed items.
func FilterCatalog(catalog []byte, keepResources, keepOffers map[string]bool) ([]byte, error) {
	var doc catalogfile.Doc
	if err := json.Unmarshal(catalog, &doc); err != nil {
		return nil, Permanentf("crawler: reading catalog for filter: %v", err)
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
