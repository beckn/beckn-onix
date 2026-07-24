// Package catalogpublisher implements definition.CatalogPublisher: given a
// publisher's catalog submissions, it produces a signed DeDi manifest and a
// signed catalog index whose wire shape matches exactly what
// pkg/plugin/implementation/catalogcrawler consumes (see
// onix-catalog-crawler-plugin-requirements.md §2 for the three-level
// chain). This is the producing side of that same chain.
//
// Publish diffs each submission against caller-supplied PriorState (added/
// updated/removed items in the catalog's "resources" array) and emits
// either a fresh baseline (no prior state, or ForceBaseline) or a change
// file (prior state present and the diff is non-empty); an empty diff is a
// no-op. Publish holds no storage-backed state of its own -- see
// definition.PriorCatalogState's doc comment -- callers (a CLI, a handler)
// own reconstructing "what was last published" and pass it back in.
// Compaction is not implemented yet (see the package README's phased
// plan). Signing uses the real detached-JWS scheme
// (pkg/security/artifactsigner), the counterpart to catalogcrawler's fixed
// verifyDediProof. Output is JSON only; where these bytes get written and
// served is a separate concern (ArtifactStore, not yet built).
package catalogpublisher

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactsigner"
)

// catalogIndexRegistry must match catalogcrawler's isCatalogIndexFile
// convention -- it's how a crawler recognizes which manifest files[] entry
// is the catalog index.
const catalogIndexRegistry = "beckn-catalogs"

// Config controls publish behavior.
type Config struct {
	// KeyID is both the JWK "kid" embedded in the manifest/index and the
	// key identifier passed to KeyManager.Keyset to load the signing
	// keypair.
	KeyID string

	// Domain is the publisher's own domain, embedded as the index's
	// publisher.domain.
	Domain string

	// IndexURL is where the index will be reachable once published. A
	// placeholder ("pending-artifact-store://...") is used when unset --
	// there is no ArtifactStore yet to ask for a real location.
	IndexURL string

	// CatalogBaseURL, if set, is used as the URL prefix for catalog part
	// files; parts are addressed as
	// {CatalogBaseURL}/{catalogId}/{baseline.json | change-<version>.json}.
	// Same placeholder fallback as IndexURL when unset.
	CatalogBaseURL string
}

// Publisher implements definition.CatalogPublisher.
type Publisher struct {
	keyManager definition.KeyManager
	config     *Config
}

// New creates a Publisher instance.
func New(ctx context.Context, keyManager definition.KeyManager, cfg *Config) (*Publisher, func() error, error) {
	if keyManager == nil {
		return nil, nil, fmt.Errorf("catalogpublisher: KeyManager plugin not configured")
	}
	if cfg == nil || cfg.KeyID == "" {
		return nil, nil, fmt.Errorf("catalogpublisher: keyID is required")
	}
	p := &Publisher{keyManager: keyManager, config: cfg}
	return p, func() error { return nil }, nil
}

// --- DeDi wire types -------------------------------------------------
//
// Field-for-field identical (json tags) to catalogcrawler's private
// dedi* types, so the JSON this package emits is exactly what that
// package parses. Kept as separate, private types here rather than a
// shared package because this is a wire-format contract, not a Go type
// two packages should share code over.

type dediManifest struct {
	Keys  []dediKey  `json:"keys"`
	Files []dediFile `json:"files"`
	Proof *dediProof `json:"proof,omitempty"`
}

type dediKey struct {
	KID string `json:"kid"`
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
}

type dediProof struct {
	VerificationMethod string `json:"verification_method"`
	Jws                string `json:"jws"`
}

type dediFile struct {
	Registry string `json:"registry"`
	URL      string `json:"url"`
	Digest   string `json:"digest,omitempty"`
	Schema   string `json:"schema,omitempty"`
}

type dediIndex struct {
	Publisher dediIndexPublisher `json:"publisher"`
	Records   []dediIndexRecord  `json:"records"`
	Proof     *dediProof         `json:"proof,omitempty"`
}

type dediIndexPublisher struct {
	Domain string  `json:"domain"`
	Key    dediKey `json:"key"`
}

type dediIndexRecord struct {
	Details dediCatalogPointer `json:"details"`
}

type dediCatalogPointer struct {
	CatalogID   string     `json:"catalogId"`
	Version     int        `json:"version"`
	Status      string     `json:"status"`
	Visibility  string     `json:"visibility"`
	SchemaTypes []string   `json:"schemaTypes"`
	Baseline    dediPart   `json:"baseline"`
	Changes     []dediPart `json:"changes,omitempty"`

	// Parts flattens Baseline+Changes into one list for crawlers that
	// don't yet understand baseline/change semantics -- catalogcrawler
	// today fetches every Parts[] entry independently and validates each
	// against digest+shallow-schema (delta-file support is an explicit
	// open item there). Keeping this flattened view means a baseline-only
	// publish (this catalog's first) is still exactly what the crawler's
	// existing round-trip test expects.
	Parts []dediPart `json:"parts"`
}

type dediPart struct {
	URL    string `json:"url"`
	Digest string `json:"digest"` // "sha-256:<hex>"
}

// changeFileDoc is the change-file shape for one publish: just the added
// or updated items and the ids of removed ones (design doc, "Incremental
// updates"). Version records which catalog version this change corresponds
// to, purely for readability when a file is opened directly.
type changeFileDoc struct {
	Version int               `json:"version"`
	Added   []json.RawMessage `json:"added,omitempty"`
	Updated []json.RawMessage `json:"updated,omitempty"`
	Removed []string          `json:"removed,omitempty"`
}

// Publish validates each submission, diffs it against any PriorState for
// its catalogId, builds the resulting catalog-index entry (baseline or
// change file), and signs the index and manifest. A submission that fails
// validation or diffing is reported as a non-fatal definition.PublishError
// and skipped; it does not fail the rest of the batch.
func (p *Publisher) Publish(ctx context.Context, req definition.PublishRequest) (definition.PublishResult, error) {
	result := definition.PublishResult{PublishedAt: time.Now()}

	keyset, err := p.keyManager.Keyset(ctx, p.config.KeyID)
	if err != nil {
		return result, fmt.Errorf("catalogpublisher: loading keyset %q: %w", p.config.KeyID, err)
	}
	priv, pub, err := decodeKeyset(keyset)
	if err != nil {
		return result, fmt.Errorf("catalogpublisher: decoding keyset %q: %w", p.config.KeyID, err)
	}

	var records []dediIndexRecord
	for _, sub := range req.Catalogs {
		if err := validateSubmission(sub); err != nil {
			result.Errors = append(result.Errors, definition.PublishError{
				CatalogID: sub.CatalogID, Stage: "validate", Reason: err.Error(), Fatal: false,
			})
			continue
		}

		outcome, record, err := p.publishOne(sub, req.PriorState[sub.CatalogID], req.ForceBaseline)
		if err != nil {
			result.Errors = append(result.Errors, definition.PublishError{
				CatalogID: sub.CatalogID, Stage: "diff", Reason: err.Error(), Fatal: false,
			})
			continue
		}

		records = append(records, record)
		result.Catalogs = append(result.Catalogs, outcome)
	}

	jwk := dediKey{KID: p.config.KeyID, Kty: "OKP", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(pub)}

	signedIndex, err := p.signDocument(dediIndex{
		Publisher: dediIndexPublisher{Domain: p.config.Domain, Key: jwk},
		Records:   records,
	}, priv, p.config.KeyID)
	if err != nil {
		return result, fmt.Errorf("catalogpublisher: signing index: %w", err)
	}
	result.Index = signedIndex

	// Per the design doc, the manifest references the index by URL alone,
	// with no whole-file digest -- integrity for the index itself comes
	// from its own signature, not a manifest-declared hash.
	signedManifest, err := p.signDocument(dediManifest{
		Keys:  []dediKey{jwk},
		Files: []dediFile{{Registry: catalogIndexRegistry, URL: p.indexURL()}},
	}, priv, p.config.KeyID)
	if err != nil {
		return result, fmt.Errorf("catalogpublisher: signing manifest: %w", err)
	}
	result.Manifest = signedManifest

	return result, nil
}

// publishOne decides baseline vs. change-file for one submission and
// builds both its definition.CatalogPublishOutcome and its index record.
// hasPrior is implicit in prior being the zero value only when the
// catalogId truly has no entry in req.PriorState -- callers distinguish
// via the map's ok-form before calling this, so a zero-value
// PriorCatalogState here always means "no prior state".
func (p *Publisher) publishOne(sub definition.CatalogSubmission, prior definition.PriorCatalogState, forceBaseline bool) (definition.CatalogPublishOutcome, dediIndexRecord, error) {
	hasPrior := prior.Catalog != nil

	var (
		version      int
		mode         string
		content      json.RawMessage
		digestHex    string
		changed      bool
		baselinePart dediPart
		changeParts  []dediPart
	)

	if !hasPrior || forceBaseline {
		version = 1
		mode = "baseline"
		changed = true
		content = sub.Catalog
		digestHex = "sha-256:" + digestOf(content)
		baselinePart = dediPart{URL: p.catalogPartURL(sub.CatalogID, "baseline.json"), Digest: digestHex}
	} else {
		diff, err := diffCatalogs(prior.Catalog, sub.Catalog)
		if err != nil {
			return definition.CatalogPublishOutcome{}, dediIndexRecord{}, err
		}
		baselinePart = toDediPart(prior.BaselinePart)
		changeParts = toDediParts(prior.ChangeParts)

		if diff.isEmpty() {
			version = prior.Version
			mode = "unchanged"
			changed = false
		} else {
			version = prior.Version + 1
			mode = "change"
			changed = true
			change := changeFileDoc{Version: version, Added: diff.Added, Updated: diff.Updated, Removed: diff.Removed}
			var err error
			content, err = json.Marshal(change)
			if err != nil {
				return definition.CatalogPublishOutcome{}, dediIndexRecord{}, fmt.Errorf("marshaling change file: %w", err)
			}
			digestHex = "sha-256:" + digestOf(content)
			changeParts = append(changeParts, dediPart{
				URL:    p.catalogPartURL(sub.CatalogID, fmt.Sprintf("change-%d.json", version)),
				Digest: digestHex,
			})
		}
	}

	allParts := append([]dediPart{baselinePart}, changeParts...)
	record := dediIndexRecord{Details: dediCatalogPointer{
		CatalogID:   sub.CatalogID,
		Version:     version,
		Status:      "ACTIVE",
		Visibility:  encodeVisibility(sub.Visibility),
		SchemaTypes: sub.SchemaTypes,
		Baseline:    baselinePart,
		Changes:     changeParts,
		Parts:       allParts,
	}}

	outcome := definition.CatalogPublishOutcome{
		CatalogID: sub.CatalogID,
		Version:   version,
		Changed:   changed,
		Digest:    digestHex,
		Mode:      mode,
		Content:   content,
	}
	return outcome, record, nil
}

func toDediPart(pr *definition.PartRef) dediPart {
	if pr == nil {
		return dediPart{}
	}
	return dediPart{URL: pr.URL, Digest: pr.Digest}
}

func toDediParts(prs []definition.PartRef) []dediPart {
	if len(prs) == 0 {
		return nil
	}
	out := make([]dediPart, len(prs))
	for i, pr := range prs {
		out[i] = dediPart{URL: pr.URL, Digest: pr.Digest}
	}
	return out
}

// catalogDiff is the result of comparing two catalogs' "resources" arrays
// by item id.
type catalogDiff struct {
	Added   []json.RawMessage
	Updated []json.RawMessage
	Removed []string
}

func (d catalogDiff) isEmpty() bool {
	return len(d.Added) == 0 && len(d.Updated) == 0 && len(d.Removed) == 0
}

// diffCatalogs compares prior and next by their top-level "resources"
// array, matched by each resource's "id" field -- the only structure the
// design doc's change-file model assumes (added/updated items plus
// removed ids). id/descriptor/provider are not diffed: change files only
// ever carry resource-level deltas.
func diffCatalogs(prior, next json.RawMessage) (catalogDiff, error) {
	priorItems, err := resourcesByID(prior)
	if err != nil {
		return catalogDiff{}, fmt.Errorf("prior catalog: %w", err)
	}
	nextItems, nextIDs, err := resourcesByIDOrdered(next)
	if err != nil {
		return catalogDiff{}, fmt.Errorf("submitted catalog: %w", err)
	}

	var diff catalogDiff
	for _, id := range nextIDs {
		item := nextItems[id]
		if old, ok := priorItems[id]; !ok {
			diff.Added = append(diff.Added, item)
		} else if !jsonEqual(old, item) {
			diff.Updated = append(diff.Updated, item)
		}
	}
	for id := range priorItems {
		if _, ok := nextItems[id]; !ok {
			diff.Removed = append(diff.Removed, id)
		}
	}
	sort.Strings(diff.Removed)
	return diff, nil
}

// resourcesByID parses a catalog's "resources" array into an id-keyed map.
func resourcesByID(catalog json.RawMessage) (map[string]json.RawMessage, error) {
	m, _, err := resourcesByIDOrdered(catalog)
	return m, err
}

// resourcesByIDOrdered is resourcesByID plus the ids in their original
// array order, so diff output (Added/Updated) is deterministic rather than
// depending on Go's randomized map iteration order.
func resourcesByIDOrdered(catalog json.RawMessage) (map[string]json.RawMessage, []string, error) {
	var shape struct {
		Resources []json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(catalog, &shape); err != nil {
		return nil, nil, fmt.Errorf("parsing resources: %w", err)
	}
	m := make(map[string]json.RawMessage, len(shape.Resources))
	ids := make([]string, 0, len(shape.Resources))
	for _, r := range shape.Resources {
		id, err := resourceID(r)
		if err != nil {
			return nil, nil, err
		}
		m[id] = r
		ids = append(ids, id)
	}
	return m, ids, nil
}

func resourceID(raw json.RawMessage) (string, error) {
	var withID struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &withID); err != nil {
		return "", fmt.Errorf("parsing resource: %w", err)
	}
	if withID.ID == "" {
		return "", fmt.Errorf("resource missing id")
	}
	return withID.ID, nil
}

// jsonEqual compares two JSON values semantically (decoded structure, not
// raw bytes) so whitespace/key-order differences don't register as an
// update.
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// signDocument marshals doc, signs the result (per §7: JCS-canonicalize
// with "proof" absent/removed, detached JWS), then re-marshals with the
// proof attached. doc is passed by value as an "any" because dediManifest
// and dediIndex share no common signable interface; both are simple
// structs with an optional trailing *dediProof field the caller sets after
// the first marshal.
func (p *Publisher) signDocument(doc any, priv ed25519.PrivateKey, kid string) (json.RawMessage, error) {
	unsigned, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshaling document: %w", err)
	}
	jws, err := artifactsigner.SignDetachedJWS(unsigned, priv)
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	proof := &dediProof{VerificationMethod: kid, Jws: jws}

	switch d := doc.(type) {
	case dediManifest:
		d.Proof = proof
		return json.Marshal(d)
	case dediIndex:
		d.Proof = proof
		return json.Marshal(d)
	default:
		return nil, fmt.Errorf("signDocument: unsupported document type %T", doc)
	}
}

// decodeKeyset decodes a model.Keyset's base64-encoded signing keypair into
// raw Ed25519 keys, matching the exact encoding convention
// simplekeymanager/keymanager already use: SigningPrivate is
// base64(seed), expanded via ed25519.NewKeyFromSeed; SigningPublic is
// base64(rawPublicKey).
func decodeKeyset(keyset *model.Keyset) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	if keyset == nil {
		return nil, nil, fmt.Errorf("nil keyset")
	}
	seed, err := base64.StdEncoding.DecodeString(keyset.SigningPrivate)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding signing private key: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, nil, fmt.Errorf("invalid signing private key length %d, want %d", len(seed), ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)

	pub, err := base64.StdEncoding.DecodeString(keyset.SigningPublic)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding signing public key: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return nil, nil, fmt.Errorf("invalid signing public key length %d, want %d", len(pub), ed25519.PublicKeySize)
	}

	return priv, ed25519.PublicKey(pub), nil
}

// digestOf returns the hex-encoded SHA-256 of body, matching
// artifactfetcher's digest convention (plain hex, no prefix -- the
// "sha-256:" prefix is added by the caller when building a dediPart/dediFile
// digest field, matching the reference fixture's convention).
func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// validateSubmission applies the same shallow structural check
// catalogcrawler applies on the way in (looksLikeBecknCatalog): a Beckn
// Catalog object has no context/message envelope, just {id, descriptor,
// ...}. Keeping this duplicated rather than shared avoids a cross-package
// dependency between the two plugins for a few lines of logic; it should
// be lifted into a shared validator once a real schemaValidator plugin call
// replaces both inline checks (tracked as an open item on both sides).
func validateSubmission(sub definition.CatalogSubmission) error {
	if sub.CatalogID == "" {
		return fmt.Errorf("missing catalogId")
	}
	var catalog struct {
		ID         string          `json:"id"`
		Descriptor json.RawMessage `json:"descriptor"`
	}
	if err := json.Unmarshal(sub.Catalog, &catalog); err != nil {
		return fmt.Errorf("invalid catalog JSON: %w", err)
	}
	if catalog.ID == "" || len(catalog.Descriptor) == 0 {
		return fmt.Errorf("catalog missing id/descriptor")
	}
	return nil
}

// encodeVisibility is a best-effort, not-yet-standardized string encoding
// of definition.PublishVisibility (see the type's doc comment: the real
// wire convention for restricted-catalog visibility is an open design
// item). catalogcrawler currently only passes this string through
// (CatalogResult.Visibility); it does not parse or filter on it yet.
func encodeVisibility(v definition.PublishVisibility) string {
	if v.Public || len(v.Networks) == 0 {
		return "public"
	}
	return "networks:" + strings.Join(v.Networks, ",")
}

// indexURL returns the configured index location, or a placeholder when
// no ArtifactStore-assigned location exists yet.
func (p *Publisher) indexURL() string {
	if p.config.IndexURL != "" {
		return p.config.IndexURL
	}
	return "pending-artifact-store://index.json"
}

// catalogPartURL returns the configured location for one of a catalog's
// part files (baseline.json, change-<version>.json, ...), or a placeholder
// when no ArtifactStore-assigned location exists yet.
func (p *Publisher) catalogPartURL(catalogID, filename string) string {
	if p.config.CatalogBaseURL != "" {
		return strings.TrimRight(p.config.CatalogBaseURL, "/") + "/" + catalogID + "/" + filename
	}
	return "pending-artifact-store://catalog/" + catalogID + "/" + filename
}
