package catalogpublisher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// fakeHandlerKeyManager is a minimal definition.KeyManager double -- only
// Keyset is ever exercised by this handler test suite. Named distinctly
// from this package's own fakeKeyManager (catalogpublisher_test.go), which
// has a different shape (fixed Ed25519 keyset per subscriberID) used by
// this package's business-logic tests.
type fakeHandlerKeyManager struct{}

func (fakeHandlerKeyManager) GenerateKeyset() (*model.Keyset, error) { return nil, nil }
func (fakeHandlerKeyManager) InsertKeyset(context.Context, string, *model.Keyset) error {
	return nil
}
func (fakeHandlerKeyManager) Keyset(context.Context, string) (*model.Keyset, error) {
	return &model.Keyset{}, nil
}
func (fakeHandlerKeyManager) LookupNPKeys(context.Context, string, string) (string, string, error) {
	return "", "", nil
}
func (fakeHandlerKeyManager) DeleteKeyset(context.Context, string) error { return nil }

type fakeHandlerCache struct{}

func (fakeHandlerCache) Get(context.Context, string) (string, error) { return "", nil }
func (fakeHandlerCache) Set(context.Context, string, string, time.Duration) error {
	return nil
}
func (fakeHandlerCache) Delete(context.Context, string) error { return nil }
func (fakeHandlerCache) Clear(context.Context) error          { return nil }

type fakeHandlerRegistry struct{}

func (fakeHandlerRegistry) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	return nil, nil
}

// fakeHandlerCatalogBlobStore is a no-op definition.CatalogBlobStore double:
// this handler test suite exercises plugin wiring, not storage behavior
// (that's this package's own catalogpublisher_test.go's job).
type fakeHandlerCatalogBlobStore struct{}

func (fakeHandlerCatalogBlobStore) Get(context.Context, string) ([]byte, error) {
	return nil, definition.ErrBlobNotFound
}
func (fakeHandlerCatalogBlobStore) Put(context.Context, string, []byte) error { return nil }

// fakeHandlerSchemaValidator records the last payload it was asked to
// validate and returns a configurable error.
type fakeHandlerSchemaValidator struct {
	err       error
	lastBody  []byte
	callCount int
}

func (f *fakeHandlerSchemaValidator) Validate(_ context.Context, _ *url.URL, data []byte) error {
	f.callCount++
	f.lastBody = data
	return f.err
}

// fakeHandlerPolicyChecker records the last StepContext.Body it was asked
// to check and returns a configurable error.
type fakeHandlerPolicyChecker struct {
	err       error
	lastBody  []byte
	callCount int
}

func (f *fakeHandlerPolicyChecker) CheckPolicy(ctx *model.StepContext) error {
	f.callCount++
	f.lastBody = ctx.Body
	return f.err
}

// fakeHandlerCatalogPublisher is a fully-controllable
// definition.CatalogPublisher double: this handler test suite is about
// plugin wiring and the generic EndpointHandler's Decode/Execute/Encode
// sequencing, not about decode/validate/publish business logic (that lives
// in, and is tested by, this package's own catalogpublisher_test.go).
type fakeHandlerCatalogPublisher struct {
	decodeErr   error
	decodeResp  definition.PublishRequest
	publishErr  error
	publishResp definition.PublishResult
	indexURL    string

	decodeCalls  int
	lastDecodeIn []byte
}

func (f *fakeHandlerCatalogPublisher) DecodeRequest(ctx context.Context, r *http.Request) (definition.PublishRequest, error) {
	f.decodeCalls++
	body := make([]byte, r.ContentLength)
	_, _ = r.Body.Read(body)
	if f.decodeErr != nil {
		return definition.PublishRequest{}, f.decodeErr
	}
	return f.decodeResp, nil
}

func (f *fakeHandlerCatalogPublisher) Publish(ctx context.Context, req definition.PublishRequest) (definition.PublishResult, error) {
	return f.publishResp, f.publishErr
}

func (f *fakeHandlerCatalogPublisher) IndexURL() string { return f.indexURL }

// catalogPublishTestManager is a minimal handler.PluginManager for
// exercising NewHandler: only Cache/Registry/KeyManager/CatalogBlobStore/
// CatalogPublisher are ever called by it, every other method is unreachable
// and panics if invoked.
type catalogPublishTestManager struct {
	registry             definition.RegistryLookup
	publisher            definition.CatalogPublisher
	schemaValidator      definition.SchemaValidator
	policyChecker        definition.PolicyChecker
	capturedPublisherCfg *plugin.Config
}

func (m *catalogPublishTestManager) Cache(context.Context, *plugin.Config) (definition.Cache, error) {
	return fakeHandlerCache{}, nil
}
func (m *catalogPublishTestManager) Registry(context.Context, definition.Cache, *plugin.Config) (definition.RegistryLookup, error) {
	if m.registry != nil {
		return m.registry, nil
	}
	return fakeHandlerRegistry{}, nil
}
func (m *catalogPublishTestManager) KeyManager(context.Context, definition.RegistryLookup, *plugin.Config) (definition.KeyManager, error) {
	return fakeHandlerKeyManager{}, nil
}
func (m *catalogPublishTestManager) Mapper(context.Context, *plugin.Config) (definition.Mapper, error) {
	return nil, nil
}

func (m *catalogPublishTestManager) ProviderStep(context.Context, definition.ProviderRecordLookup, definition.Mapper, *plugin.Config) (definition.Step, error) {
	return nil, nil
}

func (m *catalogPublishTestManager) CatalogBlobStore(context.Context, *plugin.Config) (definition.CatalogBlobStore, error) {
	return fakeHandlerCatalogBlobStore{}, nil
}
func (m *catalogPublishTestManager) CatalogPublisher(ctx context.Context, km definition.KeyManager, blobStore definition.CatalogBlobStore, registry definition.RegistryLookup, cfg *plugin.Config) (definition.CatalogPublisher, error) {
	m.capturedPublisherCfg = cfg
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

func newTestManager(publisher definition.CatalogPublisher) *catalogPublishTestManager {
	return &catalogPublishTestManager{publisher: publisher}
}

func newTestConfig() *handler.Config {
	return &handler.Config{
		Plugins: handler.PluginCfg{
			Cache:            &plugin.Config{ID: "cache"},
			Registry:         &plugin.Config{ID: "registry"},
			KeyManager:       &plugin.Config{ID: "keymanager", Config: map[string]string{"subscriberId": "example.test"}},
			CatalogPublisher: &plugin.Config{ID: "catalogpublisher"},
			CatalogBlobStore: &plugin.Config{ID: "localcatalogblobstore", Config: map[string]string{"root": "/catalog"}},
		},
	}
}

func TestNewCatalogPublishHandler_RequiresConfig(t *testing.T) {
	if _, err := NewHandler(context.Background(), newTestManager(&fakeHandlerCatalogPublisher{}), nil, "test"); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestNewCatalogPublishHandler_RequiresCatalogPublisherPlugin(t *testing.T) {
	cfg := newTestConfig()
	cfg.Plugins.CatalogPublisher = nil
	if _, err := NewHandler(context.Background(), newTestManager(&fakeHandlerCatalogPublisher{}), cfg, "test"); err == nil {
		t.Fatal("expected error for missing catalogPublisher plugin config")
	}
}

func TestNewCatalogPublishHandler_RequiresCatalogBlobStorePlugin(t *testing.T) {
	cfg := newTestConfig()
	cfg.Plugins.CatalogBlobStore = nil
	if _, err := NewHandler(context.Background(), newTestManager(&fakeHandlerCatalogPublisher{}), cfg, "test"); err == nil {
		t.Fatal("expected error for missing catalogBlobStore plugin config")
	}
}

func TestNewCatalogPublishHandler_DerivesSubscriberIDFromKeyManager(t *testing.T) {
	mgr := newTestManager(&fakeHandlerCatalogPublisher{})
	cfg := newTestConfig() // catalogPublisher.Config has no subscriberId of its own
	if _, err := NewHandler(context.Background(), mgr, cfg, "test"); err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	if mgr.capturedPublisherCfg == nil || mgr.capturedPublisherCfg.Config["subscriberId"] != "example.test" {
		t.Fatalf("expected subscriberId derived from keyManager config, got %+v", mgr.capturedPublisherCfg)
	}
}

func TestNewCatalogPublishHandler_ExplicitSubscriberIDIsNotOverridden(t *testing.T) {
	mgr := newTestManager(&fakeHandlerCatalogPublisher{})
	cfg := newTestConfig()
	cfg.Plugins.CatalogPublisher.Config = map[string]string{"subscriberId": "explicit.test"}
	if _, err := NewHandler(context.Background(), mgr, cfg, "test"); err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	if mgr.capturedPublisherCfg == nil || mgr.capturedPublisherCfg.Config["subscriberId"] != "explicit.test" {
		t.Fatalf("expected explicit subscriberId to be left untouched, got %+v", mgr.capturedPublisherCfg)
	}
}

func TestNewCatalogPublishHandler_RequiresKeyManagerSubscriberID(t *testing.T) {
	mgr := newTestManager(&fakeHandlerCatalogPublisher{})
	cfg := newTestConfig()
	cfg.Plugins.KeyManager.Config = nil // no subscriberId to derive from
	if _, err := NewHandler(context.Background(), mgr, cfg, "test"); err == nil {
		t.Fatal("expected error when keyManager config has no subscriberId to derive from")
	}
}

func TestCatalogPublishHandler_PublishesAndRendersResults(t *testing.T) {
	pub := &fakeHandlerCatalogPublisher{
		decodeResp: definition.PublishRequest{
			Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1"}},
		},
		publishResp: definition.PublishResult{
			Catalogs: []definition.CatalogPublishOutcome{{CatalogID: "example.test/CAT-1", Version: 1}},
		},
	}
	h, err := NewHandler(context.Background(), newTestManager(pub), newTestConfig(), "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1"}]}}`
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
}

func TestCatalogPublishHandler_RunsSchemaValidatorAndPolicyCheckerOnRawBody(t *testing.T) {
	schemaValidator := &fakeHandlerSchemaValidator{}
	policyChecker := &fakeHandlerPolicyChecker{}
	pub := &fakeHandlerCatalogPublisher{
		decodeResp: definition.PublishRequest{Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1"}}},
	}
	mgr := newTestManager(pub)
	mgr.schemaValidator = schemaValidator
	mgr.policyChecker = policyChecker

	cfg := newTestConfig()
	cfg.Plugins.SchemaValidator = &plugin.Config{ID: "schemav2validator"}
	cfg.Plugins.PolicyChecker = &plugin.Config{ID: "opapolicychecker"}
	h, err := NewHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1"}]}}`
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
	if string(schemaValidator.lastBody) != body {
		t.Errorf("schemaValidator was not given the raw request body: got %q, want %q", schemaValidator.lastBody, body)
	}
	if string(schemaValidator.lastBody) != string(policyChecker.lastBody) {
		t.Errorf("schemaValidator and policyChecker were not given the same body")
	}
}

func TestCatalogPublishHandler_SkipsValidationForRetireOnlyRequest(t *testing.T) {
	schemaValidator := &fakeHandlerSchemaValidator{}
	pub := &fakeHandlerCatalogPublisher{
		decodeResp: definition.PublishRequest{Retire: []string{"example.test/CAT-GONE"}},
	}
	mgr := newTestManager(pub)
	mgr.schemaValidator = schemaValidator

	cfg := newTestConfig()
	cfg.Plugins.SchemaValidator = &plugin.Config{ID: "schemav2validator"}
	h, err := NewHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(`{"retire":["example.test/CAT-GONE"]}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if schemaValidator.callCount != 0 {
		t.Errorf("expected schemaValidator not called for a retire-only request, got %d calls", schemaValidator.callCount)
	}
}

func TestCatalogPublishHandler_RejectsRequestWhenSchemaValidationFails(t *testing.T) {
	schemaValidator := &fakeHandlerSchemaValidator{err: fmt.Errorf("schema mismatch")}
	pub := &fakeHandlerCatalogPublisher{
		decodeResp: definition.PublishRequest{Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1"}}},
	}
	mgr := newTestManager(pub)
	mgr.schemaValidator = schemaValidator

	cfg := newTestConfig()
	cfg.Plugins.SchemaValidator = &plugin.Config{ID: "schemav2validator"}
	h, err := NewHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1"}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCatalogPublishHandler_RejectsRequestWhenPolicyCheckFails(t *testing.T) {
	policyChecker := &fakeHandlerPolicyChecker{err: fmt.Errorf("policy violation")}
	pub := &fakeHandlerCatalogPublisher{
		decodeResp: definition.PublishRequest{Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1"}}},
	}
	mgr := newTestManager(pub)
	mgr.policyChecker = policyChecker

	cfg := newTestConfig()
	cfg.Plugins.PolicyChecker = &plugin.Config{ID: "opapolicychecker"}
	h, err := NewHandler(context.Background(), mgr, cfg, "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1"}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCatalogPublishHandler_RejectsWhenDecodeRequestFails(t *testing.T) {
	pub := &fakeHandlerCatalogPublisher{decodeErr: fmt.Errorf("invalid request body")}
	h, err := NewHandler(context.Background(), newTestManager(pub), newTestConfig(), "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestCatalogPublishHandler_PublishErrorReportsOverallFailedWithCodedError(t *testing.T) {
	pub := &fakeHandlerCatalogPublisher{
		decodeResp: definition.PublishRequest{Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1"}}},
		publishErr: fmt.Errorf("storage unavailable"),
	}
	h, err := NewHandler(context.Background(), newTestManager(pub), newTestConfig(), "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1"}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (publish error is reported in the body, not a transport error), got %d: %s", rec.Code, rec.Body.String())
	}
	var resp publishResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}
	if resp.Status != publishOverallFailed {
		t.Fatalf("expected overall status FAILED, got %+v", resp)
	}
	if resp.Message == nil || resp.Message.Error == nil {
		t.Fatal("expected a coded error in the response message")
	}
}

func TestCatalogPublishHandler_FatalPublishErrorReportsOverallFailed(t *testing.T) {
	pub := &fakeHandlerCatalogPublisher{
		decodeResp: definition.PublishRequest{Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1"}}},
		publishResp: definition.PublishResult{
			Errors: []definition.PublishError{{CatalogID: "example.test/CAT-1", Stage: "sign", Reason: "signing key unavailable", Fatal: true}},
		},
	}
	h, err := NewHandler(context.Background(), newTestManager(pub), newTestConfig(), "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1"}]}}`
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

func TestCatalogPublishHandler_Retire(t *testing.T) {
	pub := &fakeHandlerCatalogPublisher{
		decodeResp: definition.PublishRequest{Retire: []string{"example.test/CAT-OLD"}},
	}
	h, err := NewHandler(context.Background(), newTestManager(pub), newTestConfig(), "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
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
	pub := &fakeHandlerCatalogPublisher{
		decodeResp: definition.PublishRequest{
			Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1"}},
			Retire:   []string{"example.test/CAT-1"},
		},
		publishResp: definition.PublishResult{
			Catalogs: []definition.CatalogPublishOutcome{{CatalogID: "example.test/CAT-1", Version: 1}},
		},
	}
	h, err := NewHandler(context.Background(), newTestManager(pub), newTestConfig(), "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1"}]},"retire":["example.test/CAT-1"]}`
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

func TestCatalogPublishHandler_WarningsFromPublishAreSurfaced(t *testing.T) {
	pub := &fakeHandlerCatalogPublisher{
		decodeResp: definition.PublishRequest{Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1"}}},
		publishResp: definition.PublishResult{
			Catalogs: []definition.CatalogPublishOutcome{{CatalogID: "example.test/CAT-1", Version: 1}},
			Warnings: []string{"DeDi record does not link catalog index"},
		},
	}
	h, err := NewHandler(context.Background(), newTestManager(pub), newTestConfig(), "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1"}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp publishResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}
	if len(resp.Warnings) != 1 {
		t.Fatalf("expected 1 warning surfaced from Publish's result, got %+v", resp.Warnings)
	}
}

func TestCatalogPublishHandler_MethodNotAllowed(t *testing.T) {
	// A real DecodeRequest (decode.go) wraps a method mismatch in
	// handler.StatusError{Status: http.StatusMethodNotAllowed}, which the
	// generic EndpointHandler shell (endpointhandler.go) must surface as
	// 405, not the default 400 every other Decode failure gets.
	pub := &fakeHandlerCatalogPublisher{decodeErr: &handler.StatusError{
		Status: http.StatusMethodNotAllowed,
		Err:    fmt.Errorf("method not allowed: GET"),
	}}
	h, err := NewHandler(context.Background(), newTestManager(pub), newTestConfig(), "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/catalog/publish", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestCatalogPublishHandler_DecodeErrorWithoutStatusDefaultsTo400(t *testing.T) {
	// A Decode error that doesn't wrap a *handler.StatusError (e.g. a
	// malformed body or failed validation) falls back to the default 400.
	pub := &fakeHandlerCatalogPublisher{decodeErr: fmt.Errorf("invalid request body")}
	h, err := NewHandler(context.Background(), newTestManager(pub), newTestConfig(), "test")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
