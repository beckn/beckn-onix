package catalogpublisher

import (
	"context"
	"fmt"
	"strings"
)

// dediSubscriberWildcardRegistry is the DeDi registry service's special
// "search across all registries" value -- the same one
// dediregistry.dediAllRegistriesWildcard uses for RegistryLookup.Lookup's
// signing-key resolution during ordinary transactions. Duplicated here
// (rather than exporting dediregistry's unexported constant, or adding a
// dedicated subscriberID+keyID-shaped method to RegistryMetadataLookup) to
// keep this change's footprint small; giving RegistryMetadataLookup a
// proper subscriberID+keyID-shaped lookup method instead of this
// synthetic-path workaround is a separate, better-scoped follow-up.
const dediSubscriberWildcardRegistry = "subscribers.beckn.one"

// catalogIndexMetaKey is the DeDi registry record's meta field this check
// looks at: the direct link from a subscriber's own DeDi record to the
// catalog index(es) it publishes. Replaces the earlier three-level
// indirection (DeDi record -> node manifest -> catalog index) with a
// two-level one (DeDi record -> catalog index directly). Plural, an array
// of {url} objects, per NFH-014 §Schema Changes ("Beckn_subscriber
// (unmodified) + meta.catalog_index_urls") -- a node MAY host more than
// one catalog index, so this is never a single-value field.
const catalogIndexMetaKey = "catalog_index_urls"

// checkIndexLink reads this node's own DeDi registry record (read-only, via
// RegistryMetadataLookup.LookupNode -- dediregistry has no write path, so
// there is nothing this check could push even if it wanted to) and checks
// whether its meta.catalog_index_urls already includes this publisher's
// index URL (p.IndexURL()) -- plural, since a node MAY host more than one
// catalog index, so the match is membership in the array, not equality
// against a single value. Unlike the earlier node-manifest-based check,
// there is no local artifact to stage: getting a value into a DeDi record's
// meta is, and remains, an external, manual operator action (e.g. via
// DeDi's own registration tooling) -- a missing link is reported as a
// warning naming the meta key and the URL it should be added to. Returns
// ("", nil) when the link already matches.
func (p *Publisher) checkIndexLink(ctx context.Context) (string, error) {
	indexURL := p.IndexURL()

	keyset, err := p.keyManager.Keyset(ctx, p.config.SubscriberID)
	if err != nil {
		return "", fmt.Errorf("resolving keyset for registry self-lookup (subscriberId=%s): %w", p.config.SubscriberID, err)
	}
	keyID := keyset.UniqueKeyID
	if keyID == "" {
		return "", fmt.Errorf("keyset for subscriberId=%s has no keyId; cannot build registry self-lookup path", p.config.SubscriberID)
	}
	if strings.Contains(keyID, "/") {
		return "", fmt.Errorf("keyId %q for subscriberId=%s cannot contain \"/\" (needed to build the registry self-lookup's synthetic path)", keyID, p.config.SubscriberID)
	}

	syntheticNodeID := p.config.SubscriberID + "/" + dediSubscriberWildcardRegistry + "/" + keyID
	record, err := p.registryMetadata.LookupNode(ctx, syntheticNodeID)
	if err != nil {
		return "", fmt.Errorf("looking up DeDi record for %s: %w", p.config.SubscriberID, err)
	}

	for _, url := range record.MetaArrays[catalogIndexMetaKey] {
		if url == indexURL {
			return "", nil
		}
	}

	return fmt.Sprintf(
		"DeDi record for %s does not link catalog index %s; add this URL to meta.%s on your DeDi record",
		p.config.SubscriberID, indexURL, catalogIndexMetaKey,
	), nil
}
