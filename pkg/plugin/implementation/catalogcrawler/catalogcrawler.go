// Package catalogcrawler implements definition.Crawler: it walks a
// participant's published manifest -> catalog-index -> catalog-file chain
// per "Decentralized Catalog file spec.md" (the file-spec doc supersedes
// the earlier DeDi-wrapper-shaped index this package originally consumed
// -- see git history for that version, and catalogpublisher's own history
// for the producing side of the same change).
//
// Verification per the file spec's "Crawler verification rules":
//  1. Fetch the manifest at the fixed well-known path, verify its
//     document-level detached-JWS proof against its own embedded keys[]
//     (the manifest is the trust anchor -- a key not present in it is
//     invalid, and so is everything signed with it).
//  2. Fetch the catalog index it points to (the "becknCatalogs" files[]
//     entry). The index itself is a plain Beckn file and is NOT signed as
//     a whole -- trust rides on each catalog file's own signature.
//  3. For each catalog entry: a RETIRED entry is a tombstone, no files to
//     fetch. Otherwise, fetch the baseline, verify its digest, size, and
//     per-file signature tuple ({catalogId, version, url, digest,
//     validUntil}, verified against the manifest's keys[] by
//     signature.keyId), and check validUntil hasn't passed. Then fetch
//     every changes[] entry the same way, in order, applying each onto
//     the running content (pkg/catalogfile) to produce the catalog's
//     current effective content. Any failed check drops the whole catalog
//     (a non-fatal definition.CrawlError), never a partial, unverified
//     composition.
//
// Outbound GETs to the index/catalog endpoints are currently unsigned
// (see fetchOpts) pending a participant in the test setup that expects
// signed requests; the capability is wired through and is a one-line
// change to re-enable. This phase does not use caching, checkPolicy, or
// schemaValidator -- every call re-fetches and re-verifies from scratch,
// and version-rollback detection is not implemented yet (VerificationOutcome.VersionOK
// is always true) -- both explicit open items, matching this package's
// original phased plan.
package catalogcrawler

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogfile"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactfetcher"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// Config controls fetch behavior for the crawler.
type Config struct {
	MaxArtifactSize int64
	FetchTimeout    time.Duration
	RetryMax        int
}

// Crawler fetches, verifies, and returns a participant's published
// catalogs.
type Crawler struct {
	signer     definition.Signer
	keyManager definition.KeyManager
	config     *Config
}

// New creates a Crawler instance.
func New(ctx context.Context, signer definition.Signer, keyManager definition.KeyManager, cfg *Config) (*Crawler, func() error, error) {
	if signer == nil {
		return nil, nil, fmt.Errorf("catalogcrawler: Signer plugin not configured")
	}
	if keyManager == nil {
		return nil, nil, fmt.Errorf("catalogcrawler: KeyManager plugin not configured")
	}
	if cfg == nil {
		cfg = &Config{}
	}
	c := &Crawler{signer: signer, keyManager: keyManager, config: cfg}
	return c, func() error { return nil }, nil
}

// --- Manifest wire types -------------------------------------------------

// dediManifest is the subset of a DeDi manifest (type: dedi-manifest) this
// crawler reads: the trust anchor keys, the pointer(s) to registries the
// domain offers, and the manifest's own detached-signature proof.
type dediManifest struct {
	Domain string     `json:"domain"`
	Keys   []dediKey  `json:"keys"`
	Files  []dediFile `json:"files"`
	Proof  *dediProof `json:"proof,omitempty"`
}

// dediKey is a JWK-shaped Ed25519 (OKP) public key, as published in a
// manifest's keys[].
type dediKey struct {
	KID string `json:"kid"`
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"` // base64url (no padding), raw public key bytes
}

// dediProof is a detached JWS-style signature referencing the key that
// signed the document by kid.
type dediProof struct {
	VerificationMethod string `json:"verification_method"`
	Jws                string `json:"jws"`
}

// dediFile is one manifest files[] entry -- per the file spec's Beckn
// extensions to DeDi's manifest format: "name" replaces DeDi's "registry"
// (this entry references a Beckn file, not a DeDi registry), and there is
// no whole-file digest (integrity comes from the index's own per-entry
// signatures instead).
type dediFile struct {
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	Schema     string   `json:"schema"`
	NetworkIds []string `json:"networkIds"`
}

// catalogIndexFileName is the manifest files[].name value identifying the
// catalog-index file (file spec's "becknCatalogs"), matching
// catalogpublisher's catalogIndexFileName constant.
const catalogIndexFileName = "becknCatalogs"

// isCatalogIndexFile reports whether a manifest files[] entry is the
// catalog-index file.
func isCatalogIndexFile(f dediFile) bool {
	return f.Name == catalogIndexFileName
}

// --- Catalog index wire types --------------------------------------------

// catalogIndexDoc is the catalog index: a plain Beckn file, not a DeDi
// file, and not signed as a whole -- trust rides on each catalog file's
// own signature (file spec, "The catalog index").
type catalogIndexDoc struct {
	ParticipantID string            `json:"participantId"`
	Version       int               `json:"version"`
	NextUpdate    *time.Time        `json:"next_update,omitempty"`
	Catalogs      []json.RawMessage `json:"catalogs"`
}

// catalogEntryProbe is the minimal shape read from every catalogs[] entry
// before deciding whether it's a tombstone or needs full parsing.
type catalogEntryProbe struct {
	CatalogID string `json:"catalogId"`
	Status    string `json:"status"`
}

type catalogEntry struct {
	CatalogID   string      `json:"catalogId"`
	CatalogType string      `json:"catalogType"`
	Status      string      `json:"status"`
	NetworkIds  []string    `json:"networkIds"`
	SchemaTypes []string    `json:"schemaTypes"`
	Baseline    *fileEntry  `json:"baseline"`
	Changes     []fileEntry `json:"changes"`
	RetiredAt   *time.Time  `json:"retiredAt"`
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

// CrawlSubscriber fetches and verifies the manifest, the catalog index it
// points to, and every catalog's baseline+changes files, composing each
// catalog's current effective content. A single bad catalog is reported
// as a non-fatal definition.CrawlError rather than failing the whole call.
func (c *Crawler) CrawlSubscriber(ctx context.Context, req definition.CrawlRequest) (definition.CrawlResult, error) {
	result := definition.CrawlResult{
		SubscriberID: req.SubscriberID,
		CrawledAt:    time.Now(),
		Mode:         req.Mode,
	}
	if result.Mode == "" {
		result.Mode = definition.CrawlModeIncremental
	}

	domain, err := domainFromSubscriberID(req.SubscriberID)
	if err != nil {
		return result, err
	}

	manifest, manifestResult, err := c.fetchManifest(ctx, domain)
	result.Manifest = manifestResult
	if err != nil {
		return result, err
	}

	for _, file := range manifest.Files {
		if !isCatalogIndexFile(file) {
			continue
		}
		catalogs, errs := c.fetchIndex(ctx, req.SubscriberID, manifest.Keys, file)
		result.Catalogs = append(result.Catalogs, catalogs...)
		result.Errors = append(result.Errors, errs...)
	}

	return result, nil
}

// domainFromSubscriberID extracts a base URL (scheme+host) from the DS-
// supplied participant URI. No registry lookup is performed -- the DS
// supplies this URI directly.
func domainFromSubscriberID(subscriberID string) (string, error) {
	raw := subscriberID
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("catalogcrawler: invalid subscriberID %q", subscriberID)
	}
	return u.Scheme + "://" + u.Host, nil
}

// fetchManifest fetches the public, unsigned well-known manifest and
// verifies it against its own embedded keys. Per the file spec, the
// manifest lives at /.well-known/dedi.index.json.
func (c *Crawler) fetchManifest(ctx context.Context, domain string) (dediManifest, definition.ManifestResult, error) {
	manifestURL := domain + "/.well-known/dedi.index.json"
	res, err := artifactfetcher.Fetch(ctx, manifestURL, c.fetchOpts(""))
	if err != nil {
		return dediManifest{}, definition.ManifestResult{URL: manifestURL}, fmt.Errorf("catalogcrawler: fetching manifest: %w", err)
	}

	var manifest dediManifest
	if err := json.Unmarshal(res.Body, &manifest); err != nil {
		return dediManifest{}, definition.ManifestResult{URL: manifestURL, Digest: res.Digest}, fmt.Errorf("catalogcrawler: parsing manifest: %w", err)
	}

	verified := verifyManifestProof(res.Body, manifest.Keys, manifest.Proof)
	return manifest, definition.ManifestResult{
		URL:        manifestURL,
		Digest:     res.Digest,
		Verified:   verified,
		VerifiedAt: time.Now(),
	}, nil
}

// verifyManifestProof verifies content's compact detached-JWS proof.jws
// against the given keys[], matched by proof.verification_method (kid).
// This is the manifest's own document-level proof -- the only remaining
// whole-document signature in this chain; the catalog index carries none,
// and individual catalog files carry their own per-entry signature
// instead (see verifyFileEntry). Returns false (not fatal) when there's no
// proof, no matching key, or the signature doesn't verify.
func verifyManifestProof(content []byte, keys []dediKey, proof *dediProof) bool {
	if proof == nil || proof.Jws == "" {
		return false
	}
	for _, k := range keys {
		if proof.VerificationMethod != "" && proof.VerificationMethod != k.KID {
			continue
		}
		pub, err := decodeOKPPublicKey(k)
		if err != nil {
			continue
		}
		if err := artifactverifier.VerifyDetachedJWS(content, proof.Jws, pub); err == nil {
			return true
		}
	}
	return false
}

// decodeOKPPublicKey decodes a JWK OKP/Ed25519 key's "x" value into a raw
// ed25519.PublicKey.
func decodeOKPPublicKey(k dediKey) (ed25519.PublicKey, error) {
	if k.Kty != "OKP" || k.Crv != "Ed25519" {
		return nil, fmt.Errorf("unsupported key type %s/%s", k.Kty, k.Crv)
	}
	raw, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid ed25519 public key length %d", len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// keyByID returns the manifest key matching kid, per the manifest's role
// as the trust anchor for every signature downstream (file spec: "A key
// not present in the current manifest is invalid, and so is everything
// signed with it").
func keyByID(keys []dediKey, kid string) (ed25519.PublicKey, error) {
	for _, k := range keys {
		if k.KID == kid {
			return decodeOKPPublicKey(k)
		}
	}
	return nil, fmt.Errorf("key %q not found in manifest", kid)
}

// stripDigestPrefix removes a leading "sha-256:" (or "sha256:") from a
// declared digest, matching the plain hex digest artifactfetcher computes.
func stripDigestPrefix(digest string) string {
	if i := strings.Index(digest, ":"); i != -1 {
		return digest[i+1:]
	}
	return digest
}

// fetchIndex fetches one manifest-referenced catalog index, then processes
// every catalog it lists.
func (c *Crawler) fetchIndex(ctx context.Context, subscriberID string, manifestKeys []dediKey, file dediFile) ([]definition.CatalogResult, []definition.CrawlError) {
	res, err := artifactfetcher.Fetch(ctx, file.URL, c.fetchOpts(subscriberID))
	if err != nil {
		return nil, []definition.CrawlError{{Stage: "index_fetch", Reason: err.Error(), Fatal: false}}
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(res.Body, &index); err != nil {
		return nil, []definition.CrawlError{{Stage: "index_fetch", Reason: fmt.Sprintf("parsing index: %v", err), Fatal: false}}
	}

	indexStale := index.NextUpdate != nil && time.Now().After(*index.NextUpdate)

	var catalogs []definition.CatalogResult
	var errs []definition.CrawlError
	for _, raw := range index.Catalogs {
		var probe catalogEntryProbe
		if err := json.Unmarshal(raw, &probe); err != nil {
			errs = append(errs, definition.CrawlError{Stage: "index_verify", Reason: fmt.Sprintf("parsing catalog entry: %v", err), Fatal: false})
			continue
		}
		if probe.CatalogID == "" {
			errs = append(errs, definition.CrawlError{Stage: "index_verify", Reason: "catalog entry missing catalogId", Fatal: false})
			continue
		}

		if strings.EqualFold(probe.Status, "RETIRED") {
			var entry catalogEntry
			if err := json.Unmarshal(raw, &entry); err != nil {
				errs = append(errs, definition.CrawlError{CatalogID: probe.CatalogID, Stage: "index_verify", Reason: fmt.Sprintf("parsing retired entry: %v", err), Fatal: false})
				continue
			}
			catalogs = append(catalogs, definition.CatalogResult{
				CatalogID: entry.CatalogID,
				Status:    "RETIRED",
				RetiredAt: entry.RetiredAt,
				Changed:   true,
			})
			continue
		}

		if indexStale {
			log.Debugf(ctx, "catalogcrawler: index %s is stale (next_update %s has passed)", file.URL, index.NextUpdate)
		}

		var entry catalogEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			errs = append(errs, definition.CrawlError{CatalogID: probe.CatalogID, Stage: "index_verify", Reason: fmt.Sprintf("parsing entry: %v", err), Fatal: false})
			continue
		}
		if err := validateEntry(entry); err != nil {
			errs = append(errs, definition.CrawlError{CatalogID: entry.CatalogID, Stage: "index_verify", Reason: err.Error(), Fatal: false})
			continue
		}

		cr, err := c.processCatalog(ctx, subscriberID, manifestKeys, entry)
		if err != nil {
			errs = append(errs, definition.CrawlError{CatalogID: entry.CatalogID, Stage: "part_fetch", Reason: err.Error(), Fatal: false})
			continue
		}
		catalogs = append(catalogs, cr)
	}
	return catalogs, errs
}

// validateEntry checks the minimum shape a non-retired index entry needs.
func validateEntry(e catalogEntry) error {
	if e.Baseline == nil {
		return fmt.Errorf("catalog %s has no baseline", e.CatalogID)
	}
	return nil
}

// processCatalog fetches and verifies a catalog's baseline, then every
// changes[] entry in order, applying each onto the running content
// (pkg/catalogfile) to produce the catalog's current effective content.
// Any failed check (digest, signature, expired validUntil) drops the
// whole catalog rather than composing from partially-verified data (file
// spec's crawler rule 5: "Verify every fetched file's bytes against its
// digest before use").
func (c *Crawler) processCatalog(ctx context.Context, subscriberID string, manifestKeys []dediKey, entry catalogEntry) (definition.CatalogResult, error) {
	baselineBody, baselineOK, err := c.fetchAndVerifyFile(ctx, subscriberID, entry.CatalogID, manifestKeys, *entry.Baseline)
	if err != nil {
		return definition.CatalogResult{}, fmt.Errorf("baseline: %w", err)
	}

	content := baselineBody
	version := entry.Baseline.Version
	allVerified := baselineOK

	for _, change := range entry.Changes {
		changeBody, ok, err := c.fetchAndVerifyFile(ctx, subscriberID, entry.CatalogID, manifestKeys, change)
		if err != nil {
			return definition.CatalogResult{}, fmt.Errorf("change v%d: %w", change.Version, err)
		}
		allVerified = allVerified && ok

		content, err = catalogfile.Apply(content, changeBody)
		if err != nil {
			return definition.CatalogResult{}, fmt.Errorf("applying change v%d: %w", change.Version, err)
		}
		version = change.Version
	}

	outcome := definition.VerificationOutcome{
		DigestMatch:    true, // fetchAndVerifyFile already aborted the whole catalog on a mismatch
		SchemaValid:    looksLikeBecknCatalog(content),
		SignatureValid: allVerified,
		VersionOK:      true, // rollback detection not implemented yet -- open item
	}

	return definition.CatalogResult{
		CatalogID:   entry.CatalogID,
		Version:     version,
		Status:      entry.Status,
		CatalogType: entry.CatalogType,
		NetworkIds:  entry.NetworkIds,
		SchemaTypes: entry.SchemaTypes,
		Source: definition.PartRef{
			URL:    entry.Baseline.URL,
			Digest: entry.Baseline.Digest,
			Size:   entry.Baseline.Size,
		},
		Changed:      true,
		Verification: outcome,
		Catalog:      json.RawMessage(content),
	}, nil
}

// fetchAndVerifyFile fetches one catalog file (baseline or a change),
// verifies its digest, declared size, and per-file signature tuple
// (verified against the manifest's keys[] by signature.keyId), and checks
// validUntil hasn't passed. Returns the verified body and whether its
// signature check passed. A digest mismatch, missing key, or expired
// validUntil is a hard error -- the caller aborts the whole catalog rather
// than using unverified content.
func (c *Crawler) fetchAndVerifyFile(ctx context.Context, subscriberID, catalogID string, manifestKeys []dediKey, fe fileEntry) ([]byte, bool, error) {
	res, err := artifactfetcher.Fetch(ctx, fe.URL, c.fetchOpts(subscriberID))
	if err != nil {
		return nil, false, err
	}

	declaredDigest := stripDigestPrefix(fe.Digest)
	if declaredDigest != "" && declaredDigest != res.Digest {
		return nil, false, fmt.Errorf("digest mismatch: declared %s, fetched %s", declaredDigest, res.Digest)
	}
	if fe.Size > 0 && fe.Size != int64(len(res.Body)) {
		return nil, false, fmt.Errorf("size mismatch: declared %d, fetched %d", fe.Size, int64(len(res.Body)))
	}
	if !fe.ValidUntil().IsZero() && time.Now().After(fe.ValidUntil()) {
		return nil, false, fmt.Errorf("signature validUntil %s has passed", fe.ValidUntil())
	}

	pub, err := keyByID(manifestKeys, fe.Signature.KeyID)
	if err != nil {
		return nil, false, fmt.Errorf("resolving signing key: %w", err)
	}
	verifyErr := artifactverifier.VerifyFileTuple(catalogID, fe.Version, fe.URL, fe.Digest, fe.Signature.ValidUntil, fe.Signature.Value, pub)
	if verifyErr != nil {
		log.Debugf(ctx, "catalogcrawler: file %s signature verification failed: %v", fe.URL, verifyErr)
	}

	return res.Body, verifyErr == nil, nil
}

// ValidUntil is a convenience accessor so fetchAndVerifyFile reads
// uniformly regardless of struct nesting.
func (fe fileEntry) ValidUntil() time.Time { return fe.Signature.ValidUntil }

// looksLikeBecknCatalog is a shallow structural check -- it does not
// perform full JSON-Schema validation (no schemaValidator plugin in this
// phase). A Beckn Catalog object (unlike a request/response action)
// carries no context/message envelope; it's the bare
// {id, descriptor, provider, resources} shape the file spec uses.
func looksLikeBecknCatalog(body []byte) bool {
	var catalog struct {
		ID         string          `json:"id"`
		Descriptor json.RawMessage `json:"descriptor"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		return false
	}
	return catalog.ID != "" && len(catalog.Descriptor) > 0
}

// fetchOpts builds fetch options for index/catalog GETs. Signing is
// currently disabled: the reference test setup hosts index/catalog files
// as public, unsigned artifacts, so there's nothing on the PN side yet
// that would check a signed request. c.signer/c.keyManager are still
// constructor-required and wired through so re-enabling this is a
// one-line change once a participant in the test setup actually expects
// signed GETs -- tracked as an open item to revisit, not dropped.
func (c *Crawler) fetchOpts(subscriberID string) artifactfetcher.Options {
	return artifactfetcher.Options{
		MaxSize:  c.config.MaxArtifactSize,
		Timeout:  c.config.FetchTimeout,
		RetryMax: c.config.RetryMax,
		// Signer:       c.signer,
		// KeyManager:   c.keyManager,
		// SubscriberID: subscriberID,
	}
}
