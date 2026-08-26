// Package catalogpublisher implements definition.CatalogPublisher by
// standing up pkg/catalog/publisher's RFC NFH-014 publish logic as an
// onix plugin: it resolves the signing keyset via KeyManager (fresh every
// call, so a key rotation is picked up immediately) and its own config,
// and converts between definition's own types and pkg/catalog/publisher's/
// pkg/catalog/store's. All of the actual diffing/versioning/compaction/
// signing logic lives in pkg/catalog/publisher, not here -- this package
// holds no spec logic of its own.
package catalogpublisher

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"
	"github.com/beckn/catalog-core/pkg/catalog/publisher"
	"github.com/beckn/catalog-core/pkg/catalog/store"
)

// Config controls publish behavior -- resolved once at plugin construction
// and passed straight through to pkg/catalog/publisher.Publish as
// explicit per-call parameters every time. See pkg/catalog/publisher.
// Params for what each field actually does; this Config exists only to
// receive it from onix plugin config, not to add any policy of its own.
type Config struct {
	// SubscriberID is the identifier passed to KeyManager.Keyset to load
	// the signing keypair.
	SubscriberID string

	NextUpdateIn                   time.Duration
	PublicBaseURL                  string
	PublishLatest                  bool
	Gzip                           bool
	CompactionChangeCountThreshold int
	CompactionSizeRatioThreshold   float64

	// CheckCatalogIndexLink, when true, makes Publish also check whether
	// this node's DeDi registry record already links its catalog index
	// (see registrylink.go's checkIndexLink), surfacing a miss as a
	// PublishResult.Warnings entry. Requires a non-nil
	// RegistryMetadataLookup to have been supplied to New -- validated at
	// construction time so a missing dependency fails fast at startup
	// rather than on the first Publish call.
	CheckCatalogIndexLink bool
}

// ParseCheckCatalogIndexLink parses the "checkCatalogIndexLink" plugin
// config value -- shared by cmd/plugin.go's parseConfig (which sets
// Config.CheckCatalogIndexLink) and handler.go's NewHandler (which needs
// the same decision before New is even called, to know whether to resolve
// a RegistryMetadataLookup). A single implementation so the two call sites
// can't drift apart. An absent/empty value means false, not an error.
func ParseCheckCatalogIndexLink(config map[string]string) (bool, error) {
	v := config["checkCatalogIndexLink"]
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid checkCatalogIndexLink value %q: %w", v, err)
	}
	return b, nil
}

// Publisher implements definition.CatalogPublisher.
type Publisher struct {
	keyManager   definition.KeyManager
	blobStore    definition.CatalogBlobStore
	catalogStore *store.Store
	// registryMetadata is the DeDi-native RegistryMetadataLookup used to
	// read this node's own registry record (read-only) and check whether
	// its meta.catalog_index_urls already links this publisher's catalog
	// index -- see registrylink.go's checkIndexLink. nil when
	// config.CheckCatalogIndexLink is false.
	registryMetadata definition.RegistryMetadataLookup
	// fetcher re-fetches and re-verifies what Publish just wrote (see
	// verify.go), against registry (below) -- the same RegistryLookup
	// already required to load keyManager, reused here rather than adding
	// a second registry config field.
	fetcher  *catalog.Fetcher
	registry definition.RegistryLookup
	config   *Config
	log      *slog.Logger
}

// New creates a Publisher instance. pkg/catalog/publisher's own logging
// (diff summaries, mode decisions and why, signing, compaction triggers,
// ...) is bridged into this package's own pkg/log -- so it shows up in
// the same log stream, at whatever level onix itself is configured for,
// with nothing further to wire up. blobStore is required: Publish always
// persists what it produces somewhere. registryMetadata is required only
// when cfg.CheckCatalogIndexLink is true. registry is required
// unconditionally: verify.go's post-write verification always re-resolves
// the signing key via a real registry lookup (never the local keyset
// Publish just signed with), and there is deliberately no way to disable
// that check.
func New(ctx context.Context, keyManager definition.KeyManager, blobStore definition.CatalogBlobStore, registry definition.RegistryLookup, registryMetadata definition.RegistryMetadataLookup, cfg *Config) (*Publisher, func() error, error) {
	if keyManager == nil {
		return nil, nil, fmt.Errorf("catalogpublisher: KeyManager plugin not configured")
	}
	if cfg == nil || cfg.SubscriberID == "" {
		return nil, nil, fmt.Errorf("catalogpublisher: subscriberID is required")
	}
	if blobStore == nil {
		return nil, nil, fmt.Errorf("catalogpublisher: CatalogBlobStore plugin not configured")
	}
	if registry == nil {
		return nil, nil, fmt.Errorf("catalogpublisher: Registry plugin not configured (required to verify published files against the real registered signing key)")
	}

	if cfg.CheckCatalogIndexLink {
		if registryMetadata == nil {
			return nil, nil, fmt.Errorf("catalogpublisher: RegistryMetadataLookup not configured (needed for the catalog-index link check)")
		}
		// subscriberID is fixed for the plugin's lifetime, so its shape can
		// be validated once here: the registry self-lookup's synthetic path
		// (subscriberID/wildcard/keyID, see registrylink.go) requires
		// exactly 3 non-empty slash-separated parts downstream
		// (dediregistry.LookupNode) -- a "/" inside subscriberID would
		// silently produce a malformed path on every single check instead
		// of failing loudly here at startup.
		if strings.Contains(cfg.SubscriberID, "/") {
			return nil, nil, fmt.Errorf("catalogpublisher: subscriberId %q cannot contain \"/\" (needed to build the registry self-lookup's synthetic path)", cfg.SubscriberID)
		}
		// Resolve once here too, purely to fail fast at startup on a
		// missing/broken keyset -- the actual keyID used per-check is
		// re-resolved fresh in checkIndexLink, so this result itself is
		// intentionally discarded.
		if _, err := keyManager.Keyset(ctx, cfg.SubscriberID); err != nil {
			return nil, nil, fmt.Errorf("catalogpublisher: resolving keyset for registry self-lookup (subscriberId=%s): %w", cfg.SubscriberID, err)
		}
	}

	// WithLogger bridges pkg/catalog/store's own log/slog logging into
	// this package's own onix logging (pkg/log) -- same log stream, same
	// configured level, nothing further to wire up.
	catalogStore := store.New(blobStore).WithLogger(slog.New(log.NewSlogHandler()))

	// Fixed, generous defaults -- no new config keys for this (verify.go's
	// fetches are of files this same process just wrote, not arbitrary
	// third-party content, so there's no operator-facing tuning need the
	// way catalogcrawler's own fetch limits have).
	//
	// allowPrivateHosts=true, unlike catalogcrawler's own client: that
	// SSRF guard exists to stop an untrusted, externally-supplied index
	// entry from tricking the crawler into fetching an internal-network
	// target. cfg.PublicBaseURL carries no such risk -- it's the
	// deployment operator's own trusted config (the same trust level as
	// dbDsn/discoveryPushUrl elsewhere in this codebase, neither of which
	// is SSRF-guarded either), and legitimately may point at a private
	// network (an internal reverse proxy, an air-gapped deployment)
	// before public DNS/routing exists.
	client := crawler.NewClient(defaultVerifyFetchTimeout, defaultVerifyMaxFetchBytes, true)
	fetcher := catalog.NewFetcher(client, registryKeySource(registry), defaultVerifyMaxDecompressedBytes)

	return &Publisher{
		keyManager:       keyManager,
		blobStore:        blobStore,
		catalogStore:     catalogStore,
		registryMetadata: registryMetadata,
		fetcher:          fetcher,
		registry:         registry,
		config:           cfg,
		log:              slog.New(log.NewSlogHandler()),
	}, func() error { return nil }, nil
}

const (
	defaultVerifyFetchTimeout         = 30 * time.Second
	defaultVerifyMaxFetchBytes        = 10 << 20
	defaultVerifyMaxDecompressedBytes = 20 << 20
)

// Publish resolves this call's signing keyset, loads prior state for the
// submitted/retired catalogs from its own CatalogBlobStore, delegates the
// actual diffing/signing/versioning to pkg/catalog/publisher.Publish,
// persists the result back to storage, and -- if configured -- runs the
// registry catalog-index-link check, surfacing a miss as a non-fatal
// warning rather than failing the call.
func (p *Publisher) Publish(ctx context.Context, req definition.PublishRequest) (definition.PublishResult, error) {
	keyset, err := p.keyManager.Keyset(ctx, p.config.SubscriberID)
	if err != nil {
		return definition.PublishResult{}, fmt.Errorf("catalogpublisher: loading keyset %q: %w", p.config.SubscriberID, err)
	}
	priv, _, err := decodeKeyset(keyset)
	if err != nil {
		return definition.PublishResult{}, fmt.Errorf("catalogpublisher: decoding keyset %q: %w", p.config.SubscriberID, err)
	}

	// Retired catalogIds need their prior state loaded too -- the
	// tombstone Publish builds for them carries forward their prior
	// CatalogType/NetworkIds/SchemaTypes and bumps their EntryVersion
	// (NFH-014 Appendix A, Example 4's retired entry still carries those
	// fields), not just retiredAt.
	loadIDs := append(nonEmptyCatalogIDs(req.Catalogs), req.Retire...)

	priorStates, err := p.catalogStore.LoadCatalogs(ctx, loadIDs)
	if err != nil {
		return definition.PublishResult{}, fmt.Errorf("catalogpublisher: loading prior state: %w", err)
	}

	// A catalogId present in both req.Retire and req.Catalogs is published
	// normally, per PublishRequest.Retire's own doc comment -- so a
	// synthetic retire Submission is only added for an id req.Catalogs
	// doesn't already carry, avoiding two Submission entries for the same
	// CatalogID.
	submitted := make(map[string]bool, len(req.Catalogs))
	for _, id := range nonEmptyCatalogIDs(req.Catalogs) {
		submitted[id] = true
	}
	var retireOnly []string
	for _, id := range req.Retire {
		if !submitted[id] {
			retireOnly = append(retireOnly, id)
		}
	}

	result, err := publisher.Publish(ctx, publisher.Params{
		Catalogs:   append(toSubmissions(req.Catalogs), retireSubmissions(retireOnly)...),
		PriorState: priorStates,

		CompactionChangeCountThreshold: p.config.CompactionChangeCountThreshold,
		CompactionSizeRatioThreshold:   p.config.CompactionSizeRatioThreshold,
		Gzip:                           p.config.Gzip,
		PublishLatest:                  p.config.PublishLatest,
		NextUpdateIn:                   p.config.NextUpdateIn,
		PublicBaseURL:                  p.config.PublicBaseURL,

		SigningKey: priv,
		KeyID:      keyset.UniqueKeyID,
		Domain:     keyset.SubscriberID,

		Logger: p.log,
	})
	if err != nil {
		return definition.PublishResult{}, err
	}

	definitionResult := toDefinitionResult(result)

	if err := p.catalogStore.Publish(ctx, ToStorePublishRequest(definitionResult)); err != nil {
		return definition.PublishResult{}, fmt.Errorf("catalogpublisher: persisting publish result: %w", err)
	}

	// Post-write verification (verify.go): re-fetch and re-verify whatever
	// this call actually touched against a real registry key lookup. A
	// failure here means the write committed but isn't actually
	// crawlable, so it's appended to Errors -- the same non-fatal,
	// per-catalog PublishError vocabulary as any other failure -- rather
	// than left silently reported as a successful CatalogPublishOutcome.
	definitionResult.Errors = append(definitionResult.Errors, p.verifyPublished(ctx, definitionResult)...)

	if p.config.CheckCatalogIndexLink && p.registryMetadata != nil {
		if warning, err := p.checkIndexLink(ctx); err != nil {
			p.log.Warn("catalogpublisher: registry catalog-index link check failed", "error", err)
		} else if warning != "" {
			definitionResult.Warnings = append(definitionResult.Warnings, warning)
		}
	}

	return definitionResult, nil
}

// IndexURL implements definition.CatalogPublisher.
func (p *Publisher) IndexURL() string { return publisher.IndexURL(p.config.PublicBaseURL) }

// decodeKeyset decodes a model.Keyset's base64-encoded signing keypair
// into raw Ed25519 keys, matching the exact encoding convention
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
