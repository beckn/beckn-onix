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
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalog/publisher"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
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
}

// Publisher implements definition.CatalogPublisher.
type Publisher struct {
	keyManager definition.KeyManager
	config     *Config
	log        *slog.Logger
}

// New creates a Publisher instance. pkg/catalog/publisher's own logging
// (diff summaries, mode decisions and why, signing, compaction triggers,
// ...) is bridged into this package's own pkg/log -- so it shows up in
// the same log stream, at whatever level onix itself is configured for,
// with nothing further to wire up.
func New(ctx context.Context, keyManager definition.KeyManager, cfg *Config) (*Publisher, func() error, error) {
	if keyManager == nil {
		return nil, nil, fmt.Errorf("catalogpublisher: KeyManager plugin not configured")
	}
	if cfg == nil || cfg.SubscriberID == "" {
		return nil, nil, fmt.Errorf("catalogpublisher: subscriberID is required")
	}
	return &Publisher{keyManager: keyManager, config: cfg, log: slog.New(log.NewSlogHandler())}, func() error { return nil }, nil
}

// Publish resolves this call's signing keyset and delegates everything
// else to pkg/catalog/publisher.Publish.
func (p *Publisher) Publish(ctx context.Context, req definition.PublishRequest) (definition.PublishResult, error) {
	keyset, err := p.keyManager.Keyset(ctx, p.config.SubscriberID)
	if err != nil {
		return definition.PublishResult{}, fmt.Errorf("catalogpublisher: loading keyset %q: %w", p.config.SubscriberID, err)
	}
	priv, _, err := decodeKeyset(keyset)
	if err != nil {
		return definition.PublishResult{}, fmt.Errorf("catalogpublisher: decoding keyset %q: %w", p.config.SubscriberID, err)
	}

	result, err := publisher.Publish(ctx, publisher.Params{
		Catalogs:      toSubmissions(req.Catalogs),
		PriorState:    toCatalogStates(req.PriorState),
		Retire:        req.Retire,
		ForceBaseline: req.ForceBaseline,

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
	return toDefinitionResult(result), nil
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
