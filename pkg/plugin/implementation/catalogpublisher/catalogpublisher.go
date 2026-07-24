// Package catalogpublisher implements definition.CatalogPublisher: given a
// publisher's catalog submissions, it produces a signed DeDi manifest and a
// catalog index whose wire shape matches "Decentralized Catalog file
// spec.md" (the file-spec doc supersedes the earlier DeDi-wrapper-shaped
// index this package originally produced -- see git history for that
// version; catalogcrawler has not yet been updated to match this shape,
// tracked as the immediate next step).
//
// Publish diffs each submission against caller-supplied PriorState (added/
// updated/removed items in the catalog's "resources" and "offers" arrays)
// and emits either a fresh baseline (no prior state, or ForceBaseline) or a
// change file (prior state present and the diff is non-empty); an empty
// diff is a no-op. Publish holds no storage-backed state of its own -- see
// definition.PriorCatalogState's doc comment -- callers (a CLI, a handler)
// own reconstructing "what was last published" and pass it back in.
// Compaction beyond ForceBaseline-as-manual-trigger is not implemented yet
// (see the package README's phased plan).
//
// Signing is two different schemes for two different documents, per the
// file spec: the manifest carries one whole-document detached JWS
// (pkg/security/artifactsigner, JCS canonicalization per RFC 8785, RFC 7515
// detached JWS); the catalog index is not signed as a whole -- instead,
// every baseline/change file entry carries its own signature, a plain
// Ed25519 signature over the JCS-canonicalized tuple
// {catalogId, version, url, digest, validUntil}, binding that signature to
// exactly one file in one role (file spec, "The signed entry is a tuple,
// not a bare hash").
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

// dediVersion matches the file spec's manifest example byte for byte.
const dediVersion = "0.1"

// catalogIndexFileName is the manifest files[].name value identifying the
// catalog-index file (file spec's "becknCatalogs" -- the Beckn extension
// replacing DeDi's registry-name convention with a plain name key, since
// this entry references a Beckn file, not a DeDi registry).
const catalogIndexFileName = "becknCatalogs"

// defaultFileValidity is the fallback used when neither Config.FileValidityIn
// nor Config.NextUpdateIn is set -- see its use in Publish for why zero is
// unsafe here (unlike next_update, a file's validUntil can't just be
// omitted).
const defaultFileValidity = 24 * time.Hour

// Config controls publish behavior.
type Config struct {
	// KeyID is both the JWK "kid" embedded in the manifest and the
	// signature.keyId on every catalog-index file entry, and the key
	// identifier passed to KeyManager.Keyset to load the signing keypair.
	// The file spec uses one key for both roles throughout its examples;
	// nothing requires they differ.
	KeyID string

	// Domain is the publisher's own domain, embedded as the manifest's
	// top-level "domain" and the catalog index's "participantId" (file
	// spec: identity is the domain).
	Domain string

	// IndexSchemaURL is the JSON-Schema URL describing the catalog-index
	// document shape, embedded as the manifest's files[].schema.
	IndexSchemaURL string

	// IndexNetworkIds scopes the catalog index itself (not any one
	// catalog) to specific networks; embedded as the manifest's
	// files[].networkIds. Empty/nil means public.
	IndexNetworkIds []string

	// IndexAuthMethods is only meaningful when IndexNetworkIds is
	// non-empty (file spec: "A restricted index adds authMethods beside
	// networkIds").
	IndexAuthMethods []definition.AuthMethod

	// NextUpdateIn sets how far in the future the manifest's and index's
	// "next_update" freshness window extends from the moment of
	// publishing. Zero omits next_update entirely.
	NextUpdateIn time.Duration

	// FileValidityIn sets how far in the future each catalog file's
	// signature.validUntil extends. Falls back to NextUpdateIn when zero.
	FileValidityIn time.Duration

	// IndexURL is where the catalog index will be reachable once
	// published. A placeholder ("pending-artifact-store://...") is used
	// when unset -- there is no ArtifactStore yet to ask for a real
	// location.
	IndexURL string

	// CatalogBaseURL, if set, is used as the URL prefix for catalog part
	// files; parts are addressed as
	// {CatalogBaseURL}/{localName}.v{version}.json (baseline) or
	// {CatalogBaseURL}/{localName}.v{version}.changes.json (change file),
	// where localName is CatalogID with any "domain/" prefix stripped
	// (file spec's example: catalogId
	// "open-economy.nfh.global/electronics-2026" -> file
	// "electronics-2026.v40.json"). Same placeholder fallback as IndexURL
	// when unset.
	CatalogBaseURL string

	// ExtraManifestFiles are additional manifest files[] entries appended
	// after the catalog-index entry (the manifest's files[] is a list of
	// every registry/file the domain offers, of which the catalog index
	// is only one). These are pass-through references the caller
	// supplies as-is; Publish never computes a digest for them (the file
	// spec's manifest file entries never carry one at all).
	ExtraManifestFiles []ManifestFileRef
}

// ManifestFileRef is one additional manifest files[] entry, supplied
// verbatim by the caller (see Config.ExtraManifestFiles).
type ManifestFileRef struct {
	Name        string
	URL         string
	Schema      string
	NetworkIds  []string
	AuthMethods []definition.AuthMethod
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

// --- Manifest wire types -----------------------------------------------
//
// DeDi's manifest format (file spec: "keys as JWKs, a files list naming
// each registry the domain offers, freshness fields, and a proof block"),
// with the file spec's three Beckn extensions on the file entry: no
// digest, networkIds added, and "name" in place of DeDi's "registry".

type dediManifest struct {
	DediVersion string     `json:"dedi_version"`
	Type        string     `json:"type"` // "dedi-manifest"
	Domain      string     `json:"domain"`
	Keys        []dediKey  `json:"keys"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	NextUpdate  *time.Time `json:"next_update,omitempty"`
	Files       []dediFile `json:"files"`
	Proof       *dediProof `json:"proof,omitempty"`
}

type dediKey struct {
	KID string `json:"kid"`
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
}

type dediProof struct {
	VerificationMethod string `json:"verification_method"`
	Canonicalization   string `json:"canonicalization"` // always "JCS" here, per file spec
	Jws                string `json:"jws"`
}

type dediFile struct {
	Name        string           `json:"name"`
	URL         string           `json:"url"`
	Schema      string           `json:"schema,omitempty"`
	NetworkIds  []string         `json:"networkIds,omitempty"`
	AuthMethods []authMethodWire `json:"authMethods,omitempty"`
}

type authMethodWire struct {
	Method           string   `json:"method"`
	Algorithm        string   `json:"algorithm"`
	Header           string   `json:"header"`
	Challenge        []string `json:"challenge,omitempty"`
	FreshnessSeconds int      `json:"freshnessSeconds,omitempty"`
}

func toAuthMethodWire(methods []definition.AuthMethod) []authMethodWire {
	if len(methods) == 0 {
		return nil
	}
	out := make([]authMethodWire, len(methods))
	for i, m := range methods {
		out[i] = authMethodWire{
			Method:           m.Method,
			Algorithm:        m.Algorithm,
			Header:           m.Header,
			Challenge:        m.Challenge,
			FreshnessSeconds: m.FreshnessSeconds,
		}
	}
	return out
}

// --- Catalog index wire types -------------------------------------------
//
// A plain Beckn file; DeDi never reads it, and it is not required to be
// signed as a whole -- trust rides on each file entry's own signature
// tuple (file spec, "The catalog index").

type catalogIndexDoc struct {
	ParticipantID string            `json:"participantId"`
	Version       int               `json:"version"`
	NextUpdate    *time.Time        `json:"next_update,omitempty"`
	Catalogs      []json.RawMessage `json:"catalogs"`
}

type catalogEntry struct {
	CatalogID   string           `json:"catalogId"`
	CatalogType string           `json:"catalogType,omitempty"`
	Status      string           `json:"status"`
	NetworkIds  []string         `json:"networkIds,omitempty"`
	AuthMethods []authMethodWire `json:"authMethods,omitempty"`
	SchemaTypes []string         `json:"schemaTypes,omitempty"`
	Baseline    *fileEntry       `json:"baseline,omitempty"`
	Changes     []fileEntry      `json:"changes,omitempty"`
	RetiredAt   *time.Time       `json:"retiredAt,omitempty"`
}

type fileEntry struct {
	Version   int           `json:"version"`
	URL       string        `json:"url"`
	Size      int64         `json:"size"`
	Digest    string        `json:"digest"` // "sha-256:<hex>"
	Signature signatureWire `json:"signature"`
}

type signatureWire struct {
	KeyID      string    `json:"keyId"`
	Value      string    `json:"value"`
	ValidUntil time.Time `json:"validUntil"`
}

// changeFileDoc is the change-file shape for one publish, keyed by id never
// by position (file spec, "Catalog files and change files"). Upserts merge
// added and updated items into one list -- the receiver replaces by id
// either way -- and Removals names ids only.
type changeFileDoc struct {
	CatalogID   string          `json:"catalogId"`
	FromVersion int             `json:"fromVersion"`
	ToVersion   int             `json:"toVersion"`
	Resources   diffBlock       `json:"resources"`
	Offers      diffBlock       `json:"offers"`
	Catalog     json.RawMessage `json:"catalog,omitempty"`
}

type diffBlock struct {
	Upserts  []json.RawMessage `json:"upserts,omitempty"`
	Removals []string          `json:"removals,omitempty"`
}

func (b diffBlock) isEmpty() bool { return len(b.Upserts) == 0 && len(b.Removals) == 0 }

// Publish validates each submission, diffs it against any PriorState for
// its catalogId, builds the resulting catalog-index entry (baseline,
// change file, or a carried-forward no-op), folds in retirements and
// carried-forward untouched entries, and signs the manifest (the index
// itself is not signed as a whole). A submission that fails validation or
// diffing is reported as a non-fatal definition.PublishError and skipped;
// it does not fail the rest of the batch.
func (p *Publisher) Publish(ctx context.Context, req definition.PublishRequest) (definition.PublishResult, error) {
	now := time.Now()
	result := definition.PublishResult{PublishedAt: now}

	keyset, err := p.keyManager.Keyset(ctx, p.config.KeyID)
	if err != nil {
		return result, fmt.Errorf("catalogpublisher: loading keyset %q: %w", p.config.KeyID, err)
	}
	priv, pub, err := decodeKeyset(keyset)
	if err != nil {
		return result, fmt.Errorf("catalogpublisher: decoding keyset %q: %w", p.config.KeyID, err)
	}

	var nextUpdate *time.Time
	if p.config.NextUpdateIn > 0 {
		t := now.Add(p.config.NextUpdateIn)
		nextUpdate = &t
	}

	fileValidityIn := p.config.FileValidityIn
	if fileValidityIn <= 0 {
		fileValidityIn = p.config.NextUpdateIn
	}
	if fileValidityIn <= 0 {
		// Every file entry's signature.validUntil is a mandatory part of
		// the signed tuple (unlike next_update, it cannot simply be
		// omitted) -- silently falling back to now+0 would sign a file
		// that is already expired the instant a crawler checks it.
		fileValidityIn = defaultFileValidity
	}
	validUntil := now.Add(fileValidityIn)

	submitted := make(map[string]bool, len(req.Catalogs))
	retireSet := make(map[string]bool, len(req.Retire))
	for _, id := range req.Retire {
		retireSet[id] = true
	}

	anyChanged := false
	var entries []json.RawMessage

	for _, sub := range req.Catalogs {
		submitted[sub.CatalogID] = true
		if err := validateSubmission(sub); err != nil {
			result.Errors = append(result.Errors, definition.PublishError{
				CatalogID: sub.CatalogID, Stage: "validate", Reason: err.Error(), Fatal: false,
			})
			continue
		}

		outcome, entry, changed, err := p.publishOne(sub, req.PriorState[sub.CatalogID], req.ForceBaseline, now, validUntil, priv)
		if err != nil {
			result.Errors = append(result.Errors, definition.PublishError{
				CatalogID: sub.CatalogID, Stage: "diff", Reason: err.Error(), Fatal: false,
			})
			continue
		}

		raw, err := json.Marshal(entry)
		if err != nil {
			return result, fmt.Errorf("catalogpublisher: marshaling catalog entry %q: %w", sub.CatalogID, err)
		}
		entries = append(entries, raw)
		result.Catalogs = append(result.Catalogs, outcome)
		if changed {
			anyChanged = true
		}
	}

	for id := range retireSet {
		if submitted[id] {
			continue // submitting and retiring the same catalogId in one call: submission wins
		}
		tomb := catalogEntry{CatalogID: id, Status: "RETIRED", RetiredAt: &now}
		raw, err := json.Marshal(tomb)
		if err != nil {
			return result, fmt.Errorf("catalogpublisher: marshaling tombstone %q: %w", id, err)
		}
		entries = append(entries, raw)
		anyChanged = true
	}

	for _, raw := range req.CarryForward {
		var probe struct {
			CatalogID string `json:"catalogId"`
		}
		if json.Unmarshal(raw, &probe) == nil && (submitted[probe.CatalogID] || retireSet[probe.CatalogID]) {
			continue
		}
		entries = append(entries, raw)
	}

	indexVersion := req.PriorIndexVersion
	if anyChanged || indexVersion == 0 {
		indexVersion = req.PriorIndexVersion + 1
	}
	result.IndexVersion = indexVersion

	indexBytes, err := json.Marshal(catalogIndexDoc{
		ParticipantID: p.config.Domain,
		Version:       indexVersion,
		NextUpdate:    nextUpdate,
		Catalogs:      entries,
	})
	if err != nil {
		return result, fmt.Errorf("catalogpublisher: marshaling catalog index: %w", err)
	}
	result.Index = indexBytes

	jwk := dediKey{KID: p.config.KeyID, Kty: "OKP", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(pub)}

	files := []dediFile{{
		Name:        catalogIndexFileName,
		URL:         p.indexURL(),
		Schema:      p.config.IndexSchemaURL,
		NetworkIds:  p.config.IndexNetworkIds,
		AuthMethods: toAuthMethodWire(p.config.IndexAuthMethods),
	}}
	for _, extra := range p.config.ExtraManifestFiles {
		files = append(files, dediFile{
			Name:        extra.Name,
			URL:         extra.URL,
			Schema:      extra.Schema,
			NetworkIds:  extra.NetworkIds,
			AuthMethods: toAuthMethodWire(extra.AuthMethods),
		})
	}

	// Per the file spec, the manifest file entry carries no whole-file
	// digest: index churn never touches the domain root, and integrity
	// comes from the per-entry signatures inside the index instead.
	signedManifest, err := p.signManifest(dediManifest{
		DediVersion: dediVersion,
		Type:        "dedi-manifest",
		Domain:      p.config.Domain,
		Keys:        []dediKey{jwk},
		UpdatedAt:   &now,
		NextUpdate:  nextUpdate,
		Files:       files,
	}, priv, p.config.KeyID)
	if err != nil {
		return result, fmt.Errorf("catalogpublisher: signing manifest: %w", err)
	}
	result.Manifest = signedManifest

	return result, nil
}

// currentVersion returns a catalog's implicit current version: the last
// change file's version, or the baseline's version if there are no change
// files yet. Zero if there is no prior state at all.
func currentVersion(prior definition.PriorCatalogState) int {
	if n := len(prior.ChangeFiles); n > 0 {
		return prior.ChangeFiles[n-1].Version
	}
	if prior.BaselineFile != nil {
		return prior.BaselineFile.Version
	}
	return 0
}

// publishOne decides baseline vs. change-file vs. no-op for one submission
// and builds both its definition.CatalogPublishOutcome and its
// catalogEntry.
func (p *Publisher) publishOne(sub definition.CatalogSubmission, prior definition.PriorCatalogState, forceBaseline bool, now, validUntil time.Time, priv ed25519.PrivateKey) (definition.CatalogPublishOutcome, catalogEntry, bool, error) {
	hasPrior := prior.Catalog != nil
	catalogType := sub.CatalogType
	if catalogType == "" {
		catalogType = "REGULAR"
	}

	entry := catalogEntry{
		CatalogID:   sub.CatalogID,
		CatalogType: catalogType,
		Status:      "ACTIVE",
		NetworkIds:  sub.NetworkIds,
		AuthMethods: toAuthMethodWire(sub.AuthMethods),
		SchemaTypes: sub.SchemaTypes,
	}

	if hasPrior {
		entry.Baseline = fileRefToWire(prior.BaselineFile)
		entry.Changes = fileRefsToWire(prior.ChangeFiles)
	}

	if !hasPrior || forceBaseline {
		version := currentVersion(prior) + 1 // 0+1 == 1 for a brand-new catalog
		fe, err := p.buildFileEntry(sub.CatalogID, version, "json", sub.Catalog, now, validUntil, priv)
		if err != nil {
			return definition.CatalogPublishOutcome{}, catalogEntry{}, false, err
		}
		entry.Baseline = &fe
		entry.Changes = nil // a fresh baseline (first publish, or a forced compaction) resets the change list

		outcome := definition.CatalogPublishOutcome{
			CatalogID: sub.CatalogID, Version: version, Changed: true, Digest: fe.Digest, Mode: "baseline", Content: sub.Catalog,
		}
		return outcome, entry, true, nil
	}

	diff, changeCatalog, err := diffCatalogs(prior.Catalog, sub.Catalog)
	if err != nil {
		return definition.CatalogPublishOutcome{}, catalogEntry{}, false, err
	}

	if diff.Resources.isEmpty() && diff.Offers.isEmpty() && changeCatalog == nil {
		version := currentVersion(prior)
		outcome := definition.CatalogPublishOutcome{CatalogID: sub.CatalogID, Version: version, Changed: false, Mode: "unchanged"}
		return outcome, entry, false, nil
	}

	fromVersion := currentVersion(prior)
	toVersion := fromVersion + 1
	changeDoc := changeFileDoc{
		CatalogID: sub.CatalogID, FromVersion: fromVersion, ToVersion: toVersion,
		Resources: diff.Resources, Offers: diff.Offers, Catalog: changeCatalog,
	}
	content, err := json.Marshal(changeDoc)
	if err != nil {
		return definition.CatalogPublishOutcome{}, catalogEntry{}, false, fmt.Errorf("marshaling change file: %w", err)
	}

	fe, err := p.buildFileEntry(sub.CatalogID, toVersion, "changes.json", content, now, validUntil, priv)
	if err != nil {
		return definition.CatalogPublishOutcome{}, catalogEntry{}, false, err
	}
	entry.Changes = append(entry.Changes, fe)

	outcome := definition.CatalogPublishOutcome{
		CatalogID: sub.CatalogID, Version: toVersion, Changed: true, Digest: fe.Digest, Mode: "change", Content: content,
	}
	return outcome, entry, true, nil
}

// buildFileEntry computes a versioned URL, digest, size, and per-entry
// signature tuple for one catalog file (baseline or change), per the file
// spec's rules: immutable, versioned URLs, and a signature over
// {catalogId, version, url, digest, validUntil}.
func (p *Publisher) buildFileEntry(catalogID string, version int, suffix string, content []byte, now, validUntil time.Time, priv ed25519.PrivateKey) (fileEntry, error) {
	filename := fmt.Sprintf("%s.v%d.%s", localCatalogName(catalogID), version, suffix)
	url := p.catalogPartURL(filename)
	digest := "sha-256:" + digestOf(content)

	sigValue, err := artifactsigner.SignFileTuple(catalogID, version, url, digest, validUntil, priv)
	if err != nil {
		return fileEntry{}, fmt.Errorf("signing file entry for %q v%d: %w", catalogID, version, err)
	}

	return fileEntry{
		Version: version,
		URL:     url,
		Size:    int64(len(content)),
		Digest:  digest,
		Signature: signatureWire{
			KeyID:      p.keyID(),
			Value:      sigValue,
			ValidUntil: validUntil,
		},
	}, nil
}

func (p *Publisher) keyID() string { return p.config.KeyID }

// localCatalogName returns catalogID with any "domain/" prefix stripped,
// matching the file spec's example filenames (catalogId
// "open-economy.nfh.global/electronics-2026" -> "electronics-2026.v40.json").
func localCatalogName(catalogID string) string {
	if i := strings.LastIndex(catalogID, "/"); i != -1 {
		return catalogID[i+1:]
	}
	return catalogID
}

func fileRefToWire(fr *definition.FileRef) *fileEntry {
	if fr == nil {
		return nil
	}
	fe := fileRefValueToWire(*fr)
	return &fe
}

func fileRefValueToWire(fr definition.FileRef) fileEntry {
	return fileEntry{
		Version: fr.Version,
		URL:     fr.URL,
		Size:    fr.Size,
		Digest:  fr.Digest,
		Signature: signatureWire{
			KeyID:      fr.SignatureKeyID,
			Value:      fr.SignatureValue,
			ValidUntil: fr.SignatureValidUntil,
		},
	}
}

func fileRefsToWire(frs []definition.FileRef) []fileEntry {
	if len(frs) == 0 {
		return nil
	}
	out := make([]fileEntry, len(frs))
	for i, fr := range frs {
		out[i] = fileRefValueToWire(fr)
	}
	return out
}

// catalogDiff is the result of comparing two catalogs' "resources" and
// "offers" arrays by item id.
type catalogDiff struct {
	Resources diffBlock
	Offers    diffBlock
}

// diffCatalogs compares prior and next by their top-level "resources" and
// "offers" arrays, matched by each item's "id" field, and separately
// detects catalog-level attribute changes (currently: "descriptor",
// "provider" -- the file spec names "name, validity window" as examples
// without pinning an exact shape, so this is a best-effort subset, not a
// complete implementation of that field; tracked as an open item).
// changeCatalog is nil when no catalog-level attributes changed.
func diffCatalogs(prior, next json.RawMessage) (catalogDiff, json.RawMessage, error) {
	resourcesDiff, err := diffArrayField(prior, next, "resources")
	if err != nil {
		return catalogDiff{}, nil, fmt.Errorf("diffing resources: %w", err)
	}
	offersDiff, err := diffArrayField(prior, next, "offers")
	if err != nil {
		return catalogDiff{}, nil, fmt.Errorf("diffing offers: %w", err)
	}
	changeCatalog, err := diffCatalogAttributes(prior, next)
	if err != nil {
		return catalogDiff{}, nil, fmt.Errorf("diffing catalog attributes: %w", err)
	}
	return catalogDiff{Resources: resourcesDiff, Offers: offersDiff}, changeCatalog, nil
}

// diffArrayField diffs prior[field] against next[field] (each a
// json.RawMessage array, defaulting to empty when the field is absent),
// matched by item id, merging added+updated into one Upserts list.
func diffArrayField(prior, next json.RawMessage, field string) (diffBlock, error) {
	priorItems, err := itemsByID(prior, field)
	if err != nil {
		return diffBlock{}, fmt.Errorf("prior catalog: %w", err)
	}
	nextItems, nextIDs, err := itemsByIDOrdered(next, field)
	if err != nil {
		return diffBlock{}, fmt.Errorf("submitted catalog: %w", err)
	}

	var block diffBlock
	for _, id := range nextIDs {
		item := nextItems[id]
		if old, ok := priorItems[id]; !ok || !jsonEqual(old, item) {
			block.Upserts = append(block.Upserts, item)
		}
	}
	for id := range priorItems {
		if _, ok := nextItems[id]; !ok {
			block.Removals = append(block.Removals, id)
		}
	}
	sort.Strings(block.Removals)
	return block, nil
}

func itemsByID(catalog json.RawMessage, field string) (map[string]json.RawMessage, error) {
	m, _, err := itemsByIDOrdered(catalog, field)
	return m, err
}

// itemsByIDOrdered is itemsByID plus the ids in their original array
// order, so diff output (Upserts) is deterministic rather than depending
// on Go's randomized map iteration order.
func itemsByIDOrdered(catalog json.RawMessage, field string) (map[string]json.RawMessage, []string, error) {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(catalog, &shape); err != nil {
		return nil, nil, fmt.Errorf("parsing catalog: %w", err)
	}
	raw, ok := shape[field]
	if !ok || len(raw) == 0 {
		return map[string]json.RawMessage{}, nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", field, err)
	}
	m := make(map[string]json.RawMessage, len(items))
	ids := make([]string, 0, len(items))
	for _, item := range items {
		id, err := itemID(item)
		if err != nil {
			return nil, nil, err
		}
		m[id] = item
		ids = append(ids, id)
	}
	return m, ids, nil
}

func itemID(raw json.RawMessage) (string, error) {
	var withID struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &withID); err != nil {
		return "", fmt.Errorf("parsing item: %w", err)
	}
	if withID.ID == "" {
		return "", fmt.Errorf("item missing id")
	}
	return withID.ID, nil
}

// diffCatalogAttributes returns a non-nil json.RawMessage carrying only
// the catalog-level fields (currently: descriptor, provider) that changed
// between prior and next, or nil if none did.
func diffCatalogAttributes(prior, next json.RawMessage) (json.RawMessage, error) {
	var priorFields, nextFields map[string]json.RawMessage
	if err := json.Unmarshal(prior, &priorFields); err != nil {
		return nil, fmt.Errorf("parsing prior catalog: %w", err)
	}
	if err := json.Unmarshal(next, &nextFields); err != nil {
		return nil, fmt.Errorf("parsing submitted catalog: %w", err)
	}

	changed := map[string]json.RawMessage{}
	for _, field := range []string{"descriptor", "provider"} {
		nv, ok := nextFields[field]
		if !ok {
			continue
		}
		if pv, ok := priorFields[field]; !ok || !jsonEqual(pv, nv) {
			changed[field] = nv
		}
	}
	if len(changed) == 0 {
		return nil, nil
	}
	return json.Marshal(changed)
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

// signManifest marshals doc, signs the result (JCS-canonicalize with
// "proof" absent/removed, detached JWS per RFC 7515), then re-marshals
// with the proof attached.
func (p *Publisher) signManifest(doc dediManifest, priv ed25519.PrivateKey, kid string) (json.RawMessage, error) {
	unsigned, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshaling manifest: %w", err)
	}
	jws, err := artifactsigner.SignDetachedJWS(unsigned, priv)
	if err != nil {
		return nil, fmt.Errorf("signing manifest: %w", err)
	}
	doc.Proof = &dediProof{VerificationMethod: kid, Canonicalization: "JCS", Jws: jws}
	return json.Marshal(doc)
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
// "sha-256:" prefix is added by the caller when building a fileEntry
// digest field, matching the file spec's convention).
func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// validateSubmission applies the same shallow structural check
// catalogcrawler applies on the way in: a Beckn Catalog object has no
// context/message envelope, just {id, descriptor, ...}. Keeping this
// duplicated rather than shared avoids a cross-package dependency between
// the two plugins for a few lines of logic; it should be lifted into a
// shared validator once a real schemaValidator plugin call replaces both
// inline checks (tracked as an open item on both sides).
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

// indexURL returns the configured index location, or a placeholder when
// no ArtifactStore-assigned location exists yet.
func (p *Publisher) indexURL() string {
	if p.config.IndexURL != "" {
		return p.config.IndexURL
	}
	return "pending-artifact-store://catalog-index.json"
}

// catalogPartURL returns the configured location for one of a catalog's
// versioned file names (see buildFileEntry), or a placeholder when no
// ArtifactStore-assigned location exists yet.
func (p *Publisher) catalogPartURL(filename string) string {
	if p.config.CatalogBaseURL != "" {
		return strings.TrimRight(p.config.CatalogBaseURL, "/") + "/" + filename
	}
	return "pending-artifact-store://catalog/" + filename
}
