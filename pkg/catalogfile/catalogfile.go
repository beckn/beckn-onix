// Package catalogfile implements the file spec's change-file application
// rule ("Catalog files and change files"): a change file carries what
// changed between two consecutive versions, keyed by id never by
// position, as upserts (added or updated items, replaced by id) and
// removals (ids only). Both catalogpublisher's CLI (reconstructing prior
// state to diff against) and crawler (composing a catalog's
// current content from its baseline plus every change file since) apply
// change files the same way, so the logic lives here once rather than
// being duplicated on both sides of that chain.
package catalogfile

import (
	"encoding/json"
	"fmt"
)

// Doc is the top-level shape a Beckn Catalog carries (file spec: "the plain
// Beckn catalog JSON, exactly the schema used today"). Offers is optional --
// not every catalog carries one.
//
// id/descriptor/provider/resources/offers/isActive are named fields because
// callers throughout this package and the crawler read and write them
// directly (resource/offer diffing, filtering, batching, isActive stamping).
// Extra preserves every OTHER top-level field a real Catalog document can
// carry -- the file spec's own examples name "validity window" alongside
// descriptor/provider, and a domain may add its own -- so a baseline+changes
// fold, or a catalog-attribute patch naming a field that isn't one of the six
// above, never silently drops it. Custom MarshalJSON/UnmarshalJSON below
// route the known fields to their typed slots and everything else into
// Extra, in both directions.
type Doc struct {
	ID         json.RawMessage
	Descriptor json.RawMessage
	Provider   json.RawMessage
	Resources  []json.RawMessage
	Offers     []json.RawMessage
	// IsActive mirrors the pushed Catalog schema's own isActive (default true
	// there). It is a pointer so nil (never stamped) stays omitted on the wire
	// rather than us inventing a default -- see catalog.StampIsActive, the only
	// writer of this field via this path; a baseline/change file's own content
	// can also carry it directly (see Apply).
	IsActive *bool
	Extra    map[string]json.RawMessage
}

// nullRaw is what a zero-value json.RawMessage field marshals as, matching
// encoding/json's own behavior for an untagged (no omitempty) nil field.
var nullRaw = json.RawMessage("null")

// MarshalJSON emits id/descriptor/provider/resources/isActive unconditionally
// (matching the struct's pre-generic no-omitempty tags: an unset field
// marshals as null, not absent), offers/isActive only when set (omitempty),
// and every Extra field alongside them.
func (d Doc) MarshalJSON() ([]byte, error) {
	m := make(map[string]json.RawMessage, len(d.Extra)+6)
	for k, v := range d.Extra {
		m[k] = v
	}
	m["id"] = orNull(d.ID)
	m["descriptor"] = orNull(d.Descriptor)
	m["provider"] = orNull(d.Provider)
	resources, err := json.Marshal(d.Resources) // nil -> null, non-nil (even empty) -> []
	if err != nil {
		return nil, fmt.Errorf("catalogfile: marshaling resources: %w", err)
	}
	m["resources"] = resources
	if len(d.Offers) > 0 {
		offers, err := json.Marshal(d.Offers)
		if err != nil {
			return nil, fmt.Errorf("catalogfile: marshaling offers: %w", err)
		}
		m["offers"] = offers
	}
	if d.IsActive != nil {
		active, err := json.Marshal(*d.IsActive)
		if err != nil {
			return nil, err
		}
		m["isActive"] = active
	}
	return json.Marshal(m)
}

func orNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nullRaw
	}
	return raw
}

// UnmarshalJSON routes the six named fields to their typed slots and every
// other top-level field into Extra, so nothing beyond the fields this package
// actively reads/writes is ever silently dropped on a round trip.
func (d *Doc) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	extra := make(map[string]json.RawMessage, len(m))
	for field, v := range m {
		switch field {
		case "id":
			d.ID = v
		case "descriptor":
			d.Descriptor = v
		case "provider":
			d.Provider = v
		case "resources":
			if err := json.Unmarshal(v, &d.Resources); err != nil {
				return fmt.Errorf("catalogfile: parsing resources: %w", err)
			}
		case "offers":
			if err := json.Unmarshal(v, &d.Offers); err != nil {
				return fmt.Errorf("catalogfile: parsing offers: %w", err)
			}
		case "isActive":
			var active bool
			if err := json.Unmarshal(v, &active); err != nil {
				return fmt.Errorf("catalogfile: parsing isActive: %w", err)
			}
			d.IsActive = &active
		default:
			extra[field] = v
		}
	}
	d.Extra = extra
	return nil
}

// DiffBlock is one array's worth of upserts (added or updated items,
// applied by id) and removals (ids only).
type DiffBlock struct {
	Upserts  []json.RawMessage `json:"upserts,omitempty"`
	Removals []string          `json:"removals,omitempty"`
}

// IsEmpty reports whether this block carries no changes at all.
func (b DiffBlock) IsEmpty() bool { return len(b.Upserts) == 0 && len(b.Removals) == 0 }

// ChangeFileDoc is the change-file shape for one publish (file spec,
// "Catalog files and change files"): resources and offers are diffed
// independently, and Catalog optionally carries catalog-level attribute
// changes (currently: descriptor, provider -- see the "Deliberately not
// done" note in catalogpublisher's README for why this is a best-effort
// subset of that field, not a complete implementation of it).
type ChangeFileDoc struct {
	CatalogID   string          `json:"catalogId"`
	FromVersion int             `json:"fromVersion"`
	ToVersion   int             `json:"toVersion"`
	Resources   DiffBlock       `json:"resources"`
	Offers      DiffBlock       `json:"offers"`
	Catalog     json.RawMessage `json:"catalog,omitempty"`
}

// Apply folds one change file onto catalog's resources/offers arrays
// (upserts replace by id or append; removals drop by id) and overlays any
// catalog-level attribute changes, returning the resulting catalog bytes.
func Apply(catalog []byte, changeRaw []byte) ([]byte, error) {
	var doc Doc
	if err := json.Unmarshal(catalog, &doc); err != nil {
		return nil, fmt.Errorf("catalogfile: parsing catalog: %w", err)
	}
	var change ChangeFileDoc
	if err := json.Unmarshal(changeRaw, &change); err != nil {
		return nil, fmt.Errorf("catalogfile: parsing change file: %w", err)
	}

	resources, err := applyDiffBlock(doc.Resources, change.Resources)
	if err != nil {
		return nil, fmt.Errorf("catalogfile: applying resources: %w", err)
	}
	doc.Resources = resources

	offers, err := applyDiffBlock(doc.Offers, change.Offers)
	if err != nil {
		return nil, fmt.Errorf("catalogfile: applying offers: %w", err)
	}
	doc.Offers = offers

	if len(change.Catalog) > 0 {
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal(change.Catalog, &attrs); err != nil {
			return nil, fmt.Errorf("catalogfile: parsing catalog attribute changes: %w", err)
		}
		// Any catalog-level field the change file names is applied, not a fixed
		// list -- the file spec's own examples aren't limited to descriptor/
		// provider (e.g. "validity window"), and a domain may add its own.
		// resources/offers are deliberately NOT valid here even if named: they
		// have their own dedicated upserts/removals diffing above, never this
		// nested per-catalog-attribute patch, so overlaying them here would open
		// a second, conflicting path for the same content.
		if doc.Extra == nil {
			doc.Extra = map[string]json.RawMessage{}
		}
		for field, v := range attrs {
			switch field {
			case "descriptor":
				doc.Descriptor = v
			case "provider":
				doc.Provider = v
			case "isActive":
				var active bool
				if err := json.Unmarshal(v, &active); err != nil {
					return nil, fmt.Errorf("catalogfile: parsing isActive attribute change: %w", err)
				}
				doc.IsActive = &active
			case "resources", "offers":
				return nil, fmt.Errorf("catalogfile: %q is not a valid catalog-attribute field (it has its own dedicated diffing)", field)
			default:
				doc.Extra[field] = v
			}
		}
	}

	return json.Marshal(doc)
}

// applyDiffBlock applies one DiffBlock (upserts by id, replacing existing
// or appending new; removals by id) to items.
func applyDiffBlock(items []json.RawMessage, block DiffBlock) ([]json.RawMessage, error) {
	removed := make(map[string]bool, len(block.Removals))
	for _, id := range block.Removals {
		removed[id] = true
	}
	upserts := make(map[string]json.RawMessage, len(block.Upserts))
	for _, u := range block.Upserts {
		id, err := ItemID(u)
		if err != nil {
			return nil, err
		}
		upserts[id] = u
	}

	next := make([]json.RawMessage, 0, len(items)+len(block.Upserts))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		id, err := ItemID(item)
		if err != nil {
			return nil, err
		}
		seen[id] = true
		if removed[id] {
			continue
		}
		if u, ok := upserts[id]; ok {
			next = append(next, u)
			continue
		}
		next = append(next, item)
	}
	for _, u := range block.Upserts {
		id, _ := ItemID(u) // already validated above
		if !seen[id] {
			next = append(next, u)
		}
	}
	return next, nil
}

// ItemID extracts the "id" field from a resource/offer item.
func ItemID(raw json.RawMessage) (string, error) {
	var withID struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &withID); err != nil {
		return "", fmt.Errorf("catalogfile: parsing item: %w", err)
	}
	if withID.ID == "" {
		return "", fmt.Errorf("catalogfile: item missing id")
	}
	return withID.ID, nil
}
