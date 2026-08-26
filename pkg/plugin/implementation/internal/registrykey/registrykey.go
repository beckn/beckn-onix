// Package registrykey adapts this onix deployment's own
// definition.RegistryLookup into a crawler.KeySource -- the generic
// key-lookup function catalog-core's Fetcher calls internally to verify a
// fetched artifact's signature. catalog-core knows nothing about onix's
// Subscription/status model, so the base64 decoding, usable-status check,
// and fault classification live here.
//
// Shared by catalogcrawler and catalogpublisher: both need to resolve
// {nodeID, keyID} against the real registry and classify the result the
// same way (a down/rate-limited registry is transient and worth retrying;
// a missing/unusable/malformed key is permanent and never will be), so a
// future fix to that classification (a new model.Status value, a changed
// encoding convention) only has one place to land instead of drifting
// between two copies. Lives under pkg/plugin/implementation/internal
// specifically so plugin implementation packages still don't cross-import
// each other directly -- only this common, dependency-free helper is
// shared.
//
// No caching is added at this layer: definition.RegistryLookup
// implementations (registry, dediregistry) already cache Lookup results
// themselves; a second, independently-expiring cache here would risk
// serving a revoked/rotated key past the registry plugin's own cache
// invalidation.
package registrykey

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"

	"github.com/beckn/catalog-core/pkg/catalog/crawler"
)

// Source adapts reg into a crawler.KeySource. caller labels every error
// this produces (e.g. "catalogcrawler", "catalogpublisher") so a log line
// or PublishError/CrawlError reason still says which plugin hit it.
func Source(caller string, reg definition.RegistryLookup) crawler.KeySource {
	return func(ctx context.Context, nodeID, keyID string) (ed25519.PublicKey, error) {
		if reg == nil {
			// Fail closed. Wired without a registry, there is no way to learn any
			// key, so nothing can be verified.
			return nil, crawler.PermanentFaultf(crawler.FaultSignature, "%s: no registry configured, cannot resolve key %q", caller, keyID)
		}
		if nodeID == "" {
			return nil, crawler.PermanentFaultf(crawler.FaultSignature, "%s: no nodeId given, cannot resolve key %q", caller, keyID)
		}
		return Resolve(ctx, caller, reg, nodeID, keyID)
	}
}

// Resolve asks the registry for {subscriberID, keyID} and turns the answer
// into a usable Ed25519 key, or into a correctly classified failure.
func Resolve(ctx context.Context, caller string, reg definition.RegistryLookup, nodeID, keyID string) (ed25519.PublicKey, error) {
	subs, err := reg.Lookup(ctx, &model.Subscription{
		Subscriber: model.Subscriber{SubscriberID: nodeID},
		KeyID:      keyID,
	})
	if err != nil {
		// TRANSIENT: a registry that is down, rate limiting, or timing out says
		// nothing about this key. Returned unclassified, so ClassifyFault reports
		// FaultTransient.
		return nil, fmt.Errorf("%s: registry lookup for node %q key %q: %w", caller, nodeID, keyID, err)
	}
	if len(subs) == 0 {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "%s: registry has no key %q for node %q", caller, keyID, nodeID)
	}
	sub := subs[0]
	if !model.IsKeyStatusUsable(sub.Status) {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "%s: registry key %q for node %q has unusable status %q", caller, keyID, nodeID, sub.Status)
	}
	if sub.SigningPublicKey == "" {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "%s: registry key %q for node %q has no signing public key", caller, keyID, nodeID)
	}
	raw, err := base64.StdEncoding.DecodeString(sub.SigningPublicKey)
	if err != nil {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "%s: registry key %q for node %q is not valid base64: %v", caller, keyID, nodeID, err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "%s: registry key %q for node %q decodes to %d bytes, want %d",
			caller, keyID, nodeID, len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
