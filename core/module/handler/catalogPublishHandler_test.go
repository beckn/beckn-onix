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

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher/localstore"
)

// fakeKeyManager returns a fixed Ed25519 keyset (and keyID) for any subscriberID.
type fakeKeyManager struct {
	priv  ed25519.PrivateKey
	pub   ed25519.PublicKey
	keyID string
}

func (f *fakeKeyManager) GenerateKeyset() (*model.Keyset, error) { return nil, nil }
func (f *fakeKeyManager) InsertKeyset(context.Context, string, *model.Keyset) error {
	return nil
}
func (f *fakeKeyManager) Keyset(context.Context, string) (*model.Keyset, error) {
	return &model.Keyset{
		SigningPrivate: base64.StdEncoding.EncodeToString(f.priv.Seed()),
		SigningPublic:  base64.StdEncoding.EncodeToString(f.pub),
		UniqueKeyID:    f.keyID,
	}, nil
}
func (f *fakeKeyManager) LookupNPKeys(context.Context, string, string) (string, string, error) {
	return "", "", nil
}
func (f *fakeKeyManager) DeleteKeyset(context.Context, string) error { return nil }

// fakeRegistry is a configurable RegistryLookup + RegistryMetadataLookup
// double. LookupNode returns nodeRecord/nodeErr and records the nodeID it
// was called with (lastNodeID) -- the handler calls it with a synthetic
// subscriberID/dediSubscriberWildcardRegistry/keyID path (see
// catalogPublishHandler.go's checkRegistryLinksCatalogIndex).
type fakeRegistry struct {
	nodeRecord *model.SubscriberRecord
	nodeErr    error
	lastNodeID string
}

func (*fakeRegistry) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	return nil, nil
}
func (*fakeRegistry) LookupRegistry(context.Context, string, string) (*model.RegistryMetadata, error) {
	panic("unused")
}
func (f *fakeRegistry) LookupNode(_ context.Context, nodeID string) (*model.SubscriberRecord, error) {
	f.lastNodeID = nodeID
	return f.nodeRecord, f.nodeErr
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
	registry        *fakeRegistry
	publisher       definition.CatalogPublisher
	schemaValidator definition.SchemaValidator
	policyChecker   definition.PolicyChecker
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
	return m.registry, nil
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
func (m *catalogPublishTestManager) PolicyChecker(_ context.Context, _ definition.ManifestLoader, _ *plugin.Config) (definition.PolicyChecker, error) {
	if m.policyChecker == nil {
		panic("unused")
	}
	return m.policyChecker, nil
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
	km := &fakeKeyManager{priv: priv, pub: pub, keyID: "test-key-1"}
	publisher, _, err := catalogpublisher.New(context.Background(), km, &catalogpublisher.Config{
		SubscriberID: "k1",
	})
	if err != nil {
		t.Fatalf("catalogpublisher.New: %v", err)
	}
	return &catalogPublishTestManager{km: km, registry: &fakeRegistry{}, publisher: publisher}
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

func TestNewCatalogPublishHandler_RejectsSlashInSubscriberIDWhenCatalogIndexLinkCheckEnabled(t *testing.T) {
	cfg := newTestConfig(t.TempDir())
	cfg.Plugins.CatalogPublisher.Config = map[string]string{"checkCatalogIndexLink": "true"}
	cfg.Plugins.KeyManager.Config = map[string]string{"subscriberId": "nfh.global/bpp.example.com"}
	mgr := newTestManager(t)

	_, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err == nil {
		t.Fatal("expected error for a subscriberId containing \"/\" when the catalog-index link check is enabled -- it would silently produce a malformed registry self-lookup path on every check")
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

// fakeFatalPublisher always returns one Fatal PublishError and no successful
// catalog outcomes, to exercise the handler's Fatal -> overall status wiring.
type fakeFatalPublisher struct{}

func (fakeFatalPublisher) Publish(context.Context, definition.PublishRequest) (definition.PublishResult, error) {
	return definition.PublishResult{
		Errors: []definition.PublishError{{CatalogID: "example.test/CAT-1", Stage: "sign", Reason: "signing key unavailable", Fatal: true}},
	}, nil
}
func (fakeFatalPublisher) IndexURL() string { return "pending-artifact-store://catalog-index.json" }

func TestCatalogPublishHandler_FatalPublishErrorReportsOverallFailed(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	mgr.publisher = fakeFatalPublisher{}

	h, err := NewCatalogPublishHandler(context.Background(), mgr, newTestConfig(root), "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (Fatal is still reported in the body, not a transport error), got %d: %s", rec.Code, rec.Body.String())
	}
	var resp publishResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}
	if resp.Status != publishOverallFailed {
		t.Fatalf("expected overall status FAILED when a PublishError is Fatal, got %+v", resp)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != catalogRejected {
		t.Fatalf("expected 1 rejected result, got %+v", resp.Results)
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

func TestCatalogPublishHandler_SubmittedAndRetiredSameCatalogID_ReportsOnlyAccepted(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	h, err := NewCatalogPublishHandler(context.Background(), mgr, newTestConfig(root), "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}]},"retire":["example.test/CAT-1"]}`
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
	// Publish's own rule is "submission wins" when a catalogId is both
	// submitted and retired in one call, so exactly one result should be
	// reported for it -- the real publish outcome, not a second spurious
	// "retired" entry for a tombstone that was never actually written.
	if len(resp.Results) != 1 {
		t.Fatalf("expected exactly 1 result for a catalogId that was both submitted and retired, got %+v", resp.Results)
	}
	if resp.Results[0].CatalogID != "example.test/CAT-1" || resp.Results[0].Status != catalogAccepted || resp.Results[0].Reason == "retired" {
		t.Fatalf("expected the real publish outcome, not a spurious retired result, got %+v", resp.Results[0])
	}
}

// TestCatalogPublishHandler_WarnsWhenRegistryDoesNotLinkIndex replaces the
// old staged-node-manifest test: staging a proposed manifest update no
// longer applies now that the catalog index is declared directly in the
// DeDi record's own meta.catalog_index_url instead of via a node-manifest
// document -- there is nothing to stage locally either way, since
// dediregistry has no write path (getting a value into a DeDi record's
// meta is, and remains, an external, manual operator action).
func TestCatalogPublishHandler_WarnsWhenRegistryDoesNotLinkIndex(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	mgr.registry.nodeRecord = &model.SubscriberRecord{Meta: map[string]string{}}

	cfg := newTestConfig(root)
	cfg.Plugins.CatalogPublisher.Config = map[string]string{"checkCatalogIndexLink": "true"}
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
}

func TestCatalogPublishHandler_NoWarningWhenIndexAlreadyLinked(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	mgr.registry.nodeRecord = &model.SubscriberRecord{
		MetaArrays: map[string][]string{catalogIndexMetaKey: {"pending-artifact-store://catalog-index.json"}},
	}

	cfg := newTestConfig(root)
	cfg.Plugins.CatalogPublisher.Config = map[string]string{"checkCatalogIndexLink": "true"}
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
}

// TestCatalogPublishHandler_RegistryLookupUsesSubscriberIDAndDerivedKeyID
// replaces the old manifestSubscriberId-override test: that config field no
// longer exists. The registry self-lookup resolves keyID fresh on every
// check from keyManager.Keyset(subscriberID) -- the same keyset
// catalogpublisher.Publish signs with -- and calls LookupNode with a
// synthetic subscriberID/dediSubscriberWildcardRegistry/keyID path built
// from those two values, instead of requiring a hand-configured three-part
// DeDi path. Verified directly against a real DeDi registry that this
// addressing resolves to the identical record a real
// namespace/registry/recordName lookup would.
func TestCatalogPublishHandler_RegistryLookupUsesSubscriberIDAndDerivedKeyID(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	mgr.registry.nodeRecord = &model.SubscriberRecord{
		MetaArrays: map[string][]string{catalogIndexMetaKey: {"pending-artifact-store://catalog-index.json"}},
	}

	cfg := newTestConfig(root)
	cfg.Plugins.CatalogPublisher.Config = map[string]string{"checkCatalogIndexLink": "true"}
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
	wantNodeID := "example.test/" + dediSubscriberWildcardRegistry + "/test-key-1"
	if mgr.registry.lastNodeID != wantNodeID {
		t.Errorf("LookupNode called with %q, want synthetic path %q", mgr.registry.lastNodeID, wantNodeID)
	}
}

func TestCatalogPublishHandler_EmptyKeyIDFailsRegistryCheckLoudly(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	mgr.km.keyID = "" // keyset has no keyId -- can't build the synthetic lookup path
	mgr.registry.nodeRecord = &model.SubscriberRecord{}

	cfg := newTestConfig(root)
	cfg.Plugins.CatalogPublisher.Config = map[string]string{"checkCatalogIndexLink": "true"}
	h, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	warning, checkErr := h.(*catalogPublishHandler).checkRegistryLinksCatalogIndex(context.Background())
	if checkErr == nil {
		t.Fatalf("expected an error for an empty keyId, got warning=%q", warning)
	}
}

func TestCatalogPublishHandler_KeyIDWithSlashFailsRegistryCheckLoudly(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	mgr.km.keyID = "has/a/slash"
	mgr.registry.nodeRecord = &model.SubscriberRecord{}

	cfg := newTestConfig(root)
	cfg.Plugins.CatalogPublisher.Config = map[string]string{"checkCatalogIndexLink": "true"}
	h, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	if _, checkErr := h.(*catalogPublishHandler).checkRegistryLinksCatalogIndex(context.Background()); checkErr == nil {
		t.Fatal("expected an error for a keyId containing \"/\"")
	}
}

// TestCatalogPublishHandler_RegistryLookupPicksUpKeyRotation guards against
// the bug this handler previously had: keyID used to be resolved once at
// construction and cached, so a signing-key rotation after startup would
// leave the registry self-lookup silently querying a stale keyID that no
// longer matches what catalogpublisher.Publish actually signs with. keyID
// is now re-resolved from KeyManager on every check.
func TestCatalogPublishHandler_RegistryLookupPicksUpKeyRotation(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	mgr.registry.nodeRecord = &model.SubscriberRecord{}

	cfg := newTestConfig(root)
	cfg.Plugins.CatalogPublisher.Config = map[string]string{"checkCatalogIndexLink": "true"}
	h, err := NewCatalogPublishHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewCatalogPublishHandler: %v", err)
	}

	if _, err := h.(*catalogPublishHandler).checkRegistryLinksCatalogIndex(context.Background()); err != nil {
		t.Fatalf("checkRegistryLinksCatalogIndex: %v", err)
	}
	wantBefore := "example.test/" + dediSubscriberWildcardRegistry + "/test-key-1"
	if mgr.registry.lastNodeID != wantBefore {
		t.Fatalf("before rotation: LookupNode called with %q, want %q", mgr.registry.lastNodeID, wantBefore)
	}

	mgr.km.keyID = "rotated-key-2" // simulate a key rotation with no restart
	if _, err := h.(*catalogPublishHandler).checkRegistryLinksCatalogIndex(context.Background()); err != nil {
		t.Fatalf("checkRegistryLinksCatalogIndex after rotation: %v", err)
	}
	wantAfter := "example.test/" + dediSubscriberWildcardRegistry + "/rotated-key-2"
	if mgr.registry.lastNodeID != wantAfter {
		t.Fatalf("after rotation: LookupNode called with %q, want %q -- keyID was not re-resolved", mgr.registry.lastNodeID, wantAfter)
	}
}

// TestCatalogPublishHandler_RegistryLookupErrorDoesNotProduceFalseWarning
// guards against the bug this handler's earlier node-manifest-based check
// had: any lookup failure was silently treated the same as "not declared",
// producing a warning even when the check was actually inconclusive. Now, a
// LookupNode error is surfaced via a log only -- no warning -- since the
// check couldn't determine whether the link exists at all.
func TestCatalogPublishHandler_RegistryLookupErrorDoesNotProduceFalseWarning(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(t)
	mgr.registry.nodeErr = fmt.Errorf("DeDi registry request failed with status: 503 Service Unavailable")

	cfg := newTestConfig(root)
	cfg.Plugins.CatalogPublisher.Config = map[string]string{"checkCatalogIndexLink": "true"}
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
		t.Errorf("expected no warnings on an inconclusive registry check, got %+v", resp.Warnings)
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
