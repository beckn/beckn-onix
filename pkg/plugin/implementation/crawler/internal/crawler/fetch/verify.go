package fetch

// verify.go — the two self-signature gates the v2 file spec requires:
//
//   - verifyEntrySignature checks a catalog index ENTRY's own signature
//     (over catalogId, catalogType, status, networkIds, schemaTypes, and
//     every baseline/changes[] reference together) before any of its file
//     references are trusted.
//   - verifyFileSignature checks a fetched catalog FILE's (baseline or
//     change file) own embedded signature over its own content, and unwraps
//     the baseline's {catalog, signature} envelope down to the bare catalog
//     document once verified.
//
// Both verify against the publisher's public key AS PUBLISHED IN THE NETWORK
// REGISTRY — the same key distribution channel signvalidator uses for
// transport signatures. The registry is a cache of the DeDi manifest that is
// the real trust anchor (file spec: "every registry copy is a cache of it"),
// so there is no per-deployment key list for an operator to configure.
//
// Both gates fail CLOSED. No key source, no signature, an unknown keyId, a
// revoked subscription, or undecodable key material all reject the entry or
// file.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// faultSignature is the fault class every signature verdict in this package is
// raised with. It is a package-local shorthand for catalog.FaultSignature,
// which exists so the many raise sites below (and the fetch tests) read at one
// glance as "this is a signature verdict".
//
// It is deliberately NOT catalog.FaultDigestMismatch, which is what these
// failures used to borrow. A digest mismatch tells an operator "the publisher's
// bytes disagree with the publisher's own declaration, go talk to the
// publisher". A signature failure may instead be a key problem on the registry
// side, and routing it to the publisher sends the operator to the wrong party.
const faultSignature = catalog.FaultSignature

// KeySource resolves the public Ed25519 key a publisher signed with, given the
// node (domain identity) that published the index and the keyId the signing
// entity named. It is injected rather than derived, so this layer never trusts
// a key carried by the same untrusted content it is checking.
//
// An error means "this cannot be verified", and the error's class decides park
// vs retry:
//   - a catalog.PermanentError of class catalog.FaultSignature is a definitive
//     verdict (no such key, revoked key, unusable key material) and parks;
//   - any other error is transient (the registry could not be reached) and is
//     retried.
//
// An unknown keyId must return an error, never a zero key.
type KeySource func(ctx context.Context, nodeID, keyID string) (ed25519.PublicKey, error)

// KeyRegistry is the crawler's port onto the network registry. It is declared
// here, structurally identical to definition.RegistryLookup, so the fetch layer
// stays free of the plugin framework and can be driven by a fake in tests.
type KeyRegistry interface {
	Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error)
}

// DefaultKeyCacheTTL is how long a resolved publisher key is reused before the
// registry is asked again. Without caching the crawler would make one registry
// call per catalog file, and a large index is thousands of files.
const DefaultKeyCacheTTL = 5 * time.Minute

// StaticKeys adapts an already-resolved keyId -> public key map to a KeySource,
// ignoring the node.
//
// TEST HELPER ONLY. The production path is RegistryKeys: keys come from the
// registry, never from deployment config. Nothing in the composition root
// builds a StaticKeys source.
func StaticKeys(keys map[string]ed25519.PublicKey) KeySource {
	return func(_ context.Context, _, keyID string) (ed25519.PublicKey, error) {
		pub, ok := keys[keyID]
		if !ok {
			return nil, catalog.PermanentFaultf(faultSignature,
				"crawler: keyId %q is not a known publisher key", keyID)
		}
		return pub, nil
	}
}

// RegistryKeys is the production KeySource: it resolves a signing key through
// the network registry, exactly as the transport signature path does.
//
// The nodeId it looks up under is PUBLISHER-ASSERTED (it comes from the index
// document itself, which is not signature-verified as a whole -- only its
// per-entry and per-file self-signatures are). That is safe, and the reasoning
// must be written down because it is not obvious: a forged nodeId resolves to
// THAT node's real registered key, and the signature then fails to verify
// under it. The forgery converts into a closed failure rather than into trust.
// nodeId is therefore a HINT for key resolution only. Anything that later
// starts trusting it for authorization, scoping, or attribution would break
// this property and needs its own verified source.
//
// ttl <= 0 uses DefaultKeyCacheTTL.
func RegistryKeys(reg KeyRegistry, ttl time.Duration) KeySource {
	if ttl <= 0 {
		ttl = DefaultKeyCacheTTL
	}
	c := &keyCache{reg: reg, ttl: ttl, now: time.Now, entries: map[keyCacheKey]keyCacheEntry{}}
	return c.get
}

// keyCacheKey identifies one resolved key. The node is part of the key because
// the same keyId string may exist under more than one publisher.
type keyCacheKey struct {
	nodeID string
	keyID  string
}

// keyCacheEntry is one resolved key and the instant it stops being reusable.
type keyCacheEntry struct {
	key       ed25519.PublicKey
	expiresAt time.Time
}

// keyCache memoises successful registry resolutions so an index of thousands of
// files costs one registry call per (node, keyId), not one per file.
//
// Only successes are cached. A failure is never cached: a transient registry
// outage must be retried on the next pass rather than pinned for the whole TTL,
// and a definitive "no such key" is already terminal for that catalog (the
// runner parks it), so caching it would buy nothing while delaying recovery
// after an operator registers the key.
type keyCache struct {
	reg KeyRegistry
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[keyCacheKey]keyCacheEntry
}

func (c *keyCache) get(ctx context.Context, nodeID, keyID string) (ed25519.PublicKey, error) {
	if c.reg == nil {
		// Fail closed. A crawler wired without a registry has no way to learn any
		// publisher key, so it can verify nothing.
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: no registry configured, cannot resolve publisher key %q", keyID)
	}
	if nodeID == "" {
		// Without a node id there is nothing to look the key up under. An index
		// that omits nodeId is unusable, not permissive.
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: index declares no nodeId, cannot resolve publisher key %q", keyID)
	}
	ck := keyCacheKey{nodeID: nodeID, keyID: keyID}
	if key, ok := c.lookupCached(ck); ok {
		return key, nil
	}
	key, err := c.resolve(ctx, ck)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries[ck] = keyCacheEntry{key: key, expiresAt: c.now().Add(c.ttl)}
	c.mu.Unlock()
	return key, nil
}

func (c *keyCache) lookupCached(ck keyCacheKey) (ed25519.PublicKey, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[ck]
	if !ok || !c.now().Before(e.expiresAt) {
		return nil, false
	}
	return e.key, true
}

// resolve asks the registry for {subscriberID, keyID} and turns the answer into
// a usable Ed25519 key, or into a correctly classified failure.
func (c *keyCache) resolve(ctx context.Context, ck keyCacheKey) (ed25519.PublicKey, error) {
	subs, err := c.reg.Lookup(ctx, &model.Subscription{
		Subscriber: model.Subscriber{SubscriberID: ck.nodeID},
		KeyID:      ck.keyID,
	})
	if err != nil {
		// TRANSIENT, and this distinction is the whole park-vs-retry decision. A
		// registry that is down, rate limiting, or timing out says nothing about
		// this file. Parking the catalog on it would strand a healthy publisher
		// until a human noticed. Returned unclassified, so ClassifyFault reports
		// FaultTransient and the runner retries with backoff.
		return nil, fmt.Errorf("crawler: registry lookup for node %q key %q: %w", ck.nodeID, ck.keyID, err)
	}
	if len(subs) == 0 {
		// PERMANENT: the registry answered, and the answer is that this key does
		// not exist for this node. Retrying cannot change that.
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: registry has no key %q for node %q", ck.keyID, ck.nodeID)
	}
	sub := subs[0]
	// Revoked, expired and invalid-SSL subscriptions must not verify anything.
	// model.IsKeyStatusUsable is the shared rule (pkg/model/model.go); the
	// transport signature path applies exactly the same one.
	if !model.IsKeyStatusUsable(sub.Status) {
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: registry key %q for node %q has unusable status %q", ck.keyID, ck.nodeID, sub.Status)
	}
	if sub.SigningPublicKey == "" {
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: registry key %q for node %q has no signing public key", ck.keyID, ck.nodeID)
	}
	raw, err := base64.StdEncoding.DecodeString(sub.SigningPublicKey)
	if err != nil {
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: registry key %q for node %q is not valid base64: %v", ck.keyID, ck.nodeID, err)
	}
	if len(raw) != ed25519.PublicKeySize {
		// Guard before ed25519.Verify, which panics on a mis-sized key.
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: registry key %q for node %q decodes to %d bytes, want %d",
			ck.keyID, ck.nodeID, len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// resolveSigningKey is the shared "look up sig.keyId under nodeID, park on any
// definitive verdict, retry on a registry outage" step both self-signature
// gates below use.
func resolveSigningKey(ctx context.Context, keys KeySource, nodeID, keyID, what string) (ed25519.PublicKey, error) {
	if keys == nil {
		// Fail closed: with no key source there is nothing to verify against, so
		// the signature gate would otherwise silently degrade to "digest only".
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: no key source configured, refusing unverifiable %s", what)
	}
	pub, err := keys(ctx, nodeID, keyID)
	if err != nil {
		if catalog.IsPermanent(err) {
			return nil, catalog.PermanentFaultf(faultSignature,
				"crawler: %s resolving signing key %q: %v", what, keyID, err)
		}
		// The key source could not answer (registry unreachable). Stay transient so
		// the runner retries instead of parking a healthy catalog.
		return nil, fmt.Errorf("crawler: %s resolving signing key %q: %w", what, keyID, err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: %s key %q is not a %d-byte Ed25519 key", what, keyID, ed25519.PublicKeySize)
	}
	return pub, nil
}

// verifyEntrySignature verifies a catalog index entry's own self-signature
// (file spec: "each catalog entry signs itself" over catalogId, catalogType,
// status, networkIds, schemaTypes, and every baseline/changes[] reference,
// together, as one unit). entryRaw is the entry's own raw JSON exactly as
// received on the wire — verification MUST run against those bytes, never a
// Go-struct re-marshal, since a struct round-trip can drop/add fields (e.g.
// omitempty) and silently break canonicalization.
func verifyEntrySignature(ctx context.Context, keys KeySource, nodeID string, entryRaw json.RawMessage, sig catalog.EntrySignature) error {
	if sig.Value == "" || sig.KeyID == "" {
		return catalog.PermanentFaultf(faultSignature, "crawler: catalog entry has no signature")
	}
	pub, err := resolveSigningKey(ctx, keys, nodeID, sig.KeyID, "catalog entry")
	if err != nil {
		return err
	}
	if err := artifactverifier.VerifyJSON(entryRaw, "signature", sig.Value, pub); err != nil {
		return catalog.PermanentFaultf(faultSignature, "crawler: catalog entry signature verification failed: %v", err)
	}
	return nil
}

// signedDoc is the shape both self-signed catalog files share on the wire: a
// baseline (CatalogFile) wraps its content as exactly {catalog, signature}; a
// change file (CatalogChangeFile) is flat -- catalogId/fromVersion/toVersion
// alongside resources/offers/an OPTIONAL catalog attribute patch/signature.
// FromVersion is what tells them apart: it is required on a change file and
// absent from a baseline, so it is a reliable discriminator. Catalog being
// non-empty is NOT: a change file legitimately carries a non-empty "catalog"
// block too (its optional attribute patch), so unwrapping on that alone would
// wrongly strip a change file down to just its patch and drop resources/offers.
type signedDoc struct {
	CatalogID   string                 `json:"catalogId"`
	Version     *int64                 `json:"version"` // baseline only
	FromVersion *int                   `json:"fromVersion"`
	ToVersion   *int64                 `json:"toVersion"` // change file only
	Catalog     json.RawMessage        `json:"catalog"`
	Signature   catalog.EntrySignature `json:"signature"`
}

// verifyFileSignature verifies a fetched catalog file's (baseline or change
// file) own embedded self-signature over its own content, cross-checks its
// own internal catalogId/version against what the index entry declared
// (RFC NFH-014 CON-TBD-12: a mismatch here is treated exactly like a digest
// mismatch -- discard, don't index, log; neither side is authoritative), then
// returns the bytes downstream code should use: for a baseline (which wraps
// its content as {catalog, signature}) that is the unwrapped .catalog object;
// for a change file (flat, fromVersion present) that is raw unchanged.
//
// wantCatalogID/wantVersion are the index entry's own declared catalogId and
// this FileEntry's declared version (baseline.version, or the change's own
// toVersion) -- what the file's internal fields are checked against.
func verifyFileSignature(ctx context.Context, keys KeySource, nodeID, url string, raw []byte, wantCatalogID string, wantVersion int64) ([]byte, error) {
	var doc signedDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, catalog.PermanentFaultf(catalog.FaultContentInvalid, "crawler: %s: not a JSON object: %v", url, err)
	}
	if doc.Signature.Value == "" || doc.Signature.KeyID == "" {
		return nil, catalog.PermanentFaultf(faultSignature, "crawler: %s has no signature (signature verification required)", url)
	}
	pub, err := resolveSigningKey(ctx, keys, nodeID, doc.Signature.KeyID, url)
	if err != nil {
		return nil, err
	}
	if err := artifactverifier.VerifyJSON(raw, "signature", doc.Signature.Value, pub); err != nil {
		return nil, catalog.PermanentFaultf(faultSignature, "crawler: %s signature verification failed: %v", url, err)
	}
	// CON-TBD-12: the file's own internal catalogId/version must agree with
	// what the index entry declared for it. Checked AFTER signature
	// verification (so this is the file's genuine, authored identity, not
	// something an attacker without the signing key could forge) but treated
	// exactly like a digest mismatch, not a signature failure -- the content is
	// authentic, just not the content this reference claims it is.
	gotVersion := doc.Version
	if doc.FromVersion != nil {
		gotVersion = doc.ToVersion
	}
	if doc.CatalogID != wantCatalogID || gotVersion == nil || *gotVersion != wantVersion {
		return nil, catalog.PermanentFaultf(catalog.FaultDigestMismatch,
			"crawler: %s declares catalogId=%q version=%v, index entry expected catalogId=%q version=%d",
			url, doc.CatalogID, gotVersion, wantCatalogID, wantVersion)
	}
	if doc.FromVersion == nil {
		if len(doc.Catalog) == 0 {
			return nil, catalog.PermanentFaultf(catalog.FaultContentInvalid, "crawler: %s: baseline has no catalog content", url)
		}
		return doc.Catalog, nil // baseline envelope: unwrap to the bare catalog document
	}
	return raw, nil // change file (fromVersion present): signature is a sibling field, nothing to unwrap
}
