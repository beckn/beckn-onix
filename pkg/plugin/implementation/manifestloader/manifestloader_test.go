package manifestloader

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

type mockCache struct {
	store map[string]string
	err   error
}

func (m *mockCache) Get(ctx context.Context, key string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	value, ok := m.store[key]
	if !ok {
		return "", errors.New("cache miss")
	}
	return value, nil
}
func (m *mockCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if m.err != nil {
		return m.err
	}
	m.store[key] = value
	return nil
}
func (m *mockCache) Delete(ctx context.Context, key string) error { delete(m.store, key); return nil }
func (m *mockCache) Clear(ctx context.Context) error              { m.store = map[string]string{}; return nil }

type mockRegistry struct {
	meta  *model.RegistryMetadata
	err   error
	calls int
}

func (m *mockRegistry) LookupRegistry(ctx context.Context, namespaceIdentifier, registryName string) (*model.RegistryMetadata, error) {
	m.calls++
	return m.meta, m.err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func response(status int, body string, contentType string) *http.Response {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestGetByMetadata(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	manifest := []byte("manifest: true")
	signature := ed25519.Sign(privateKey, manifest)

	originalHTTPClientFunc := httpClientFunc
	httpClientFunc = func(timeout time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://example.org/manifest":
				return response(200, string(manifest), "application/yaml"), nil
			case "https://example.org/manifest.sig":
				return response(200, base64.StdEncoding.EncodeToString(signature), "text/plain"), nil
			case "https://example.org/pubkey":
				return response(200, base64.StdEncoding.EncodeToString(publicKey), "text/plain"), nil
			default:
				return response(404, "not found", "text/plain"), nil
			}
		})}
	}
	defer func() { httpClientFunc = originalHTTPClientFunc }()

	cache := &mockCache{store: map[string]string{}}
	registry := &mockRegistry{}
	loader, _, err := New(context.Background(), cache, registry, &Config{CacheTTL: time.Hour, FetchTimeout: time.Second})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	doc, err := loader.GetByMetadata(context.Background(), model.ManifestMetadata{
		ManifestURL:               "https://example.org/manifest",
		ManifestSignatureURL:      "https://example.org/manifest.sig",
		SigningPublicKeyLookupURL: "https://example.org/pubkey",
	})
	if err != nil {
		t.Fatalf("GetByMetadata() error = %v", err)
	}
	if !doc.Verified {
		t.Fatal("expected manifest to be verified")
	}
	if string(doc.Content) != string(manifest) {
		t.Fatalf("unexpected manifest content: %q", string(doc.Content))
	}
	if len(cache.store) == 0 {
		t.Fatal("expected manifest to be cached")
	}
}

func TestGetByNetworkIDUsesCacheFirst(t *testing.T) {
	cache := &mockCache{store: map[string]string{
		networkCacheKey("nfo.example.org/network"): `{"network_id":"nfo.example.org/network","content":"bWFuaWZlc3Q=","verified":true}`,
	}}
	registry := &mockRegistry{}
	loader, _, err := New(context.Background(), cache, registry, &Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	doc, err := loader.GetByNetworkID(context.Background(), "nfo.example.org/network")
	if err != nil {
		t.Fatalf("GetByNetworkID() error = %v", err)
	}
	if registry.calls != 0 {
		t.Fatalf("expected no registry lookups on cache hit, got %d", registry.calls)
	}
	if doc.NetworkID != "nfo.example.org/network" {
		t.Fatalf("expected networkID to round-trip from cache")
	}
}

func TestGetByNetworkIDRejectsUnverifiedCacheEntry(t *testing.T) {
	cache := &mockCache{store: map[string]string{
		networkCacheKey("nfo.example.org/network"): `{"network_id":"nfo.example.org/network","content":"bWFuaWZlc3Q=","verified":false}`,
	}}
	registry := &mockRegistry{err: errors.New("should not resolve registry from poisoned cache in this test")}
	loader, _, err := New(context.Background(), cache, registry, &Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := loader.loadFromCache(context.Background(), networkCacheKey("nfo.example.org/network")); err == nil || !strings.Contains(err.Error(), "not marked verified") {
		t.Fatalf("expected unverified cache rejection, got %v", err)
	}
}

func TestGetByNetworkIDResolvesMetadata(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	manifest := []byte("hello")
	signature := ed25519.Sign(privateKey, manifest)

	originalHTTPClientFunc := httpClientFunc
	httpClientFunc = func(timeout time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://example.org/manifest":
				return response(200, string(manifest), "text/plain"), nil
			case "https://example.org/manifest.sig":
				return response(200, base64.StdEncoding.EncodeToString(signature), "text/plain"), nil
			case "https://example.org/pubkey":
				return response(200, `{"data":{"details":{"signing_public_key":"`+base64.StdEncoding.EncodeToString(publicKey)+`"}}}`, "application/json"), nil
			default:
				return response(404, "not found", "text/plain"), nil
			}
		})}
	}
	defer func() { httpClientFunc = originalHTTPClientFunc }()

	cache := &mockCache{store: map[string]string{}}
	registry := &mockRegistry{
		meta: &model.RegistryMetadata{
			NamespaceIdentifier: "nfo.example.org",
			RegistryName:        "network",
			RawMeta: map[string]string{
				"manifest_url":                  "https://example.org/manifest",
				"manifest_signature_url":        "https://example.org/manifest.sig",
				"signing_public_key_lookup_url": "https://example.org/pubkey",
			},
		},
	}
	loader, _, err := New(context.Background(), cache, registry, &Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	doc, err := loader.GetByNetworkID(context.Background(), "nfo.example.org/network")
	if err != nil {
		t.Fatalf("GetByNetworkID() error = %v", err)
	}
	if registry.calls != 1 {
		t.Fatalf("expected one registry lookup, got %d", registry.calls)
	}
	if doc.NetworkID != "nfo.example.org/network" {
		t.Fatalf("expected networkID to be set on returned document")
	}
	if _, ok := cache.store[networkCacheKey("nfo.example.org/network")]; !ok {
		t.Fatal("expected network-specific cache key to be populated")
	}
}

func TestGetByMetadata_DisableCacheBypassesAndDoesNotStore(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	manifest := []byte("fresh manifest")
	signature := ed25519.Sign(privateKey, manifest)
	requests := 0

	originalHTTPClientFunc := httpClientFunc
	httpClientFunc = func(timeout time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			switch req.URL.String() {
			case "https://example.org/manifest":
				return response(200, string(manifest), "application/yaml"), nil
			case "https://example.org/manifest.sig":
				return response(200, base64.StdEncoding.EncodeToString(signature), "text/plain"), nil
			case "https://example.org/pubkey":
				return response(200, base64.StdEncoding.EncodeToString(publicKey), "text/plain"), nil
			default:
				return response(404, "not found", "text/plain"), nil
			}
		})}
	}
	defer func() { httpClientFunc = originalHTTPClientFunc }()

	staleMetadata := model.ManifestMetadata{
		ManifestURL:               "https://example.org/manifest",
		ManifestSignatureURL:      "https://example.org/manifest.sig",
		SigningPublicKeyLookupURL: "https://example.org/pubkey",
	}
	cache := &mockCache{store: map[string]string{
		metadataCacheKey(staleMetadata): `{"content":"c3RhbGU=","verified":true}`,
	}}
	loader, _, err := New(context.Background(), cache, &mockRegistry{}, &Config{
		DisableCache: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	doc, err := loader.GetByMetadata(context.Background(), model.ManifestMetadata{
		ManifestURL:               "https://example.org/manifest",
		ManifestSignatureURL:      "https://example.org/manifest.sig",
		SigningPublicKeyLookupURL: "https://example.org/pubkey",
	})
	if err != nil {
		t.Fatalf("GetByMetadata() error = %v", err)
	}
	if string(doc.Content) != string(manifest) {
		t.Fatalf("expected fresh manifest content, got %q", string(doc.Content))
	}
	if requests != 3 {
		t.Fatalf("expected 3 remote fetches when cache disabled, got %d", requests)
	}
	if len(cache.store) != 1 {
		t.Fatalf("expected no new cache writes when cache disabled, got %d entries", len(cache.store))
	}
}

func TestGetByMetadata_ForceRefreshOnStartBypassesOnce(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	manifest := []byte("fresh manifest")
	signature := ed25519.Sign(privateKey, manifest)
	requests := 0

	originalHTTPClientFunc := httpClientFunc
	httpClientFunc = func(timeout time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			switch req.URL.String() {
			case "https://example.org/manifest":
				return response(200, string(manifest), "application/yaml"), nil
			case "https://example.org/manifest.sig":
				return response(200, base64.StdEncoding.EncodeToString(signature), "text/plain"), nil
			case "https://example.org/pubkey":
				return response(200, base64.StdEncoding.EncodeToString(publicKey), "text/plain"), nil
			default:
				return response(404, "not found", "text/plain"), nil
			}
		})}
	}
	defer func() { httpClientFunc = originalHTTPClientFunc }()

	metadata := model.ManifestMetadata{
		ManifestURL:               "https://example.org/manifest",
		ManifestSignatureURL:      "https://example.org/manifest.sig",
		SigningPublicKeyLookupURL: "https://example.org/pubkey",
	}
	cache := &mockCache{store: map[string]string{
		metadataCacheKey(metadata): `{"content":"c3RhbGU=","verified":true}`,
	}}
	loader, _, err := New(context.Background(), cache, &mockRegistry{}, &Config{
		ForceRefreshOnStart: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	doc, err := loader.GetByMetadata(context.Background(), metadata)
	if err != nil {
		t.Fatalf("first GetByMetadata() error = %v", err)
	}
	if string(doc.Content) != string(manifest) {
		t.Fatalf("expected fresh manifest content on startup refresh, got %q", string(doc.Content))
	}
	if requests != 3 {
		t.Fatalf("expected 3 remote fetches on first startup refresh, got %d", requests)
	}

	doc, err = loader.GetByMetadata(context.Background(), metadata)
	if err != nil {
		t.Fatalf("second GetByMetadata() error = %v", err)
	}
	if string(doc.Content) != string(manifest) {
		t.Fatalf("expected cached fresh manifest on second lookup, got %q", string(doc.Content))
	}
	if requests != 3 {
		t.Fatalf("expected second lookup to use cache after initial refresh, got %d remote fetches", requests)
	}
}

func TestGetByNetworkID_DisableCacheBypassesAndDoesNotStore(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	manifest := []byte("fresh manifest")
	signature := ed25519.Sign(privateKey, manifest)
	requests := 0

	originalHTTPClientFunc := httpClientFunc
	httpClientFunc = func(timeout time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			switch req.URL.String() {
			case "https://example.org/manifest":
				return response(200, string(manifest), "application/yaml"), nil
			case "https://example.org/manifest.sig":
				return response(200, base64.StdEncoding.EncodeToString(signature), "text/plain"), nil
			case "https://example.org/pubkey":
				return response(200, base64.StdEncoding.EncodeToString(publicKey), "text/plain"), nil
			default:
				return response(404, "not found", "text/plain"), nil
			}
		})}
	}
	defer func() { httpClientFunc = originalHTTPClientFunc }()

	staleMetadata2 := model.ManifestMetadata{
		ManifestURL:               "https://example.org/manifest",
		ManifestSignatureURL:      "https://example.org/manifest.sig",
		SigningPublicKeyLookupURL: "https://example.org/pubkey",
	}
	cache := &mockCache{store: map[string]string{
		networkCacheKey("nfo.example.org/network"):  `{"network_id":"nfo.example.org/network","content":"c3RhbGU=","verified":true}`,
		metadataCacheKey(staleMetadata2): `{"content":"c3RhbGU=","verified":true}`,
	}}
	registry := &mockRegistry{
		meta: &model.RegistryMetadata{
			NamespaceIdentifier: "nfo.example.org",
			RegistryName:        "network",
			RawMeta: map[string]string{
				"manifest_url":                  "https://example.org/manifest",
				"manifest_signature_url":        "https://example.org/manifest.sig",
				"signing_public_key_lookup_url": "https://example.org/pubkey",
			},
		},
	}
	loader, _, err := New(context.Background(), cache, registry, &Config{DisableCache: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	doc, err := loader.GetByNetworkID(context.Background(), "nfo.example.org/network")
	if err != nil {
		t.Fatalf("GetByNetworkID() error = %v", err)
	}
	if string(doc.Content) != string(manifest) {
		t.Fatalf("expected fresh manifest content, got %q", string(doc.Content))
	}
	if requests != 3 {
		t.Fatalf("expected 3 remote fetches when cache disabled, got %d", requests)
	}
	if registry.calls != 1 {
		t.Fatalf("expected one registry lookup when bypassing cache, got %d", registry.calls)
	}
}

func TestGetByNetworkID_ForceRefreshOnStartBypassesOnce(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	manifest := []byte("fresh manifest")
	signature := ed25519.Sign(privateKey, manifest)
	requests := 0

	originalHTTPClientFunc := httpClientFunc
	httpClientFunc = func(timeout time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			switch req.URL.String() {
			case "https://example.org/manifest":
				return response(200, string(manifest), "application/yaml"), nil
			case "https://example.org/manifest.sig":
				return response(200, base64.StdEncoding.EncodeToString(signature), "text/plain"), nil
			case "https://example.org/pubkey":
				return response(200, base64.StdEncoding.EncodeToString(publicKey), "text/plain"), nil
			default:
				return response(404, "not found", "text/plain"), nil
			}
		})}
	}
	defer func() { httpClientFunc = originalHTTPClientFunc }()

	metadata := model.ManifestMetadata{
		ManifestURL:               "https://example.org/manifest",
		ManifestSignatureURL:      "https://example.org/manifest.sig",
		SigningPublicKeyLookupURL: "https://example.org/pubkey",
	}
	cache := &mockCache{store: map[string]string{
		networkCacheKey("nfo.example.org/network"): `{"network_id":"nfo.example.org/network","content":"c3RhbGU=","verified":true}`,
		metadataCacheKey(metadata):                        `{"content":"c3RhbGU=","verified":true}`,
	}}
	registry := &mockRegistry{
		meta: &model.RegistryMetadata{
			NamespaceIdentifier: "nfo.example.org",
			RegistryName:        "network",
			RawMeta: map[string]string{
				"manifest_url":                  metadata.ManifestURL,
				"manifest_signature_url":        metadata.ManifestSignatureURL,
				"signing_public_key_lookup_url": metadata.SigningPublicKeyLookupURL,
			},
		},
	}
	loader, _, err := New(context.Background(), cache, registry, &Config{ForceRefreshOnStart: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	doc, err := loader.GetByNetworkID(context.Background(), "nfo.example.org/network")
	if err != nil {
		t.Fatalf("first GetByNetworkID() error = %v", err)
	}
	if string(doc.Content) != string(manifest) {
		t.Fatalf("expected fresh manifest content on startup refresh, got %q", string(doc.Content))
	}
	if requests != 3 {
		t.Fatalf("expected 3 remote fetches on first startup refresh, got %d", requests)
	}
	if registry.calls != 1 {
		t.Fatalf("expected one registry lookup on first startup refresh, got %d", registry.calls)
	}

	doc, err = loader.GetByNetworkID(context.Background(), "nfo.example.org/network")
	if err != nil {
		t.Fatalf("second GetByNetworkID() error = %v", err)
	}
	if string(doc.Content) != string(manifest) {
		t.Fatalf("expected cached fresh manifest on second lookup, got %q", string(doc.Content))
	}
	if requests != 3 {
		t.Fatalf("expected second lookup to use cache after initial refresh, got %d remote fetches", requests)
	}
	if registry.calls != 1 {
		t.Fatalf("expected second lookup to use network cache and avoid registry lookup, got %d", registry.calls)
	}
}

func TestFetchURL_RejectsOversizedResponse(t *testing.T) {
	loader, _, err := New(context.Background(), &mockCache{store: map[string]string{}}, &mockRegistry{}, &Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	originalHTTPClientFunc := httpClientFunc
	httpClientFunc = func(timeout time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return response(200, strings.Repeat("a", maxManifestArtifactSize+1), "application/yaml"), nil
		})}
	}
	defer func() { httpClientFunc = originalHTTPClientFunc }()
	loader.client = httpClientFunc(time.Second)

	if _, _, err := loader.fetchURL(context.Background(), "https://example.org/manifest"); err == nil || !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}
