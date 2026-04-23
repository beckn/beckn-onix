package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// noopPluginManager satisfies PluginManager with nil plugins (unused loaders are never invoked when config is omitted).
type noopPluginManager struct{}

func (noopPluginManager) Middleware(context.Context, *plugin.Config) (func(http.Handler) http.Handler, error) {
	return nil, nil
}
func (noopPluginManager) SignValidator(context.Context, *plugin.Config) (definition.SignValidator, error) {
	return nil, nil
}
func (noopPluginManager) Validator(context.Context, *plugin.Config) (definition.SchemaValidator, error) {
	return nil, nil
}
func (noopPluginManager) Router(context.Context, *plugin.Config) (definition.Router, error) {
	return nil, nil
}
func (noopPluginManager) Publisher(context.Context, *plugin.Config) (definition.Publisher, error) {
	return nil, nil
}
func (noopPluginManager) Signer(context.Context, *plugin.Config) (definition.Signer, error) {
	return nil, nil
}
func (noopPluginManager) Step(context.Context, *plugin.Config) (definition.Step, error) {
	return nil, nil
}
func (noopPluginManager) PolicyChecker(context.Context, definition.ManifestLoader, *plugin.Config) (definition.PolicyChecker, error) {
	return nil, nil
}
func (noopPluginManager) Cache(context.Context, *plugin.Config) (definition.Cache, error) {
	return nil, nil
}
func (noopPluginManager) Registry(context.Context, *plugin.Config) (definition.RegistryLookup, error) {
	return nil, nil
}
func (noopPluginManager) KeyManager(context.Context, definition.Cache, definition.RegistryLookup, *plugin.Config) (definition.KeyManager, error) {
	return nil, nil
}
func (noopPluginManager) ManifestLoader(context.Context, definition.Cache, definition.RegistryMetadataLookup, *plugin.Config) (definition.ManifestLoader, error) {
	return nil, nil
}
func (noopPluginManager) TransportWrapper(context.Context, *plugin.Config) (definition.TransportWrapper, error) {
	return nil, nil
}
func (noopPluginManager) SchemaValidator(context.Context, *plugin.Config) (definition.SchemaValidator, error) {
	return nil, nil
}

type registryWithoutMetadata struct{}

func (registryWithoutMetadata) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	return nil, errors.New("not implemented")
}

type stubCache struct{}

func (stubCache) Get(context.Context, string) (string, error)              { return "", errors.New("cache miss") }
func (stubCache) Set(context.Context, string, string, time.Duration) error { return nil }
func (stubCache) Delete(context.Context, string) error                     { return nil }
func (stubCache) Clear(context.Context) error                              { return nil }

func TestNewStdHandler_CheckPolicyStepWithoutPluginFails(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		Plugins: PluginCfg{},
		Steps:   []string{"checkPolicy"},
	}
	_, err := NewStdHandler(ctx, noopPluginManager{}, cfg, "testModule")
	if err == nil {
		t.Fatal("expected error when steps list checkPolicy but checkPolicy plugin is omitted")
	}
	if !strings.Contains(err.Error(), "failed to initialize steps") {
		t.Fatalf("expected steps init failure, got: %v", err)
	}
	if !strings.Contains(err.Error(), "PolicyChecker plugin not configured") {
		t.Fatalf("expected explicit PolicyChecker config error, got: %v", err)
	}
}

func TestLoadManifestLoader_RequiresCache(t *testing.T) {
	_, err := loadManifestLoader(context.Background(), noopPluginManager{}, nil, registryWithoutMetadata{}, &plugin.Config{ID: "manifestloader"})
	if err == nil || !strings.Contains(err.Error(), "Cache plugin not configured") {
		t.Fatalf("expected cache requirement error, got %v", err)
	}
}

func TestLoadManifestLoader_RequiresRegistry(t *testing.T) {
	_, err := loadManifestLoader(context.Background(), noopPluginManager{}, stubCache{}, nil, &plugin.Config{ID: "manifestloader"})
	if err == nil || !strings.Contains(err.Error(), "Registry plugin not configured") {
		t.Fatalf("expected registry requirement error, got %v", err)
	}
}

func TestLoadManifestLoader_RequiresRegistryMetadataLookup(t *testing.T) {
	_, err := loadManifestLoader(context.Background(), noopPluginManager{}, stubCache{}, registryWithoutMetadata{}, &plugin.Config{ID: "manifestloader"})
	if err == nil || !strings.Contains(err.Error(), "does not implement RegistryMetadataLookup") {
		t.Fatalf("expected RegistryMetadataLookup error, got %v", err)
	}
}

func TestNewHTTPClient(t *testing.T) {
	tests := []struct {
		name     string
		config   HttpClientConfig
		expected struct {
			maxIdleConns          int
			maxIdleConnsPerHost   int
			idleConnTimeout       time.Duration
			responseHeaderTimeout time.Duration
		}
	}{
		{
			name: "all values configured",
			config: HttpClientConfig{
				MaxIdleConns:          1000,
				MaxIdleConnsPerHost:   200,
				IdleConnTimeout:       300 * time.Second,
				ResponseHeaderTimeout: 5 * time.Second,
			},
			expected: struct {
				maxIdleConns          int
				maxIdleConnsPerHost   int
				idleConnTimeout       time.Duration
				responseHeaderTimeout time.Duration
			}{
				maxIdleConns:          1000,
				maxIdleConnsPerHost:   200,
				idleConnTimeout:       300 * time.Second,
				responseHeaderTimeout: 5 * time.Second,
			},
		},
		{
			name:   "zero values use defaults",
			config: HttpClientConfig{},
			expected: struct {
				maxIdleConns          int
				maxIdleConnsPerHost   int
				idleConnTimeout       time.Duration
				responseHeaderTimeout time.Duration
			}{
				maxIdleConns:          100, // Go default
				maxIdleConnsPerHost:   0,   // Go default (unlimited per host)
				idleConnTimeout:       90 * time.Second,
				responseHeaderTimeout: 0,
			},
		},
		{
			name: "partial configuration",
			config: HttpClientConfig{
				MaxIdleConns:    500,
				IdleConnTimeout: 180 * time.Second,
			},
			expected: struct {
				maxIdleConns          int
				maxIdleConnsPerHost   int
				idleConnTimeout       time.Duration
				responseHeaderTimeout time.Duration
			}{
				maxIdleConns:          500,
				maxIdleConnsPerHost:   0, // Go default (unlimited per host)
				idleConnTimeout:       180 * time.Second,
				responseHeaderTimeout: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newHTTPClient(&tt.config, nil)

			if client == nil {
				t.Fatal("newHTTPClient returned nil")
			}

			transport, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatal("client transport is not *http.Transport")
			}

			if transport.MaxIdleConns != tt.expected.maxIdleConns {
				t.Errorf("MaxIdleConns = %d, want %d", transport.MaxIdleConns, tt.expected.maxIdleConns)
			}

			if transport.MaxIdleConnsPerHost != tt.expected.maxIdleConnsPerHost {
				t.Errorf("MaxIdleConnsPerHost = %d, want %d", transport.MaxIdleConnsPerHost, tt.expected.maxIdleConnsPerHost)
			}

			if transport.IdleConnTimeout != tt.expected.idleConnTimeout {
				t.Errorf("IdleConnTimeout = %v, want %v", transport.IdleConnTimeout, tt.expected.idleConnTimeout)
			}

			if transport.ResponseHeaderTimeout != tt.expected.responseHeaderTimeout {
				t.Errorf("ResponseHeaderTimeout = %v, want %v", transport.ResponseHeaderTimeout, tt.expected.responseHeaderTimeout)
			}
		})
	}
}

func TestHttpClientConfigDefaults(t *testing.T) {
	// Test that zero config values don't override defaults
	config := &HttpClientConfig{}
	client := newHTTPClient(config, nil)

	transport := client.Transport.(*http.Transport)

	// Verify defaults are preserved when config values are zero
	if transport.MaxIdleConns == 0 {
		t.Error("MaxIdleConns should not be zero when using defaults")
	}

	// MaxIdleConnsPerHost default is 0 (unlimited), which is correct
	if transport.MaxIdleConns != 100 {
		t.Errorf("Expected default MaxIdleConns=100, got %d", transport.MaxIdleConns)
	}
}

func TestHttpClientConfigPerformanceValues(t *testing.T) {
	// Test the specific performance-optimized values from the document
	config := &HttpClientConfig{
		MaxIdleConns:          1000,
		MaxIdleConnsPerHost:   200,
		IdleConnTimeout:       300 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}

	client := newHTTPClient(config, nil)
	transport := client.Transport.(*http.Transport)

	// Verify performance-optimized values
	if transport.MaxIdleConns != 1000 {
		t.Errorf("Expected MaxIdleConns=1000, got %d", transport.MaxIdleConns)
	}

	if transport.MaxIdleConnsPerHost != 200 {
		t.Errorf("Expected MaxIdleConnsPerHost=200, got %d", transport.MaxIdleConnsPerHost)
	}

	if transport.IdleConnTimeout != 300*time.Second {
		t.Errorf("Expected IdleConnTimeout=300s, got %v", transport.IdleConnTimeout)
	}

	if transport.ResponseHeaderTimeout != 5*time.Second {
		t.Errorf("Expected ResponseHeaderTimeout=5s, got %v", transport.ResponseHeaderTimeout)
	}
}

func TestNewHTTPClientWithTransportWrapper(t *testing.T) {
	wrappedTransport := &mockRoundTripper{}
	wrapper := &mockTransportWrapper{
		returnTransport: wrappedTransport,
	}

	client := newHTTPClient(&HttpClientConfig{}, wrapper)

	if !wrapper.wrapCalled {
		t.Fatal("expected transport wrapper to be invoked")
	}

	if wrapper.wrappedTransport == nil {
		t.Fatal("expected base transport to be passed to wrapper")
	}

	if client.Transport != wrappedTransport {
		t.Errorf("expected client transport to use wrapper transport")
	}
}

type mockTransportWrapper struct {
	wrapCalled       bool
	wrappedTransport http.RoundTripper
	returnTransport  http.RoundTripper
}

func (m *mockTransportWrapper) Wrap(base http.RoundTripper) http.RoundTripper {
	m.wrapCalled = true
	m.wrappedTransport = base
	if m.returnTransport != nil {
		return m.returnTransport
	}
	return base
}

type mockRoundTripper struct{}

func (m *mockRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, nil
}
