package catalogpublisher

// keysource.go — builds a crawler.KeySource on top of this onix
// deployment's own definition.RegistryLookup, for verify.go's post-write
// verification. A near-duplicate of catalogcrawler's own keysource.go --
// deliberately not imported from there (plugin implementation packages
// don't cross-import each other in this codebase); the actual shared
// logic is catalog-core's crawler.VerifySignature/catalog.Fetcher, not
// this small adapter.
//
// No caching is added at this layer: definition.RegistryLookup
// implementations (registry, dediregistry) already cache Lookup results
// themselves; a second, independently-expiring cache here would risk
// serving a revoked/rotated key past the registry plugin's own cache
// invalidation.
import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"

	"github.com/beckn/catalog-core/pkg/catalog/crawler"
)

// registryKeySource adapts a definition.RegistryLookup to a
// crawler.KeySource.
func registryKeySource(reg definition.RegistryLookup) crawler.KeySource {
	return func(ctx context.Context, nodeID, keyID string) (ed25519.PublicKey, error) {
		if reg == nil {
			// Fail closed. Wired without a registry, there is no way to learn any
			// key, so nothing can be verified.
			return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: no registry configured, cannot resolve key %q", keyID)
		}
		if nodeID == "" {
			return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: no nodeId given, cannot resolve key %q", keyID)
		}
		return resolveRegistryKey(ctx, reg, nodeID, keyID)
	}
}

// resolveRegistryKey asks the registry for {subscriberID, keyID} and turns
// the answer into a usable Ed25519 key, or into a correctly classified
// failure.
func resolveRegistryKey(ctx context.Context, reg definition.RegistryLookup, nodeID, keyID string) (ed25519.PublicKey, error) {
	subs, err := reg.Lookup(ctx, &model.Subscription{
		Subscriber: model.Subscriber{SubscriberID: nodeID},
		KeyID:      keyID,
	})
	if err != nil {
		// TRANSIENT: a registry that is down, rate limiting, or timing out says
		// nothing about this key. Returned unclassified, so ClassifyFault reports
		// FaultTransient.
		return nil, fmt.Errorf("catalogpublisher: registry lookup for node %q key %q: %w", nodeID, keyID, err)
	}
	if len(subs) == 0 {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: registry has no key %q for node %q", keyID, nodeID)
	}
	sub := subs[0]
	if !model.IsKeyStatusUsable(sub.Status) {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: registry key %q for node %q has unusable status %q", keyID, nodeID, sub.Status)
	}
	if sub.SigningPublicKey == "" {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: registry key %q for node %q has no signing public key", keyID, nodeID)
	}
	raw, err := base64.StdEncoding.DecodeString(sub.SigningPublicKey)
	if err != nil {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: registry key %q for node %q is not valid base64: %v", keyID, nodeID, err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, crawler.PermanentFaultf(crawler.FaultSignature, "catalogpublisher: registry key %q for node %q decodes to %d bytes, want %d",
			keyID, nodeID, len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
