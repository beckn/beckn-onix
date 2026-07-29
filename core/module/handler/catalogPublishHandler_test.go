package handler

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher/localstore"
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
func (fakeRegistry) LookupRegistry(context.Context, string, string) (*model.RegistryMetadata, error) {
	panic("unused")
}
func (fakeRegistry) LookupNode(context.Context, string) (*model.SubscriberRecord, error) {
	panic("unused")
}

// fakeManifestLoader returns a configurable manifest document (or error)
// for GetBySubscriberID; GetByNetworkID/GetByMetadata are unused here.
type fakeManifestLoader struct {
	doc            *model.ManifestDocument
	err            error
	lastSubscriber string
}

func (f *fakeManifestLoader) GetByNetworkID(context.Context, string) (*model.ManifestDocument, error) {
	panic("unused")
}
func (f *fakeManifestLoader) GetByMetadata(context.Context, model.ManifestMetadata) (*model.ManifestDocument, error) {
	panic("unused")
}
func (f *fakeManifestLoader) GetBySubscriberID(_ context.Context, subscriberID string) (*model.ManifestDocument, error) {
	f.lastSubscriber = subscriberID
	return f.doc, f.err
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
	km              *fakeKeyManager
	publisher       definition.CatalogPublisher
	schemaValidator definition.SchemaValidator
	policyChecker   definition.PolicyChecker
	manifestLoader  definition.ManifestLoader
}

// fakeSchemaValidator records the last payload it was asked to validate and
// returns a configurable error.
type fakeSchemaValidator struct {
	err       error
	lastBody  []byte
	callCount int
}

func (f *fakeSchemaValidator) Validate(_ context.Context, _ *url.URL, data []byte) error {
	f.callCount++
	f.lastBody = data
	return f.err
}

// fakePolicyChecker records the last StepContext.Body it was asked to check
// and returns a configurable error.
type fakePolicyChecker struct {
	err       error
	lastBody  []byte
	callCount int
}

func (f *fakePolicyChecker) CheckPolicy(ctx *model.StepContext) error {
	f.callCount++
	f.lastBody = ctx.Body
	return f.err
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
	if m.policyChecker == nil {
		panic("unused")
	}
	return m.policyChecker, nil
}
func (m *catalogPublishTestManager) SchemaVersionMediator(context.Context, definition.ManifestLoader, *plugin.Config) (definition.SchemaVersionMediator, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) ManifestLoader(context.Context, definition.Cache, definition.RegistryMetadataLookup, *plugin.Config) (definition.ManifestLoader, error) {
	if m.manifestLoader == nil {
		panic("unused")
	}
	return m.manifestLoader, nil
}
func (m *catalogPublishTestManager) TransportWrapper(context.Context, *plugin.Config) (definition.TransportWrapper, error) {
	panic("unused")
}
func (m *catalogPublishTestManager) SchemaValidator(context.Context, *plugin.Config) (definition.SchemaValidator, error) {
	if m.schemaValidator == nil {
		panic("unused")
	}
	return m.schemaValidator, nil
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
			KeyManager:       &plugin.Config{ID: "keymanager", Config: map[string]string{"subscriberId": "example.test"}},
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

func TestNewCatalogPublishHandler_DerivesSubscriberIDFromKeyManager(t *testing.T) {
	var captured *plugin.Config
	mgr := newTestManager(t)
	mgr2 := &capturingCatalogPublisherManager{catalogPublishTestManager: mgr, captured: &captured}

	cfg := newTestConfig(t.TempDir()) // catalogPublisher.Config has no subscriberId of its own
	if _, err := NewCatalogPublishHandler(context.Background(), mgr2, cfg, "test"); err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}
	if captured == nil || captured.Config["subscriberId"] != "example.test" {
		t.Fatalf("expected subscriberId derived from keyManager config, got %+v", captured)
	}
}

func TestNewCatalogPublishHandler_ExplicitSubscriberIDIsNotOverridden(t *testing.T) {
	var captured *plugin.Config
	mgr := newTestManager(t)
	mgr2 := &capturingCatalogPublisherManager{catalogPublishTestManager: mgr, captured: &captured}

	cfg := newTestConfig(t.TempDir())
	cfg.Plugins.CatalogPublisher.Config = map[string]string{"subscriberId": "explicit.test"}
	if _, err := NewCatalogPublishHandler(context.Background(), mgr2, cfg, "test"); err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}
	if captured == nil || captured.Config["subscriberId"] != "explicit.test" {
		t.Fatalf("expected explicit subscriberId to be left untouched, got %+v", captured)
	}
}

func TestNewCatalogPublishHandler_RequiresKeyManagerSubscriberID(t *testing.T) {
	cfg := newTestConfig(t.TempDir())
	cfg.Plugins.KeyManager.Config = nil // no subscriberId to derive from
	_, err := NewCatalogPublishHandler(context.Background(), newTestManager(t), cfg, "test")
	if err == nil {
		t.Fatal("expected error when keyManager config has no subscriberId to derive from")
	}
}

// capturingCatalogPublisherManager wraps catalogPublishTestManager to record
// the *plugin.Config NewCatalogPublishHandler actually passes to
// mgr.CatalogPublisher, so tests can assert on the derived/merged config
// rather than just on success/failure.
type capturingCatalogPublisherManager struct {
	*catalogPublishTestManager
	captured **plugin.Config
}

func (m *capturingCatalogPublisherManager) CatalogPublisher(ctx context.Context, km definition.KeyManager, cfg *plugin.Config) (definition.CatalogPublisher, error) {
	*m.captured = cfg
	return m.catalogPublishTestManager.CatalogPublisher(ctx, km, cfg)
}

func TestCatalogPublishHandler_PublishesAndWritesToOutputRoot(t *testing.T) {
	root := t.TempDir()
	h, err := NewCatalogPublishHandler(context.Background(), newTestManager(t), newTestConfig(root), "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]}}`
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

	// The manifest (.well-known/dedi.index.json) is deliberately not
	// written by localstore right now (see localstore.Write) -- this
	// asserts it stays untouched, not just unwritten on a fresh root.
	if _, err := os.Stat(root + "/.well-known"); !os.IsNotExist(err) {
		t.Errorf("expected .well-known/ not created, got err=%v", err)
	}
	if _, err := os.Stat(root + "/index/becknCatalogs.index.json"); err != nil {
		t.Errorf("expected index written: %v", err)
	}
}

func TestCatalogPublishHandler_PublishDirectivesVisibleToMapsToNetworkIds(t *testing.T) {
	root := t.TempDir()
	h, err := NewCatalogPublishHandler(context.Background(), newTestManager(t), newTestConfig(root), "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}],
		"publishDirectives":[{"catalogId":"example.test/CAT-1","visibleTo":["retail-network","mobility-network"]}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	raw, err := os.ReadFile(localstore.IndexPath(root))
	if err != nil {
		t.Fatalf("reading index: %v", err)
	}
	var index struct {
		Catalogs []struct {
			CatalogID  string   `json:"catalogId"`
			NetworkIds []string `json:"networkIds"`
		} `json:"catalogs"`
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	if len(index.Catalogs) != 1 || len(index.Catalogs[0].NetworkIds) != 2 ||
		index.Catalogs[0].NetworkIds[0] != "retail-network" || index.Catalogs[0].NetworkIds[1] != "mobility-network" {
		t.Errorf("unexpected networkIds in index: %+v", index.Catalogs)
	}
}

func TestCatalogPublishHandler_RunsSchemaValidatorAndPolicyCheckerOnEnvelope(t *testing.T) {
	root := t.TempDir()
	schemaValidator := &fakeSchemaValidator{}
	policyChecker := &fakePolicyChecker{}
	mgr := newTestManager(t)
	mgr.schemaValidator = schemaValidator
	mgr.policyChecker = policyChecker

	cfg := newTestConfig(root)
	cfg.Plugins.SchemaValidator = &plugin.Config{ID: "schemav2validator"}
	cfg.Plugins.PolicyChecker = &plugin.Config{ID: "opapolicychecker"}
	h, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if schemaValidator.callCount != 1 {
		t.Fatalf("expected schemaValidator.Validate called once, got %d", schemaValidator.callCount)
	}
	if policyChecker.callCount != 1 {
		t.Fatalf("expected policyChecker.CheckPolicy called once, got %d", policyChecker.callCount)
	}

	var envelope struct {
		Context struct {
			Action string `json:"action"`
		} `json:"context"`
		Message struct {
			Catalogs []json.RawMessage `json:"catalogs"`
		} `json:"message"`
	}
	if err := json.Unmarshal(schemaValidator.lastBody, &envelope); err != nil {
		t.Fatalf("parsing envelope passed to schemaValidator: %v", err)
	}
	if envelope.Context.Action != "catalog/publish" {
		t.Errorf("envelope context.action = %q, want catalog/publish", envelope.Context.Action)
	}
	if len(envelope.Message.Catalogs) != 1 {
		t.Fatalf("expected 1 catalog in envelope message.catalogs, got %d", len(envelope.Message.Catalogs))
	}
	if string(schemaValidator.lastBody) != string(policyChecker.lastBody) {
		t.Errorf("schemaValidator and policyChecker were not given the same envelope")
	}
}

func TestCatalogPublishHandler_RejectsRequestWhenSchemaValidationFails(t *testing.T) {
	root := t.TempDir()
	schemaValidator := &fakeSchemaValidator{err: fmt.Errorf("schema mismatch")}
	mgr := newTestManager(t)
	mgr.schemaValidator = schemaValidator

	cfg := newTestConfig(root)
	cfg.Plugins.SchemaValidator = &plugin.Config{ID: "schemav2validator"}
	h, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(root + "/index/becknCatalogs.index.json"); err == nil {
		t.Error("expected no index written when schema validation fails")
	}
}

func TestCatalogPublishHandler_RejectsRequestWhenPolicyCheckFails(t *testing.T) {
	root := t.TempDir()
	policyChecker := &fakePolicyChecker{err: fmt.Errorf("policy violation")}
	mgr := newTestManager(t)
	mgr.policyChecker = policyChecker

	cfg := newTestConfig(root)
	cfg.Plugins.PolicyChecker = &plugin.Config{ID: "opapolicychecker"}
	h, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCatalogPublishHandler_SkipsValidationWhenNotConfigured(t *testing.T) {
	root := t.TempDir()
	h, err := NewCatalogPublishHandler(context.Background(), newTestManager(t), newTestConfig(root), "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with no validators configured, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCatalogPublishHandler_SkipsValidationForRetireOnlyRequest(t *testing.T) {
	root := t.TempDir()
	schemaValidator := &fakeSchemaValidator{}
	mgr := newTestManager(t)
	mgr.schemaValidator = schemaValidator

	cfg := newTestConfig(root)
	cfg.Plugins.SchemaValidator = &plugin.Config{ID: "schemav2validator"}
	h, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"retire":["example.test/CAT-GONE"]}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if schemaValidator.callCount != 0 {
		t.Errorf("expected schemaValidator not called for a retire-only request, got %d calls", schemaValidator.callCount)
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
	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"descriptor":{"name":"missing id"}}]}}`
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

func TestCatalogPublishHandler_StagesNodeManifestWhenIndexNotLinked(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	mgr.manifestLoader = &fakeManifestLoader{err: fmt.Errorf("subscriber has not published a node manifest")}

	cfg := newTestConfig(root)
	cfg.Plugins.ManifestLoader = &plugin.Config{ID: "manifestloader"}
	h, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]}}`
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
	if len(resp.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %+v", resp.Warnings)
	}

	staged, err := os.ReadFile(localstore.StagedNodeManifestPath(root))
	if err != nil {
		t.Fatalf("expected staged node manifest written: %v", err)
	}
	var nm model.NodeManifest
	if err := yaml.Unmarshal(staged, &nm); err != nil {
		t.Fatalf("parsing staged manifest: %v", err)
	}
	if len(nm.Catalog.CatalogIndexes) != 1 || nm.Catalog.CatalogIndexes[0].URL != "pending-artifact-store://catalog-index.json" {
		t.Errorf("unexpected staged catalogIndexes: %+v", nm.Catalog.CatalogIndexes)
	}
}

func TestCatalogPublishHandler_NoWarningWhenIndexAlreadyLinked(t *testing.T) {
	root := t.TempDir()
	existing := &model.NodeManifest{
		ManifestVersion: "1.0",
		ManifestType:    model.NodeManifestType,
		SubscriberID:    "example.test",
		Catalog: model.NodeManifestCatalog{
			CatalogIndexes: []model.CatalogIndexEntry{{URL: "pending-artifact-store://catalog-index.json"}},
		},
	}
	raw, err := yaml.Marshal(existing)
	if err != nil {
		t.Fatalf("marshaling fixture manifest: %v", err)
	}
	mgr := newTestManager(t)
	mgr.manifestLoader = &fakeManifestLoader{doc: &model.ManifestDocument{Content: raw}}

	cfg := newTestConfig(root)
	cfg.Plugins.ManifestLoader = &plugin.Config{ID: "manifestloader"}
	h, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp publishResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("expected no warnings, got %+v", resp.Warnings)
	}
	if _, err := os.Stat(localstore.StagedNodeManifestPath(root)); !os.IsNotExist(err) {
		t.Errorf("expected no staged manifest written, got err=%v", err)
	}
}

func TestCatalogPublishHandler_ManifestSubscriberIdOverridesSubscriberIdForManifestLookup(t *testing.T) {
	root := t.TempDir()
	loader := &fakeManifestLoader{}
	mgr := newTestManager(t)
	mgr.manifestLoader = loader

	cfg := newTestConfig(root)
	cfg.Plugins.ManifestLoader = &plugin.Config{ID: "manifestloader"}
	cfg.Plugins.CatalogPublisher.Config = map[string]string{
		"manifestSubscriberId": "nfh.global/staging-nodes/staging-p-node",
	}
	// existing NodeManifest fixture, keyed under the 3-part identity, with
	// the catalogIndex link already present, so no warning/staged file is
	// produced -- proving the handler looked it up using
	// manifestSubscriberId, not subscriberId ("example.test", which would
	// never match this document if used).
	nm := &model.NodeManifest{
		ManifestVersion: "1.0",
		ManifestType:    model.NodeManifestType,
		SubscriberID:    "nfh.global/staging-nodes/staging-p-node",
		Catalog: model.NodeManifestCatalog{
			CatalogIndexes: []model.CatalogIndexEntry{{URL: "pending-artifact-store://catalog-index.json"}},
		},
	}
	raw, err := yaml.Marshal(nm)
	if err != nil {
		t.Fatalf("marshaling fixture manifest: %v", err)
	}
	loader.doc = &model.ManifestDocument{Content: raw}

	h, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp publishResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("expected no warnings (manifest lookup should have used manifestSubscriberId), got %+v", resp.Warnings)
	}
	if loader.lastSubscriber != "nfh.global/staging-nodes/staging-p-node" {
		t.Errorf("GetBySubscriberID called with %q, want manifestSubscriberId value", loader.lastSubscriber)
	}
}

// TestCatalogPublishHandler_ManifestFetchErrorDoesNotProduceFalseWarning
// guards against the bug this handler previously had: any manifest fetch
// failure (bad subscriberID format, network error, ...) was silently
// treated the same as "no manifest published yet", producing a staged
// proposal and a "not declared" warning even when the real manifest
// already declared the index. Now, a fetch error that isn't specifically
// "no manifest published" is surfaced via a log only -- no warning, no
// staged file, since the check is inconclusive, not negative.
func TestCatalogPublishHandler_ManifestFetchErrorDoesNotProduceFalseWarning(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	mgr.manifestLoader = &fakeManifestLoader{err: fmt.Errorf(`subscriberID "example.test" must be in namespace/registry/recordName format`)}

	cfg := newTestConfig(root)
	cfg.Plugins.ManifestLoader = &plugin.Config{ID: "manifestloader"}
	h, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp publishResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("expected no warnings on an inconclusive manifest check, got %+v", resp.Warnings)
	}
	if _, err := os.Stat(localstore.StagedNodeManifestPath(root)); !os.IsNotExist(err) {
		t.Errorf("expected no staged manifest written, got err=%v", err)
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
