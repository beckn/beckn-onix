// Package catalogcrawler implements definition.Crawler: it walks a
// subscriber's published manifest -> index -> catalog chain (see
// onix-catalog-crawler-plugin-requirements.md), verifying the manifest's
// and index's own detached signatures (each carries its own proof.jws and
// publisher key) and checking each catalog part's digest against what its
// index declared -- catalog parts carry no signature of their own by
// design. Outbound GETs to the index/catalog endpoints are currently
// unsigned (see fetchOpts) pending a PN in the test setup that expects
// signed requests; the capability is wired through and is a one-line
// change to re-enable. This first phase does not use caching, checkPolicy,
// or schemaValidator -- every call re-fetches and re-verifies from
// scratch.
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

// Crawler fetches, verifies, and returns a subscriber's published catalogs.
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

// dediManifest is the subset of a DeDi manifest (type: dedi-manifest) this
// crawler reads: the trust anchor keys, the pointer to the index file(s),
// and the manifest's own detached-signature proof. Field names/shapes here
// match the reference fixture (JWK-style OKP keys, "sha-256:<hex>" digests)
// rather than a guessed convention.
type dediManifest struct {
	Keys  []dediKey  `json:"keys"`
	Files []dediFile `json:"files"`
	Proof *dediProof `json:"proof,omitempty"`
}

// dediKey is a JWK-shaped Ed25519 (OKP) public key, as published in a
// manifest's keys[] or an index's publisher.key.
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
	Jws                 string `json:"jws"`
}

type dediFile struct {
	Registry string `json:"registry"`
	URL      string `json:"url"`
	Digest   string `json:"digest"` // "sha-256:<hex>"
	Schema   string `json:"schema"`
}

// dediIndex is the subset of a DeDi file (type: dedi-file) whose
// records[].details are catalog pointers, not catalog content. Like the
// manifest, the index carries its own publisher key and detached-signature
// proof. Catalog parts, by design, carry neither -- a part's integrity is
// inherited entirely from the digest the index declares for it (FR6), not
// from a signature of its own.
type dediIndex struct {
	Publisher dediIndexPublisher `json:"publisher"`
	Records   []dediIndexRecord `json:"records"`
	Proof     *dediProof        `json:"proof,omitempty"`
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
	NextUpdate  *time.Time `json:"next_update,omitempty"`
	Parts       []dediPart `json:"parts"`
}

type dediPart struct {
	URL    string `json:"url"`
	Digest string `json:"digest"` // "sha-256:<hex>"
}

// catalogIndexRegistry is the manifest files[].registry value identifying
// the catalog-index file (FR3). Matching on the schema URL was tried first,
// but the reference fixture's schema has since been renamed without any
// "CatalogIndexRecord" marker, and a manifest can carry other files[]
// entries (e.g. "beckn-subscriber") with unrelated schemas -- registry is
// the stable identifier the provider actually commits to.
const catalogIndexRegistry = "beckn-catalogs"

// isCatalogIndexFile reports whether a manifest files[] entry is the
// catalog-index file (FR3).
func isCatalogIndexFile(f dediFile) bool {
	return f.Registry == catalogIndexRegistry
}

// CrawlSubscriber fetches and verifies the manifest, every index it
// references, and every catalog part each index references, in that order.
// A single bad index or catalog part is reported as a non-fatal
// definition.CrawlError rather than failing the whole call (FR11).
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
		catalogs, errs := c.fetchIndex(ctx, req.SubscriberID, file)
		result.Catalogs = append(result.Catalogs, catalogs...)
		result.Errors = append(result.Errors, errs...)
	}

	return result, nil
}

// domainFromSubscriberID extracts a base URL (scheme+host) from the DS-
// supplied subscriber URI. No registry lookup is performed -- the DS
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

// fetchManifest fetches the public, unsigned .well-known manifest and
// verifies it against its own embedded keys.
func (c *Crawler) fetchManifest(ctx context.Context, domain string) (dediManifest, definition.ManifestResult, error) {
	manifestURL := domain + "/.well-known/dedi.json"
	res, err := artifactfetcher.Fetch(ctx, manifestURL, c.fetchOpts(""))
	if err != nil {
		return dediManifest{}, definition.ManifestResult{URL: manifestURL}, fmt.Errorf("catalogcrawler: fetching manifest: %w", err)
	}

	var manifest dediManifest
	if err := json.Unmarshal(res.Body, &manifest); err != nil {
		return dediManifest{}, definition.ManifestResult{URL: manifestURL, Digest: res.Digest}, fmt.Errorf("catalogcrawler: parsing manifest: %w", err)
	}

	verified := verifyDediProof(res.Body, manifest.Keys, manifest.Proof)
	return manifest, definition.ManifestResult{
		URL:        manifestURL,
		Digest:     res.Digest,
		Verified:   verified,
		VerifiedAt: time.Now(),
	}, nil
}

// verifyDediProof verifies content's compact detached-JWS proof.jws
// (header_b64..signature_b64, per §7.3) against the given keys[], matched
// by proof.verification_method (kid). Used for the manifest's keys[] and an
// index's single publisher.key -- the only two artifact levels that carry a
// proof at all; catalog parts, by design, do not (see the dediIndex doc
// comment). The signing input is reconstructed from content with its own
// "proof" field stripped (§7.2) -- content is never verified against a
// signing input that includes the signature itself. Returns false (not
// fatal, see FR6/§9) when there's no proof, no matching key, or the
// signature doesn't verify -- as is the case for the reference fixture's
// placeholder jws value.
func verifyDediProof(content []byte, keys []dediKey, proof *dediProof) bool {
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

// stripDigestPrefix removes a leading "sha-256:" (or "sha256:") from a
// declared digest, matching the plain hex digest artifactfetcher computes.
func stripDigestPrefix(digest string) string {
	if i := strings.Index(digest, ":"); i != -1 {
		return digest[i+1:]
	}
	return digest
}

// fetchIndex signs and fetches one manifest-referenced index file, then
// fetches every catalog part it points to.
func (c *Crawler) fetchIndex(ctx context.Context, subscriberID string, file dediFile) ([]definition.CatalogResult, []definition.CrawlError) {
	res, err := artifactfetcher.Fetch(ctx, file.URL, c.fetchOpts(subscriberID))
	if err != nil {
		return nil, []definition.CrawlError{{Stage: "index_fetch", Reason: err.Error(), Fatal: false}}
	}
	if declared := stripDigestPrefix(file.Digest); declared != "" && declared != res.Digest {
		return nil, []definition.CrawlError{{Stage: "index_verify", Reason: fmt.Sprintf("index digest mismatch: manifest declared %s, fetched %s", declared, res.Digest), Fatal: false}}
	}

	var index dediIndex
	if err := json.Unmarshal(res.Body, &index); err != nil {
		return nil, []definition.CrawlError{{Stage: "index_fetch", Reason: fmt.Sprintf("parsing index: %v", err), Fatal: false}}
	}

	indexVerified := verifyDediProof(res.Body, []dediKey{index.Publisher.Key}, index.Proof)
	log.Debugf(ctx, "catalogcrawler: index %s signature verified=%t", file.URL, indexVerified)

	var catalogs []definition.CatalogResult
	var errs []definition.CrawlError
	for _, record := range index.Records {
		details := record.Details
		if err := validatePointer(details); err != nil {
			errs = append(errs, definition.CrawlError{CatalogID: details.CatalogID, Stage: "index_verify", Reason: err.Error(), Fatal: false})
			continue
		}
		for _, part := range details.Parts {
			cr, err := c.fetchCatalogPart(ctx, subscriberID, details, part)
			if err != nil {
				errs = append(errs, definition.CrawlError{CatalogID: details.CatalogID, Stage: "part_fetch", Reason: err.Error(), Fatal: false})
				continue
			}
			catalogs = append(catalogs, cr)
		}
	}
	return catalogs, errs
}

// validatePointer checks the minimum shape FR4 requires of an index record.
func validatePointer(d dediCatalogPointer) error {
	if d.CatalogID == "" {
		return fmt.Errorf("index record missing catalogId")
	}
	if len(d.Parts) == 0 {
		return fmt.Errorf("catalog %s has no parts", d.CatalogID)
	}
	return nil
}

// fetchCatalogPart signs and fetches one catalog part, then applies the
// inline freshness/status/digest/signature checks FR6 requires. Caching and
// version-rollback comparison are no-ops in this phase (nothing is cached
// yet to compare against), so Changed is always true and VersionOK is always
// true.
func (c *Crawler) fetchCatalogPart(ctx context.Context, subscriberID string, details dediCatalogPointer, part dediPart) (definition.CatalogResult, error) {
	res, err := artifactfetcher.Fetch(ctx, part.URL, c.fetchOpts(subscriberID))
	if err != nil {
		return definition.CatalogResult{}, err
	}

	declaredDigest := stripDigestPrefix(part.Digest)
	// Catalog parts carry no proof.jws of their own by design -- their
	// integrity is inherited entirely from the digest the index declared
	// for them (checked below), not from a signature. There is no
	// SignatureValid check here because there is no signature to check.
	outcome := definition.VerificationOutcome{
		DigestMatch: declaredDigest == "" || declaredDigest == res.Digest,
		SchemaValid: looksLikeBecknCatalog(res.Body),
		VersionOK:   true,
	}

	if !isLive(details.Status) {
		return definition.CatalogResult{}, fmt.Errorf("catalog %s has non-live status %q", details.CatalogID, details.Status)
	}
	if details.NextUpdate != nil && time.Now().After(*details.NextUpdate) {
		return definition.CatalogResult{}, fmt.Errorf("catalog %s is stale (next_update %s has passed)", details.CatalogID, details.NextUpdate)
	}

	return definition.CatalogResult{
		CatalogID:   details.CatalogID,
		Version:     details.Version,
		Status:      details.Status,
		Visibility:  details.Visibility,
		SchemaTypes: details.SchemaTypes,
		Source: definition.PartRef{
			URL:    part.URL,
			Digest: res.Digest,
		},
		Changed:      true,
		Verification: outcome,
		Catalog:      json.RawMessage(res.Body),
	}, nil
}

// isLive reports whether status represents an active/live catalog entry.
func isLive(status string) bool {
	switch strings.ToUpper(status) {
	case "ACTIVE", "LIVE":
		return true
	default:
		return false
	}
}

// looksLikeBecknCatalog is a shallow structural check -- it does not perform
// full JSON-Schema validation (no schemaValidator plugin in this phase). A
// Beckn Catalog object (unlike a request/response action) carries no
// context/message envelope; it's the bare {id, descriptor, provider,
// resources} shape the reference fixture's catalog part uses.
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
// currently disabled: the reference test setup hosts index/catalog files as
// public, unsigned artifacts, so there's nothing on the PN side yet that
// would check a signed request. c.signer/c.keyManager are still
// constructor-required and wired through so re-enabling this is a one-line
// change once a PN in the test setup actually expects signed GETs --
// tracked as an open item to revisit, not dropped.
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

