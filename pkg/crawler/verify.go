package crawler

// verify.go — resolving a signing key through a network registry (with
// caching), and verifying a self-signature over an arbitrary JSON document
// against that key.
//
// This is deliberately content-agnostic: it verifies "this document, minus
// its own named signature field, was signed by the key registered for
// (nodeID, keyID)". It has no notion of a catalog index, an entry, or a
// baseline/change envelope — a caller that fetches self-signed catalog files,
// DeDi manifests, or any other self-signed artifact can use the same gate.
// Unwrapping a caller-specific signed envelope (e.g. a catalog baseline's
// {catalog, signature} wrapper) and cross-checking fields the caller's own
// format declares is the caller's job, layered on top of VerifySignature.
//
// The gate fails CLOSED. No key source, no signature, an unknown keyId, a
// revoked subscription, or undecodable key material all reject the document.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// KeySource resolves the public Ed25519 key a node signed with, given the
// node (domain identity) that published the content and the keyId the
// signing entity named. It is injected rather than derived, so this layer
// never trusts a key carried by the same untrusted content it is checking.
//
// An error means "this cannot be verified", and the error's class decides a
// caller's retry-vs-give-up decision:
//   - a PermanentError of class FaultSignature is a definitive verdict (no
//     such key, revoked key, unusable key material) and should not be retried;
//   - any other error is transient (the registry could not be reached) and
//     may be retried.
//
// An unknown keyId must return an error, never a zero key.
type KeySource func(ctx context.Context, nodeID, keyID string) (ed25519.PublicKey, error)

// KeyRegistry is the port onto a network registry a KeySource resolves keys
// through. Declared here, structurally identical to definition.RegistryLookup,
// so this package stays free of the plugin framework and can be driven by a
// fake in tests.
type KeyRegistry interface {
	Lookup(ctx context.Context, req *model.Subscription) ([]model.Subscription, error)
}

// DefaultKeyCacheTTL is how long a resolved key is reused before the registry
// is asked again. Without caching, verifying many documents from the same
// publisher would make one registry call per document.
const DefaultKeyCacheTTL = 5 * time.Minute

// StaticKeys adapts an already-resolved keyId -> public key map to a
// KeySource, ignoring the node.
//
// TEST HELPER ONLY. The production path is RegistryKeys: keys come from the
// registry, never from deployment config.
func StaticKeys(keys map[string]ed25519.PublicKey) KeySource {
	return func(_ context.Context, _, keyID string) (ed25519.PublicKey, error) {
		pub, ok := keys[keyID]
		if !ok {
			return nil, PermanentFaultf(FaultSignature, "crawler: keyId %q is not a known key", keyID)
		}
		return pub, nil
	}
}

// RegistryKeys is the production KeySource: it resolves a signing key through
// a network registry.
//
// The nodeId it looks up under is caller-asserted -- it typically comes from
// content that is not itself signature-verified as a whole, only in part.
// That is safe, and the reasoning must be written down because it is not
// obvious: a forged nodeId resolves to THAT node's real registered key, and
// the signature then fails to verify under it. The forgery converts into a
// closed failure rather than into trust. nodeId is therefore a HINT for key
// resolution only. A caller that starts trusting it for authorization,
// scoping, or attribution breaks this property and needs its own verified
// source.
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

// keyCache memoises successful registry resolutions so verifying many
// documents from the same (node, keyId) costs one registry call, not one per
// document.
//
// Only successes are cached. A failure is never cached: a transient registry
// outage must be retried rather than pinned for the whole TTL, and a
// definitive "no such key" is already terminal for that caller's retry
// policy, so caching it would buy nothing while delaying recovery after an
// operator registers the key.
type keyCache struct {
	reg KeyRegistry
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[keyCacheKey]keyCacheEntry
}

func (c *keyCache) get(ctx context.Context, nodeID, keyID string) (ed25519.PublicKey, error) {
	if c.reg == nil {
		// Fail closed. A caller wired without a registry has no way to learn any
		// key, so it can verify nothing.
		return nil, PermanentFaultf(FaultSignature, "crawler: no registry configured, cannot resolve key %q", keyID)
	}
	if nodeID == "" {
		// Without a node id there is nothing to look the key up under.
		return nil, PermanentFaultf(FaultSignature, "crawler: no nodeId given, cannot resolve key %q", keyID)
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
		// TRANSIENT, and this distinction is the whole retry-vs-give-up decision.
		// A registry that is down, rate limiting, or timing out says nothing
		// about this key. Returned unclassified, so ClassifyFault reports
		// FaultTransient.
		return nil, fmt.Errorf("crawler: registry lookup for node %q key %q: %w", ck.nodeID, ck.keyID, err)
	}
	if len(subs) == 0 {
		// PERMANENT: the registry answered, and the answer is that this key does
		// not exist for this node. Retrying cannot change that.
		return nil, PermanentFaultf(FaultSignature, "crawler: registry has no key %q for node %q", ck.keyID, ck.nodeID)
	}
	sub := subs[0]
	// Revoked, expired and invalid-SSL subscriptions must not verify anything.
	if !model.IsKeyStatusUsable(sub.Status) {
		return nil, PermanentFaultf(FaultSignature, "crawler: registry key %q for node %q has unusable status %q", ck.keyID, ck.nodeID, sub.Status)
	}
	if sub.SigningPublicKey == "" {
		return nil, PermanentFaultf(FaultSignature, "crawler: registry key %q for node %q has no signing public key", ck.keyID, ck.nodeID)
	}
	raw, err := base64.StdEncoding.DecodeString(sub.SigningPublicKey)
	if err != nil {
		return nil, PermanentFaultf(FaultSignature, "crawler: registry key %q for node %q is not valid base64: %v", ck.keyID, ck.nodeID, err)
	}
	if len(raw) != ed25519.PublicKeySize {
		// Guard before ed25519.Verify, which panics on a mis-sized key.
		return nil, PermanentFaultf(FaultSignature, "crawler: registry key %q for node %q decodes to %d bytes, want %d",
			ck.keyID, ck.nodeID, len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// ResolveSigningKey is the shared "look up keyID under nodeID, give a
// definitive verdict or a retryable one" step a caller's own signature gate
// uses before verifying.
func ResolveSigningKey(ctx context.Context, keys KeySource, nodeID, keyID, what string) (ed25519.PublicKey, error) {
	if keys == nil {
		// Fail closed: with no key source there is nothing to verify against, so
		// the signature gate would otherwise silently degrade to "unverified".
		return nil, PermanentFaultf(FaultSignature, "crawler: no key source configured, refusing unverifiable %s", what)
	}
	pub, err := keys(ctx, nodeID, keyID)
	if err != nil {
		if IsPermanent(err) {
			return nil, PermanentFaultf(FaultSignature, "crawler: %s resolving signing key %q: %v", what, keyID, err)
		}
		// The key source could not answer (registry unreachable). Stay transient
		// so a caller retries instead of giving up on healthy content.
		return nil, fmt.Errorf("crawler: %s resolving signing key %q: %w", what, keyID, err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return nil, PermanentFaultf(FaultSignature, "crawler: %s key %q is not a %d-byte Ed25519 key", what, keyID, ed25519.PublicKeySize)
	}
	return pub, nil
}

// VerifySignature verifies a self-signature over raw: the document minus its
// own field named sigField was signed, under keyID, by the key registered for
// nodeID. This is the generic gate -- it does not know or care what raw
// otherwise contains, so unwrapping a caller-specific envelope or
// cross-checking caller-specific fields (e.g. a declared id/version matching
// what an index entry expected) is the caller's responsibility, done after
// this call succeeds.
func VerifySignature(ctx context.Context, keys KeySource, nodeID, keyID, sigValue string, raw []byte, sigField string) error {
	if sigValue == "" || keyID == "" {
		return PermanentFaultf(FaultSignature, "crawler: no signature to verify")
	}
	pub, err := ResolveSigningKey(ctx, keys, nodeID, keyID, "self-signature")
	if err != nil {
		return err
	}
	if err := artifactverifier.VerifyJSON(raw, sigField, sigValue, pub); err != nil {
		return PermanentFaultf(FaultSignature, "crawler: signature verification failed: %v", err)
	}
	return nil
}
