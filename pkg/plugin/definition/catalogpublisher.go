package definition

import (
	"context"
	"encoding/json"
	"time"
)

// PublishVisibility declares which networks a catalog (or an index) is
// meant for. Public == true (no Networks) means visible to everyone; a
// non-public entry names the networks it is scoped to. The wire encoding of
// this into an index entry's "visibility" field is not yet a settled
// convention (see decentralized-catalog design doc, "Restricted catalogs"
// and onix-catalog-crawler-plugin-requirements.md §11's "visibleTo/
// schemaType manifest-level alias convention" open item) -- catalogpublisher
// encodes a best-effort string for now; catalogcrawler does not yet filter
// on it.
type PublishVisibility struct {
	Public   bool
	Networks []string
}

// PublishAuthMethod names one way a restricted catalog/index authenticates
// a crawler's fetch (e.g. "signed-challenge"). Params carries method-
// specific settings. This is a list, not an enum, so new methods can be
// added without changing the shape (design doc, "Restricted catalogs").
type PublishAuthMethod struct {
	Type   string
	Params map[string]string
}

// CatalogSubmission is one catalog's input to a publish call: the plain
// Beckn Catalog object (no context/message envelope) plus the publisher-
// declared metadata that only the publisher can know -- visibility and
// auth requirements are never derived from the catalog content itself.
type CatalogSubmission struct {
	CatalogID   string
	SchemaTypes []string
	Visibility  PublishVisibility
	AuthMethods []PublishAuthMethod
	Catalog     json.RawMessage
}

// PriorCatalogState is what a caller must supply, per catalogId, to get
// incremental (diffing) behavior instead of a fresh baseline. Publish is a
// pure function of (submissions, prior state) -> result; it never reads or
// writes any storage of its own. Catalog is the full, reconstructed
// content last published for this catalogId (baseline with every change
// file applied) -- diffing compares the new submission against this, not
// against the original baseline alone.
type PriorCatalogState struct {
	Version      int
	Catalog      json.RawMessage
	BaselinePart *PartRef
	ChangeParts  []PartRef
}

// PublishRequest is the input to CatalogPublisher.Publish.
type PublishRequest struct {
	Catalogs []CatalogSubmission

	// PriorState supplies, per catalogId, what was last published -- the
	// only way Publish can produce a change file instead of a fresh
	// baseline. A catalogId absent from this map is always published as a
	// new baseline, same as when ForceBaseline is set.
	PriorState map[string]PriorCatalogState

	// ForceBaseline bypasses diffing against PriorState entirely and always
	// emits a fresh baseline, discarding any prior version/parts for every
	// submitted catalog.
	ForceBaseline bool
}

// CatalogPublishOutcome reports what happened to one submitted catalog.
type CatalogPublishOutcome struct {
	CatalogID string
	Version   int
	Changed   bool // false = no-op: diffed against PriorState and found no added/updated/removed items
	Digest    string

	// Mode is "baseline" (fresh full-file publish), "change" (a diffed
	// delta was produced), or "unchanged" (Changed == false, no new
	// content). Content holds the new file's bytes for "baseline"/"change"
	// and is nil for "unchanged".
	Mode    string
	Content json.RawMessage
}

// PublishError is a non-fatal, per-catalog failure -- one bad submission
// must not fail the whole publish call, mirroring definition.CrawlError on
// the crawler side.
type PublishError struct {
	CatalogID string
	Stage     string // "validate" | "sign"
	Reason    string
	Fatal     bool
}

// PublishResult is the output of a Publish call: signed manifest and index
// JSON, ready to be handed to a storage layer (not this plugin's concern --
// see ArtifactStore, not yet built), plus per-catalog outcomes and errors.
type PublishResult struct {
	PublishedAt time.Time
	Manifest    json.RawMessage
	Index       json.RawMessage
	Catalogs    []CatalogPublishOutcome
	Errors      []PublishError
}

// CatalogPublisher turns a publisher's catalog submissions into signed,
// crawler-compatible manifest/index JSON. It is the producing side of the
// chain definition.Crawler consumes and verifies.
type CatalogPublisher interface {
	Publish(ctx context.Context, req PublishRequest) (PublishResult, error)
}

// CatalogPublisherProvider is the plugin constructor interface.
type CatalogPublisherProvider interface {
	New(ctx context.Context, keyManager KeyManager, config map[string]string) (CatalogPublisher, func() error, error)
}
