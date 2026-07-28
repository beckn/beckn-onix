package handler

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher"
)

// fakeKeyManager returns a fixed Ed25519 keyset for any keyID.
type fakeKeyManager struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func (f *fakeKeyManager) GenerateKeyset() (*model.Keyset, error) { return nil, nil }
func (f *fakeKeyManager) InsertKeyset(context.Context, string, *model.Keyset) error {
	return nil
}
func (f *fakeKeyManager) Keyset(context.Context, string) (*model.Keyset, error) {
	return &model.Keyset{
		SigningPrivate: base64.StdEncoding.EncodeToString(f.priv.Seed()),
		SigningPublic:  base64.StdEncoding.EncodeToString(f.pub),
	}, nil
}
func (f *fakeKeyManager) LookupNPKeys(context.Context, string, string) (string, string, error) {
	return "", "", nil
}
func (f *fakeKeyManager) DeleteKeyset(context.Context, string) error { return nil }

type fakeRegistry struct{}

func (fakeRegistry) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	return nil, nil
}

type fakeCache struct{}

func (fakeCache) Get(context.Context, string) (string, error) { return "", nil }
func (fakeCache) Set(context.Context, string, string, time.Duration) error {
	return nil
}
func (fakeCache) Delete(context.Context, string) error { return nil }
func (fakeCache) Clear(context.Context) error          { return nil }

// catalogPublishTestManager is a minimal PluginManager for exercising
// NewCatalogPublishHandler: only Cache/Registry/KeyManager/CatalogPublisher
// are ever called by it, every other method is unreachable and panics if
// invoked.
type catalogPublishTestManager struct {
	km        *fakeKeyManager
	publisher definition.CatalogPublisher
}

func (m *catalogPublishTestManager) Cache(context.Context, *plugin.Config) (definition.Cache, error) {
	return fakeCache{}, nil
}
func (m *catalogPublishTestManager) Registry(context.Context, definition.Cache, *plugin.Config) (definition.RegistryLookup, error) {
	return fakeRegistry{}, nil
}
func (m *catalogPublishTestManager) KeyManager(context.Context, definition.RegistryLookup, *plugin.Config) (definition.KeyManager, error) {
	return m.km, nil
}
func (m *catalogPublishTestManager) CatalogPublisher(context.Context, definition.KeyManager, *plugin.Config) (definition.CatalogPublisher, error) {
	return m.publisher, nil
}
func (m *catalogPublishTestManager) Middleware(context.Context, *plugin.Config) (func(http.Handler) http.Handler, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) SignValidator(context.Context, *plugin.Config) (definition.SignValidator, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) Validator(context.Context, *plugin.Config) (definition.SchemaValidator, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) Router(context.Context, *plugin.Config) (definition.Router, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) Publisher(context.Context, *plugin.Config) (definition.Publisher, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) Signer(context.Context, *plugin.Config) (definition.Signer, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) Step(context.Context, *plugin.Config) (definition.Step, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) PolicyChecker(context.Context, definition.ManifestLoader, *plugin.Config) (definition.PolicyChecker, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) SchemaVersionMediator(context.Context, definition.ManifestLoader, *plugin.Config) (definition.SchemaVersionMediator, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) ManifestLoader(context.Context, definition.Cache, definition.RegistryMetadataLookup, *plugin.Config) (definition.ManifestLoader, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) TransportWrapper(context.Context, *plugin.Config) (definition.TransportWrapper, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) SchemaValidator(context.Context, *plugin.Config) (definition.SchemaValidator, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) PayloadStore(context.Context, definition.Cache, string, *plugin.Config) (definition.PayloadStore, error) {
	panic("unused")
}

func newTestManager(t *testing.T) *catalogPublishTestManager {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	km := &fakeKeyManager{priv: priv, pub: pub}
	publisher, _, err := catalogpublisher.New(context.Background(), km, &catalogpublisher.Config{
		SubscriberID: "k1",
	})
	if err != nil {
		t.Fatalf("catalogpublisher.New: %v", err)
	}
	return &catalogPublishTestManager{km: km, publisher: publisher}
}

func newTestConfig(outputRoot string) *Config {
	return &Config{
		OutputRoot: outputRoot,
		Plugins: PluginCfg{
			Cache:            &plugin.Config{ID: "cache"},
			Registry:         &plugin.Config{ID: "registry"},
			KeyManager:       &plugin.Config{ID: "keymanager"},
			CatalogPublisher: &plugin.Config{ID: "catalogpublisher"},
		},
	}
}

func TestNewCatalogPublishHandler_RequiresOutputRoot(t *testing.T) {
	_, err := NewCatalogPublishHandler(context.Background(), newTestManager(t), &Config{
		Plugins: PluginCfg{CatalogPublisher: &plugin.Config{ID: "x"}},
	}, "test")
	if err == nil {
		t.Fatal("expected error for missing outputRoot")
	}
}

func TestNewCatalogPublishHandler_RequiresCatalogPublisherPlugin(t *testing.T) {
	cfg := newTestConfig(t.TempDir())
	cfg.Plugins.CatalogPublisher = nil
	_, err := NewCatalogPublishHandler(context.Background(), newTestManager(t), cfg, "test")
	if err == nil {
		t.Fatal("expected error for missing catalogPublisher plugin config")
	}
}

func TestCatalogPublishHandler_PublishesAndWritesToOutputRoot(t *testing.T) {
	root := t.TempDir()
	h, err := NewCatalogPublishHandler(context.Background(), newTestManager(t), newTestConfig(root), "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp publishResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}
	if resp.Status != publishOverallCompleted {
		t.Fatalf("expected COMPLETED, got %+v", resp)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != catalogAccepted || resp.Results[0].CatalogID != "example.test/CAT-1" {
		t.Fatalf("unexpected results: %+v", resp.Results)
	}

	if _, err := os.Stat(root + "/.well-known/dedi.index.json"); err != nil {
		t.Errorf("expected manifest written: %v", err)
	}
	if _, err := os.Stat(root + "/dedi/becknCatalogs.index.json"); err != nil {
		t.Errorf("expected index written: %v", err)
	}
}

func TestCatalogPublishHandler_RejectsInvalidBody(t *testing.T) {
	h, err := NewCatalogPublishHandler(context.Background(), newTestManager(t), newTestConfig(t.TempDir()), "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestCatalogPublishHandler_RejectsEmptyRequest(t *testing.T) {
	h, err := NewCatalogPublishHandler(context.Background(), newTestManager(t), newTestConfig(t.TempDir()), "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestCatalogPublishHandler_InvalidCatalogIsRejectedNotFatal(t *testing.T) {
	h, err := NewCatalogPublishHandler(context.Background(), newTestManager(t), newTestConfig(t.TempDir()), "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}
	body := `{"catalogs":[{"descriptor":{"name":"missing id"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (non-fatal), got %d: %s", rec.Code, rec.Body.String())
	}
	var resp publishResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}
	if resp.Status != publishOverallCompleted {
		t.Fatalf("expected top-level COMPLETED even with a rejected catalog, got %+v", resp)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != catalogRejected {
		t.Fatalf("expected 1 rejected result, got %+v", resp.Results)
	}
}

func TestCatalogPublishHandler_Retire(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	h, err := NewCatalogPublishHandler(context.Background(), mgr, newTestConfig(root), "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(`{"retire":["example.test/CAT-OLD"]}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp publishResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != catalogAccepted || resp.Results[0].CatalogID != "example.test/CAT-OLD" {
		t.Fatalf("unexpected retire result: %+v", resp.Results)
	}
}

func TestCatalogPublishHandler_MethodNotAllowed(t *testing.T) {
	h, err := NewCatalogPublishHandler(context.Background(), newTestManager(t), newTestConfig(t.TempDir()), "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/catalog/publish", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
