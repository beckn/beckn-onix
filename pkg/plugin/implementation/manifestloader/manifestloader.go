package manifestloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// Config controls fetch and cache behavior for the manifest loader.
type Config struct {
	CacheTTL     time.Duration
	FetchTimeout time.Duration
}

// Loader fetches, verifies, caches, and returns manifests.
type Loader struct {
	cache    definition.Cache
	registry definition.RegistryMetadataLookup
	config   *Config
	client   *http.Client
}

var (
	ErrNilCache    = errors.New("cache implementation cannot be nil")
	ErrNilRegistry = errors.New("registry metadata lookup cannot be nil")
)

const (
	defaultCacheTTL     = 6 * time.Hour
	defaultFetchTimeout = 30 * time.Second
)

var httpClientFunc = func(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func New(ctx context.Context, cache definition.Cache, registry definition.RegistryMetadataLookup, cfg *Config) (*Loader, func() error, error) {
	_ = ctx
	if cache == nil {
		return nil, nil, ErrNilCache
	}
	if registry == nil {
		return nil, nil, ErrNilRegistry
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaultCacheTTL
	}
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = defaultFetchTimeout
	}

	loader := &Loader{
		cache:    cache,
		registry: registry,
		config:   cfg,
		client:   httpClientFunc(cfg.FetchTimeout),
	}
	return loader, func() error { return nil }, nil
}

func (l *Loader) GetByNetworkID(ctx context.Context, networkID string) (*model.ManifestDocument, error) {
	if strings.TrimSpace(networkID) == "" {
		return nil, fmt.Errorf("networkID cannot be empty")
	}
	if doc, err := l.loadFromCache(ctx, networkCacheKey(networkID)); err == nil {
		return doc, nil
	}

	namespaceIdentifier, registryName, err := splitNetworkID(networkID)
	if err != nil {
		return nil, err
	}
	meta, err := l.registry.LookupRegistry(ctx, namespaceIdentifier, registryName)
	if err != nil {
		return nil, err
	}
	manifestMetadata, err := metadataFromRegistry(meta)
	if err != nil {
		return nil, err
	}
	doc, err := l.GetByMetadata(ctx, manifestMetadata)
	if err != nil {
		return nil, err
	}
	doc.NetworkID = networkID
	if err := l.store(ctx, networkCacheKey(networkID), doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (l *Loader) GetByMetadata(ctx context.Context, metadata model.ManifestMetadata) (*model.ManifestDocument, error) {
	if err := validateMetadata(metadata); err != nil {
		return nil, err
	}
	cacheKey := metadata.CacheKey()
	if doc, err := l.loadFromCache(ctx, cacheKey); err == nil {
		return doc, nil
	}

	manifestBody, manifestContentType, err := l.fetchURL(ctx, metadata.ManifestURL)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	signatureBody, _, err := l.fetchURL(ctx, metadata.ManifestSignatureURL)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest signature: %w", err)
	}
	publicKeyBody, _, err := l.fetchURL(ctx, metadata.SigningPublicKeyLookupURL)
	if err != nil {
		return nil, fmt.Errorf("fetch signing public key: %w", err)
	}
	if err := artifactverifier.VerifyDetachedArtifact(manifestBody, signatureBody, publicKeyBody); err != nil {
		return nil, fmt.Errorf("manifest signature verification failed: %w", err)
	}
	digest := sha256.Sum256(manifestBody)
	doc := &model.ManifestDocument{
		ContentType:  manifestContentType,
		Content:      manifestBody,
		Digest:       hex.EncodeToString(digest[:]),
		SourceURL:    metadata.ManifestURL,
		SignatureURL: metadata.ManifestSignatureURL,
		Verified:     true,
		FetchedAt:    time.Now().UTC(),
	}
	if err := l.store(ctx, cacheKey, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (l *Loader) fetchURL(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func (l *Loader) loadFromCache(ctx context.Context, key string) (*model.ManifestDocument, error) {
	raw, err := l.cache.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var doc model.ManifestDocument
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (l *Loader) store(ctx context.Context, key string, doc *model.ManifestDocument) error {
	payload, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return l.cache.Set(ctx, key, string(payload), l.config.CacheTTL)
}

func networkCacheKey(networkID string) string {
	return "manifest:network:" + networkID
}

func splitNetworkID(networkID string) (string, string, error) {
	parts := strings.SplitN(networkID, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("invalid networkID %q: expected <namespaceIdentifier>/<registryName>", networkID)
	}
	return parts[0], parts[1], nil
}

func metadataFromRegistry(meta *model.RegistryMetadata) (model.ManifestMetadata, error) {
	if meta == nil {
		return model.ManifestMetadata{}, fmt.Errorf("registry metadata cannot be nil")
	}
	result := model.ManifestMetadata{
		ManifestURL:               meta.RawMeta["manifest_url"],
		ManifestSignatureURL:      meta.RawMeta["manifest_signature_url"],
		SigningPublicKeyLookupURL: meta.RawMeta["signing_public_key_lookup_url"],
	}
	return result, validateMetadata(result)
}

func validateMetadata(metadata model.ManifestMetadata) error {
	if strings.TrimSpace(metadata.ManifestURL) == "" {
		return fmt.Errorf("manifest_url missing in metadata")
	}
	if strings.TrimSpace(metadata.ManifestSignatureURL) == "" {
		return fmt.Errorf("manifest_signature_url missing in metadata")
	}
	if strings.TrimSpace(metadata.SigningPublicKeyLookupURL) == "" {
		return fmt.Errorf("signing_public_key_lookup_url missing in metadata")
	}
	return nil
}
