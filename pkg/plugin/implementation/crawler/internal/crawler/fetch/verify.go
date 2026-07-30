package fetch

// verify.go — the per-file signature gate. Before a catalog file's bytes are
// fetched or used, its index entry's signed tuple
// {catalogId, version, url, digest, validUntil} is verified against the
// publisher's public key AS PUBLISHED IN THE NETWORK REGISTRY. That is the same
// key distribution channel signvalidator uses for transport signatures: the
// registry is the trust anchor, and there is no per-deployment key list for an
// operator to configure or rotate by hand.
//
// The gate fails CLOSED. No key source, no signature, no expiry, an unknown
// keyId, a revoked subscription, or undecodable key material all reject the
// file.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
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
// participant that published the index and the keyId the index entry names. It
// is injected rather than derived, so this layer never trusts a key carried by
// the same untrusted file it is checking.
//
// An error means "this file cannot be verified", and the error's class decides
// park vs retry:
//   - a catalog.PermanentError of class catalog.FaultSignature is a definitive
//     verdict (no such key, revoked key, unusable key material) and parks;
//   - any other error is transient (the registry could not be reached) and is
//     retried.
//
// An unknown keyId must return an error, never a zero key.
type KeySource func(ctx context.Context, participantID, keyID string) (ed25519.PublicKey, error)

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
// ignoring the participant.
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
// The subscriber id it looks up is the index's participantId, which is
// PUBLISHER-ASSERTED. The index document itself is not signature-verified (only
// the per-file tuples inside it are), so a publisher — or anyone who can serve
// that URL — can write any participantId they like.
//
// That is safe, and the reasoning must be written down because it is not
// obvious. A forged participantId resolves to THAT participant's real
// registered key, and the file's signature then fails to verify under it. The
// forgery converts into a closed failure rather than into trust. participantId
// is therefore a HINT for key resolution only. Anything that later starts
// trusting participantId for authorization, scoping, or attribution would break
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

// keyCacheKey identifies one resolved key. The participant is part of the key
// because the same keyId string may exist under more than one subscriber.
type keyCacheKey struct {
	participantID string
	keyID         string
}

// keyCacheEntry is one resolved key and the instant it stops being reusable.
type keyCacheEntry struct {
	key       ed25519.PublicKey
	expiresAt time.Time
}

// keyCache memoises successful registry resolutions so an index of thousands of
// files costs one registry call per (participant, keyId), not one per file.
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

func (c *keyCache) get(ctx context.Context, participantID, keyID string) (ed25519.PublicKey, error) {
	if c.reg == nil {
		// Fail closed. A crawler wired without a registry has no way to learn any
		// publisher key, so it can verify nothing.
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: no registry configured, cannot resolve publisher key %q", keyID)
	}
	if participantID == "" {
		// Without a subscriber id there is nothing to look the key up under. An
		// index that omits participantId is unusable, not permissive.
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: index declares no participantId, cannot resolve publisher key %q", keyID)
	}
	ck := keyCacheKey{participantID: participantID, keyID: keyID}
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
		Subscriber: model.Subscriber{SubscriberID: ck.participantID},
		KeyID:      ck.keyID,
	})
	if err != nil {
		// TRANSIENT, and this distinction is the whole park-vs-retry decision. A
		// registry that is down, rate limiting, or timing out says nothing about
		// this file. Parking the catalog on it would strand a healthy publisher
		// until a human noticed. Returned unclassified, so ClassifyFault reports
		// FaultTransient and the runner retries with backoff.
		return nil, fmt.Errorf("crawler: registry lookup for subscriber %q key %q: %w", ck.participantID, ck.keyID, err)
	}
	if len(subs) == 0 {
		// PERMANENT: the registry answered, and the answer is that this key does
		// not exist for this participant. Retrying cannot change that.
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: registry has no key %q for subscriber %q", ck.keyID, ck.participantID)
	}
	sub := subs[0]
	// Revoked, expired and invalid-SSL subscriptions must not verify anything.
	// model.IsKeyStatusUsable is the shared rule (pkg/model/model.go); the
	// transport signature path applies exactly the same one.
	if !model.IsKeyStatusUsable(sub.Status) {
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: registry key %q for subscriber %q has unusable status %q", ck.keyID, ck.participantID, sub.Status)
	}
	if sub.SigningPublicKey == "" {
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: registry key %q for subscriber %q has no signing public key", ck.keyID, ck.participantID)
	}
	raw, err := base64.StdEncoding.DecodeString(sub.SigningPublicKey)
	if err != nil {
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: registry key %q for subscriber %q is not valid base64: %v", ck.keyID, ck.participantID, err)
	}
	if len(raw) != ed25519.PublicKeySize {
		// Guard before ed25519.Verify, which panics on a mis-sized key.
		return nil, catalog.PermanentFaultf(faultSignature,
			"crawler: registry key %q for subscriber %q decodes to %d bytes, want %d",
			ck.keyID, ck.participantID, len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// tupleVerifier checks one file entry's signed tuple. now is injected so the
// expiry check is testable without sleeping.
type tupleVerifier struct {
	keys KeySource
	now  func() time.Time
}

// verify reports whether f's signed tuple is present, unexpired, keyed to a key
// the registry vouches for, and valid over
// {catalogId, version, url, digest, validUntil}.
//
// Every verdict about the file is PERMANENT (catalog.FaultSignature): an
// unsigned, expired or forged entry stays that way however often it is
// re-fetched, so the runner parks and alerts rather than retrying on a
// 5-minute loop. The single exception is a KeySource that could not reach the
// registry, which is passed through unclassified so it retries.
func (v tupleVerifier) verify(ctx context.Context, f catalog.FileEntry) error {
	if v.keys == nil {
		// Fail closed: with no key source there is nothing to verify against, so
		// the signature gate would otherwise silently degrade to "digest only".
		return catalog.PermanentFaultf(faultSignature,
			"crawler: no key source configured, refusing unverifiable file %s", f.URL)
	}
	sig := f.Signature
	if sig.Value == "" || sig.KeyID == "" {
		return catalog.PermanentFaultf(faultSignature,
			"crawler: %s has no signature (signature verification required)", f.URL)
	}
	if sig.CatalogID == "" {
		// The tuple covers catalogId; without the binding the entry cannot be
		// verified at all (see catalog.StampCatalogIDs).
		return catalog.PermanentFaultf(faultSignature,
			"crawler: %s signature is not bound to a catalog", f.URL)
	}
	validUntil, err := sig.ValidUntilTime()
	if err != nil {
		return catalog.PermanentFaultf(faultSignature, "crawler: %s: %v", f.URL, err)
	}
	if v.now().After(validUntil) {
		return catalog.PermanentFaultf(faultSignature,
			"crawler: %s signature expired at %s", f.URL, sig.ValidUntil)
	}
	pub, err := v.keys(ctx, sig.ParticipantID, sig.KeyID)
	if err != nil {
		if catalog.IsPermanent(err) {
			// A definitive verdict from the key source: no such key, revoked key,
			// unusable key material. Park.
			return catalog.PermanentFaultf(faultSignature,
				"crawler: %s resolving signing key %q: %v", f.URL, sig.KeyID, err)
		}
		// The key source could not answer (registry unreachable). Stay transient so
		// the runner retries instead of parking a healthy catalog.
		return fmt.Errorf("crawler: %s resolving signing key %q: %w", f.URL, sig.KeyID, err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return catalog.PermanentFaultf(faultSignature,
			"crawler: %s key %q is not a %d-byte Ed25519 key", f.URL, sig.KeyID, ed25519.PublicKeySize)
	}
	// The digest is passed exactly as published (prefix included) — the tuple is
	// signed over the declared string, not over the parsed hash.
	if err := artifactverifier.VerifyFileTuple(sig.CatalogID, int(f.Version), f.URL, f.Digest, validUntil, sig.Value, pub); err != nil {
		return catalog.PermanentFaultf(faultSignature,
			"crawler: %s signature verification failed: %v", f.URL, err)
	}
	return nil
}
