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
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// Config controls fetch and cache behavior for the manifest loader.
type Config struct {
	CacheTTL            time.Duration
	FetchTimeout        time.Duration
	DisableCache        bool
	ForceRefreshOnStart bool
}

// Loader fetches, verifies, caches, and returns manifests.
type Loader struct {
	cache         definition.Cache
	registry      definition.RegistryMetadataLookup
	config        *Config
	client        *http.Client
	refreshMu     sync.Mutex
	refreshedKeys map[string]bool
}

var (
	ErrNilCache    = errors.New("cache implementation cannot be nil")
	ErrNilRegistry = errors.New("registry metadata lookup cannot be nil")
)

const (
	defaultCacheTTL         = 6 * time.Hour
	defaultFetchTimeout     = 30 * time.Second
	maxManifestArtifactSize = 10 << 20 // 10 MiB
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
		cache:         cache,
		registry:      registry,
		config:        cfg,
		client:        httpClientFunc(cfg.FetchTimeout),
		refreshedKeys: make(map[string]bool),
	}
	return loader, func() error { return nil }, nil
}

func (l *Loader) GetByNetworkID(ctx context.Context, networkID string) (*model.ManifestDocument, error) {
	if strings.TrimSpace(networkID) == "" {
		return nil, fmt.Errorf("networkID cannot be empty")
	}

	networkKey := networkCacheKey(networkID)
	bypassCache := l.shouldBypassCache(networkKey)
	if !bypassCache {
		if doc, err := l.loadFromCache(ctx, networkKey); err == nil {
			log.Infof(ctx, "ManifestLoader: cache hit for networkID=%q fetchedAt=%s source=%s", networkID, doc.FetchedAt.Format(time.RFC3339), doc.SourceURL)
			return doc, nil
		} else {
			log.Debugf(ctx, "ManifestLoader: cache miss for networkID=%q key=%q: %v", networkID, networkKey, err)
		}
	} else {
		log.Infof(ctx, "ManifestLoader: bypassing cache for networkID=%q", networkID)
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
	doc, err := l.getByMetadata(ctx, manifestMetadata, bypassCache)
	if err != nil {
		return nil, err
	}
	doc.NetworkID = networkID
	// Store under the network key in addition to the metadata hash key so future
	// GetByNetworkID calls can short-circuit registry metadata lookup.
	if err := l.store(ctx, networkKey, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (l *Loader) GetByMetadata(ctx context.Context, metadata model.ManifestMetadata) (*model.ManifestDocument, error) {
	return l.getByMetadata(ctx, metadata, l.shouldBypassCache(metadata.CacheKey()))
}

func (l *Loader) getByMetadata(ctx context.Context, metadata model.ManifestMetadata, bypassCache bool) (*model.ManifestDocument, error) {
	if err := validateMetadata(metadata); err != nil {
		return nil, err
	}
	cacheKey := metadata.CacheKey()
	if !bypassCache {
		if doc, err := l.loadFromCache(ctx, cacheKey); err == nil {
			log.Infof(ctx, "ManifestLoader: metadata cache hit for source=%s fetchedAt=%s", metadata.ManifestURL, doc.FetchedAt.Format(time.RFC3339))
			return doc, nil
		} else {
			log.Debugf(ctx, "ManifestLoader: metadata cache miss for source=%s key=%q: %v", metadata.ManifestURL, cacheKey, err)
		}
	} else {
		log.Infof(ctx, "ManifestLoader: bypassing metadata cache for source=%s", metadata.ManifestURL)
	}

	log.Infof(ctx, "ManifestLoader: fetching manifest source=%s signature=%s signingKey=%s", metadata.ManifestURL, metadata.ManifestSignatureURL, metadata.SigningPublicKeyLookupURL)
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
	log.Infof(ctx, "ManifestLoader: verified and cached manifest source=%s digest=%s", metadata.ManifestURL, doc.Digest)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestArtifactSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxManifestArtifactSize {
		return nil, "", fmt.Errorf("response body exceeds maximum allowed size of %d bytes", maxManifestArtifactSize)
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
	if !doc.Verified {
		return nil, fmt.Errorf("cached manifest %q is not marked verified", key)
	}
	return &doc, nil
}

func (l *Loader) store(ctx context.Context, key string, doc *model.ManifestDocument) error {
	if l.config.DisableCache {
		return nil
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return l.cache.Set(ctx, key, string(payload), l.config.CacheTTL)
}

func (l *Loader) shouldBypassCache(key string) bool {
	if l.config.DisableCache {
		return true
	}
	if !l.config.ForceRefreshOnStart {
		return false
	}
	l.refreshMu.Lock()
	defer l.refreshMu.Unlock()
	if l.refreshedKeys[key] {
		return false
	}
	l.refreshedKeys[key] = true
	return true
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
