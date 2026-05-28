package handler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// mockSignValidatorBasic is a simple SignValidator that tracks which method was
// called and returns configurable errors.
type mockSignValidatorBasic struct {
	validateErr       error
	validateAckErr    error
	validateCalled    bool
	validateAckCalled bool
}

func (m *mockSignValidatorBasic) Validate(_ context.Context, _ []byte, _ string, _ string) error {
	m.validateCalled = true
	return m.validateErr
}
func (m *mockSignValidatorBasic) ValidateAck(_ context.Context, _ []byte, _, _, _ string) error {
	m.validateAckCalled = true
	return m.validateAckErr
}

// mockKMBasic is a simple KeyManager that returns a preset public key or error.
type mockKMBasic struct {
	publicKey string
	lookupErr error
	keyset    *model.Keyset // returned by Keyset(); nil when not set
}

func (m *mockKMBasic) GenerateKeyset() (*model.Keyset, error)                          { return nil, nil }
func (m *mockKMBasic) InsertKeyset(_ context.Context, _ string, _ *model.Keyset) error { return nil }
func (m *mockKMBasic) DeleteKeyset(_ context.Context, _ string) error                  { return nil }
func (m *mockKMBasic) Keyset(_ context.Context, _ string) (*model.Keyset, error) {
	return m.keyset, nil
}
func (m *mockKMBasic) LookupNPKeys(_ context.Context, _, _ string) (string, string, error) {
	return m.publicKey, "", m.lookupErr
}

// mockPayloadStore is an in-memory definition.PayloadStore for testing.
type mockPayloadStore struct {
	storeErr error
	entries  map[string]*definition.PayloadEntry // key: "messageID:action"
	getErr   error
}

func newMockPayloadStore() *mockPayloadStore {
	return &mockPayloadStore{entries: map[string]*definition.PayloadEntry{}}
}

// storeEntry pre-populates an entry for use in validateRequestSignatureChain tests.
func (m *mockPayloadStore) storeEntry(messageID, action, signature string) {
	m.entries[messageID+":"+action] = &definition.PayloadEntry{
		MessageID: messageID,
		Action:    action,
		Signature: signature,
	}
}

func (m *mockPayloadStore) Store(_ *model.StepContext) error { return m.storeErr }
func (m *mockPayloadStore) Exists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (m *mockPayloadStore) GetByTransactionID(_ context.Context, _ string) ([]definition.PayloadEntry, error) {
	return nil, nil
}
func (m *mockPayloadStore) GetByMessageID(_ context.Context, messageID, action string) (*definition.PayloadEntry, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	e, ok := m.entries[messageID+":"+action]
	if !ok {
		return nil, nil
	}
	return e, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// solicitedCallbackAuthHeader builds a spec-compliant 6-attribute Signature header
// that declares "request-signature" in the headers list — identifying a solicited
// callback. The CN's original signature is in the signing string, not as a header
// attribute (NFH-004 §4).
func solicitedCallbackAuthHeader(subscriberID string) string {
	return `Signature keyId="` + subscriberID + `|key-1|ed25519",algorithm="ed25519",` +
		`created="1700000000",expires="1700003600",` +
		`headers="(created) (expires) digest request-signature",` +
		`signature="callbackSig=="`
}

// providerInitiatedAuthHeader builds a Signature header without request-signature.
func providerInitiatedAuthHeader(subscriberID string) string {
	return `Signature keyId="` + subscriberID + `|key-1|ed25519",algorithm="ed25519",` +
		`created="1700000000",expires="1700003600",` +
		`headers="(created) (expires) digest",` +
		`signature="providerSig=="`
}

// makeReceiverStepCtx creates a StepContext for the Receiver path.
// body should be a Beckn JSON payload (used to extract action via extractBecknAction).
func makeReceiverStepCtx(protocolVersion, messageID, subID, authHeader, bodyJSON string) *model.StepContext {
	req, _ := http.NewRequest(http.MethodPost, "/bap/reciever/", strings.NewReader(bodyJSON))
	req.Header.Set(model.AuthHeaderSubscriber, authHeader)
	return &model.StepContext{
		Context:         context.Background(),
		Request:         req,
		Body:            []byte(bodyJSON),
		ProtocolVersion: protocolVersion,
		MessageID:       messageID,
		SubID:           subID,
		RespHeader:      http.Header{},
	}
}

// ---------------------------------------------------------------------------
// newValidateSignStep constructor tests
// ---------------------------------------------------------------------------

func TestNewValidateSignStep_NilSignValidator_ReturnsError(t *testing.T) {
	km := &mockKMBasic{}
	if _, err := newValidateSignStep(nil, km, nil); err == nil {
		t.Fatal("expected error for nil SignValidator")
	}
}

func TestNewValidateSignStep_NilKM_ReturnsError(t *testing.T) {
	sv := &mockSignValidatorBasic{}
	if _, err := newValidateSignStep(sv, nil, nil); err == nil {
		t.Fatal("expected error for nil KeyManager")
	}
}

func TestNewValidateSignStep_NilPayloadStore_OK(t *testing.T) {
	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, err := newValidateSignStep(sv, km, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if step == nil {
		t.Fatal("expected non-nil step")
	}
}

// ---------------------------------------------------------------------------
// authHeaderIncludesRequestSig unit tests
// ---------------------------------------------------------------------------

func TestAuthHeaderIncludesRequestSig_True(t *testing.T) {
	header := `Signature keyId="bpp.example.com|k1|ed25519",headers="(created) (expires) digest request-signature",signature="abc"`
	if !authHeaderIncludesRequestSig(header) {
		t.Error("expected true for header containing request-signature")
	}
}

func TestAuthHeaderIncludesRequestSig_False_NoRequestSig(t *testing.T) {
	header := `Signature keyId="bpp.example.com|k1|ed25519",headers="(created) (expires) digest",signature="abc"`
	if authHeaderIncludesRequestSig(header) {
		t.Error("expected false for header without request-signature")
	}
}

func TestAuthHeaderIncludesRequestSig_False_MissingHeadersField(t *testing.T) {
	header := `Signature keyId="bpp.example.com|k1|ed25519",signature="abc"`
	if authHeaderIncludesRequestSig(header) {
		t.Error("expected false for header without headers= attribute")
	}
}

const onSearchBody = `{"context":{"action":"on_search","messageId":"msg-chain-001","version":"2.0.0"}}`

// ---------------------------------------------------------------------------
// PayloadStore wiring tests via initSteps
// ---------------------------------------------------------------------------

// TestValidateSignStep_InitSteps_WithPayloadStore verifies that initSteps wires
// the payloadStore into the validateSign step without error.
func TestValidateSignStep_InitSteps_WithPayloadStore(t *testing.T) {
	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	store := newMockPayloadStore()

	h := &stdHandler{
		signValidator: sv,
		km:            km,
		payloadStore:  store,
		steps:         []definition.Step{},
		responseSteps: []definition.ResponseStep{},
	}

	cfg := &Config{Steps: []string{"validateSign"}}
	if err := h.initSteps(context.Background(), noopPluginManager{}, cfg); err != nil {
		t.Fatalf("initSteps() unexpected error: %v", err)
	}
	if len(h.steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(h.steps))
	}
}

// ---------------------------------------------------------------------------
// signStep.Run — Sign vs SignAck dispatch
// ---------------------------------------------------------------------------

// testKeyset returns a minimal Keyset with a dummy private key seed (32 zero
// bytes, base64-encoded) sufficient to construct but not cryptographically
// verify real signatures in unit tests.
func testKeyset() *model.Keyset {
	return &model.Keyset{
		UniqueKeyID:   "key-1",
		SigningPrivate: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
}

func makeSignStepCtx(action, messageID, subID string) *model.StepContext {
	body := []byte(`{"context":{"action":"` + action + `","messageId":"` + messageID + `","version":"2.0.0"}}`)
	req, _ := http.NewRequest(http.MethodPost, "/bpp/caller/"+action, nil)
	return &model.StepContext{
		Context:    context.Background(),
		Request:    req,
		Body:       body,
		MessageID:  messageID,
		SubID:      subID,
		RespHeader: http.Header{},
	}
}

func TestSignStep_Run_SolicitedCallback_UsesSignAck(t *testing.T) {
	store := newMockPayloadStore()
	store.storeEntry("msg-sign-run-001", "search", "callerSig==")

	signer := &mockSigner{returnSig: "callbackSig=="}
	km := &mockKMBasic{keyset: testKeyset()}
	step, _ := newSignStep(signer, km, store)

	ctx := makeSignStepCtx("on_search", "msg-sign-run-001", "bpp.example.com")
	if err := step.Run(ctx); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if !signer.signAckCalled {
		t.Error("expected SignAck to be called for a solicited callback")
	}
	if signer.signCalled {
		t.Error("expected Sign NOT to be called for a solicited callback")
	}
	authVal := ctx.Request.Header.Get(model.AuthHeaderSubscriber)
	if strings.Contains(authVal, `request-signature="`) {
		t.Errorf("Authorization header must not contain request-signature attribute, got: %s", authVal)
	}
	if !strings.Contains(authVal, `headers="(created) (expires) digest request-signature"`) {
		t.Errorf("Authorization header must declare request-signature in headers list, got: %s", authVal)
	}
}

func TestSignStep_Run_NonCallback_UsesSign(t *testing.T) {
	signer := &mockSigner{returnSig: "requestSig=="}
	km := &mockKMBasic{keyset: testKeyset()}
	step, _ := newSignStep(signer, km, nil)

	ctx := makeSignStepCtx("search", "msg-sign-run-002", "bap.example.com")
	if err := step.Run(ctx); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if signer.signAckCalled {
		t.Error("expected SignAck NOT to be called for a regular request")
	}
	if !signer.signCalled {
		t.Error("expected Sign to be called for a regular request")
	}
}

func TestSignStep_Run_SolicitedCallback_NoStoredEntry_FallsBackToSign(t *testing.T) {
	// PayloadStore configured but no entry for this messageID — lookupRequestSignature
	// returns "" so Sign (3-line) is used rather than failing the sign step.
	store := newMockPayloadStore() // empty

	signer := &mockSigner{returnSignSig: "fallbackSig=="}
	km := &mockKMBasic{keyset: testKeyset()}
	step, _ := newSignStep(signer, km, store)

	ctx := makeSignStepCtx("on_search", "msg-sign-run-003", "bpp.example.com")
	if err := step.Run(ctx); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if signer.signAckCalled {
		t.Error("expected SignAck NOT to be called when no stored entry exists")
	}
	if !signer.signCalled {
		t.Error("expected Sign to be called when no stored entry exists")
	}
}

func TestSignStep_Run_SignAckError_ReturnsError(t *testing.T) {
	store := newMockPayloadStore()
	store.storeEntry("msg-sign-run-004", "search", "callerSig==")

	signer := &mockSigner{signAckErr: errors.New("key unavailable")}
	km := &mockKMBasic{keyset: testKeyset()}
	step, _ := newSignStep(signer, km, store)

	ctx := makeSignStepCtx("on_search", "msg-sign-run-004", "bpp.example.com")
	if err := step.Run(ctx); err == nil {
		t.Fatal("expected error when SignAck fails")
	}
}

// ---------------------------------------------------------------------------
// validateHeaders — 3-line vs 4-line dispatch
// ---------------------------------------------------------------------------

func makeValidateStepCtx(protocolVersion, messageID, subID, authHeader, bodyJSON string) *model.StepContext {
	req, _ := http.NewRequest(http.MethodPost, "/bap/receiver/on_search", strings.NewReader(bodyJSON))
	req.Header.Set(model.AuthHeaderSubscriber, authHeader)
	return &model.StepContext{
		Context:         context.Background(),
		Request:         req,
		Body:            []byte(bodyJSON),
		ProtocolVersion: protocolVersion,
		MessageID:       messageID,
		SubID:           subID,
		RespHeader:      http.Header{},
	}
}

func TestValidateHeaders_SolicitedCallback_UsesValidateAck(t *testing.T) {
	store := newMockPayloadStore()
	store.storeEntry("msg-vh-001", "search", "storedSig==")

	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, store)
	vStep := step.(*validateSignStep)

	ctx := makeValidateStepCtx("2.0.0", "msg-vh-001", "bap.example.com",
		solicitedCallbackAuthHeader("bpp.example.com"),
		`{"context":{"action":"on_search","messageId":"msg-vh-001","version":"2.0.0"}}`)

	if err := vStep.validateHeaders(ctx); err != nil {
		t.Fatalf("validateHeaders() unexpected error: %v", err)
	}
	if !sv.validateAckCalled {
		t.Error("expected ValidateAck to be called for a solicited callback")
	}
	if sv.validateCalled {
		t.Error("expected Validate NOT to be called for a solicited callback")
	}
}

func TestValidateHeaders_ProviderInitiated_UsesValidate(t *testing.T) {
	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, nil)
	vStep := step.(*validateSignStep)

	ctx := makeValidateStepCtx("2.0.0", "msg-vh-002", "bap.example.com",
		providerInitiatedAuthHeader("bpp.example.com"),
		`{"context":{"action":"on_search","messageId":"msg-vh-002","version":"2.0.0"}}`)

	if err := vStep.validateHeaders(ctx); err != nil {
		t.Fatalf("validateHeaders() unexpected error: %v", err)
	}
	if sv.validateAckCalled {
		t.Error("expected ValidateAck NOT to be called for a provider-initiated callback")
	}
	if !sv.validateCalled {
		t.Error("expected Validate to be called for a provider-initiated callback")
	}
}

func TestValidateHeaders_SolicitedCallback_NilStore_FallsBackToValidate(t *testing.T) {
	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	// nil payloadStore — degrade to 3-line verification
	step, _ := newValidateSignStep(sv, km, nil)
	vStep := step.(*validateSignStep)

	ctx := makeValidateStepCtx("2.0.0", "msg-vh-003", "bap.example.com",
		solicitedCallbackAuthHeader("bpp.example.com"),
		`{"context":{"action":"on_search","messageId":"msg-vh-003","version":"2.0.0"}}`)

	if err := vStep.validateHeaders(ctx); err != nil {
		t.Fatalf("validateHeaders() unexpected error: %v", err)
	}
	if sv.validateAckCalled {
		t.Error("expected ValidateAck NOT to be called when payloadStore is nil")
	}
	if !sv.validateCalled {
		t.Error("expected Validate to be called when payloadStore is nil")
	}
}

func TestValidateHeaders_SolicitedCallback_PreV2_FallsBackToValidate(t *testing.T) {
	store := newMockPayloadStore()
	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, store)
	vStep := step.(*validateSignStep)

	ctx := makeValidateStepCtx("1.1.0", "msg-vh-004", "bap.example.com",
		solicitedCallbackAuthHeader("bpp.example.com"),
		`{"context":{"action":"on_search","messageId":"msg-vh-004","version":"1.1.0"}}`)

	if err := vStep.validateHeaders(ctx); err != nil {
		t.Fatalf("validateHeaders() unexpected error: %v", err)
	}
	if sv.validateAckCalled {
		t.Error("expected ValidateAck NOT to be called for pre-v2 version")
	}
	if !sv.validateCalled {
		t.Error("expected Validate to be called for pre-v2 version")
	}
}

func TestValidateHeaders_SolicitedCallback_NoEntry_ReturnsError(t *testing.T) {
	store := newMockPayloadStore() // empty

	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, store)
	vStep := step.(*validateSignStep)

	ctx := makeValidateStepCtx("2.0.0", "msg-vh-005", "bap.example.com",
		solicitedCallbackAuthHeader("bpp.example.com"),
		`{"context":{"action":"on_search","messageId":"msg-vh-005","version":"2.0.0"}}`)

	if err := vStep.validateHeaders(ctx); err == nil {
		t.Fatal("expected error when no stored entry found for message ID")
	}
}

func TestValidateHeaders_SolicitedCallback_StoreError_ReturnsError(t *testing.T) {
	store := newMockPayloadStore()
	store.getErr = errors.New("redis down")

	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, store)
	vStep := step.(*validateSignStep)

	ctx := makeValidateStepCtx("2.0.0", "msg-vh-006", "bap.example.com",
		solicitedCallbackAuthHeader("bpp.example.com"),
		`{"context":{"action":"on_search","messageId":"msg-vh-006","version":"2.0.0"}}`)

	if err := vStep.validateHeaders(ctx); err == nil {
		t.Fatal("expected error when payloadStore.GetByMessageID returns error")
	}
}

// ---------------------------------------------------------------------------
// signStep — constructor and generateAuthHeader tests
// ---------------------------------------------------------------------------

func TestNewSignStep_NilSigner_ReturnsError(t *testing.T) {
	km := &mockKMBasic{}
	if _, err := newSignStep(nil, km, nil); err == nil {
		t.Fatal("expected error for nil Signer")
	}
}

func TestNewSignStep_NilKM_ReturnsError(t *testing.T) {
	if _, err := newSignStep(&mockSigner{}, nil, nil); err == nil {
		t.Fatal("expected error for nil KeyManager")
	}
}

func TestNewSignStep_NilPayloadStore_OK(t *testing.T) {
	step, err := newSignStep(&mockSigner{returnSig: "sig=="}, &mockKMBasic{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if step == nil {
		t.Fatal("expected non-nil step")
	}
}

func TestGenerateAuthHeader_WithoutRequestSig(t *testing.T) {
	s := &signStep{}
	h := s.generateAuthHeader("sub.example.com", "key-1", 1700000000, 1700003600, "mySig==", "")
	if !strings.Contains(h, `headers="(created) (expires) digest"`) {
		t.Errorf("expected standard headers field, got: %s", h)
	}
	if strings.Contains(h, "request-signature") {
		t.Errorf("unexpected request-signature in header: %s", h)
	}
	if !strings.Contains(h, `signature="mySig=="`) {
		t.Errorf("missing signature value, got: %s", h)
	}
}

func TestGenerateAuthHeader_WithRequestSig(t *testing.T) {
	s := &signStep{}
	h := s.generateAuthHeader("sub.example.com", "key-1", 1700000000, 1700003600, "mySig==", "originalSig==")
	if !strings.Contains(h, `headers="(created) (expires) digest request-signature"`) {
		t.Errorf("expected extended headers field, got: %s", h)
	}
	// request-signature must be in the headers list (signing string declaration)
	// but must NOT appear as a separate header attribute (NFH-004 §4).
	if strings.Contains(h, `request-signature="`) {
		t.Errorf("request-signature must not appear as a header attribute, got: %s", h)
	}
	if !strings.Contains(h, `signature="mySig=="`) {
		t.Errorf("missing signature value, got: %s", h)
	}
}

// ---------------------------------------------------------------------------
// signStep.lookupRequestSignature tests
// ---------------------------------------------------------------------------

const onSearchCallbackBody = `{"context":{"action":"on_search","messageId":"msg-sign-001","version":"2.0.0"}}`
const searchCallerBody = `{"context":{"action":"search","messageId":"msg-sign-001","version":"2.0.0"}}`

func TestLookupRequestSignature_NilStore_ReturnsEmpty(t *testing.T) {
	s := &signStep{}
	ctx := makeReceiverStepCtx("2.0.0", "msg-sign-001", "bpp.example.com", "", onSearchCallbackBody)
	if got := s.lookupRequestSignature(ctx); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestLookupRequestSignature_NonCallbackAction_ReturnsEmpty(t *testing.T) {
	store := newMockPayloadStore()
	store.storeEntry("msg-sign-001", "search", "callerSig==")
	s := &signStep{payloadStore: store}
	ctx := makeReceiverStepCtx("2.0.0", "msg-sign-001", "bpp.example.com", "", searchCallerBody)
	if got := s.lookupRequestSignature(ctx); got != "" {
		t.Errorf("expected empty for non-callback action, got %q", got)
	}
}

func TestLookupRequestSignature_EntryFound_ReturnsSig(t *testing.T) {
	store := newMockPayloadStore()
	store.storeEntry("msg-sign-001", "search", "callerSig==")
	s := &signStep{payloadStore: store}
	ctx := makeReceiverStepCtx("2.0.0", "msg-sign-001", "bpp.example.com", "", onSearchCallbackBody)
	if got := s.lookupRequestSignature(ctx); got != "callerSig==" {
		t.Errorf("expected %q, got %q", "callerSig==", got)
	}
}

func TestLookupRequestSignature_NoEntry_ReturnsEmpty(t *testing.T) {
	store := newMockPayloadStore() // empty
	s := &signStep{payloadStore: store}
	ctx := makeReceiverStepCtx("2.0.0", "msg-sign-001", "bpp.example.com", "", onSearchCallbackBody)
	if got := s.lookupRequestSignature(ctx); got != "" {
		t.Errorf("expected empty when no entry, got %q", got)
	}
}

func TestLookupRequestSignature_StoreError_ReturnsEmpty(t *testing.T) {
	store := newMockPayloadStore()
	store.getErr = errors.New("redis down")
	s := &signStep{payloadStore: store}
	ctx := makeReceiverStepCtx("2.0.0", "msg-sign-001", "bpp.example.com", "", onSearchCallbackBody)
	// Store error is non-fatal — should degrade to empty request-signature.
	if got := s.lookupRequestSignature(ctx); got != "" {
		t.Errorf("expected empty on store error, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// stripBasePath unit tests
// ---------------------------------------------------------------------------

func TestStripBasePath_NilURL_ReturnsNil(t *testing.T) {
	if got := stripBasePath(nil, "/bap/receiver/"); got != nil {
		t.Errorf("expected nil for nil URL, got %+v", got)
	}
}

func TestStripBasePath_EmptyBasePath_StripsLeadingSlash(t *testing.T) {
	u, _ := url.Parse("/search")
	got := stripBasePath(u, "")
	if got.Path != "search" {
		t.Errorf("stripBasePath() Path = %q, want %q", got.Path, "search")
	}
}

func TestStripBasePath_SingleWordAction(t *testing.T) {
	u, _ := url.Parse("/bap/receiver/search")
	got := stripBasePath(u, "/bap/receiver/")
	if got.Path != "search" {
		t.Errorf("stripBasePath() Path = %q, want %q", got.Path, "search")
	}
}

func TestStripBasePath_CompoundAction(t *testing.T) {
	u, _ := url.Parse("/bap/receiver/catalog/subscription")
	got := stripBasePath(u, "/bap/receiver/")
	if got.Path != "catalog/subscription" {
		t.Errorf("stripBasePath() Path = %q, want %q", got.Path, "catalog/subscription")
	}
}

func TestStripBasePath_BasePathWithoutTrailingSlash(t *testing.T) {
	// basePath without trailing slash: TrimPrefix leaves a leading "/" which is
	// then stripped by the second TrimPrefix.
	u, _ := url.Parse("/bap/receiver/search")
	got := stripBasePath(u, "/bap/receiver")
	if got.Path != "search" {
		t.Errorf("stripBasePath() Path = %q, want %q", got.Path, "search")
	}
}

func TestStripBasePath_URLEqualsBasePath_ReturnsEmpty(t *testing.T) {
	u, _ := url.Parse("/bap/receiver/")
	got := stripBasePath(u, "/bap/receiver/")
	if got.Path != "" {
		t.Errorf("stripBasePath() Path = %q, want empty string", got.Path)
	}
}

func TestStripBasePath_QueryParamsNotIncluded(t *testing.T) {
	// url.URL.Path does not include the query string, so the returned Path is
	// never polluted by query parameters.
	u, _ := url.Parse("/bap/receiver/catalog/subscription?subscriptionId=abc-123")
	got := stripBasePath(u, "/bap/receiver/")
	if got.Path != "catalog/subscription" {
		t.Errorf("stripBasePath() Path = %q, want %q", got.Path, "catalog/subscription")
	}
}

func TestStripBasePath_DoesNotMutateURL(t *testing.T) {
	u, _ := url.Parse("/bap/receiver/search")
	original := u.Path
	stripBasePath(u, "/bap/receiver/")
	if u.Path != original {
		t.Errorf("stripBasePath() mutated original URL.Path: %q", u.Path)
	}
}

// ---------------------------------------------------------------------------
// Mocks for validateSchemaStep / addRouteStep wiring tests
// ---------------------------------------------------------------------------

// mockRecordingValidator records the *url.URL passed to Validate.
type mockRecordingValidator struct {
	gotURL *url.URL
	retErr error
}

func (m *mockRecordingValidator) Validate(_ context.Context, u *url.URL, _ []byte) error {
	m.gotURL = u
	return m.retErr
}

// mockRecordingRouter records the *url.URL passed to Route.
type mockRecordingRouter struct {
	gotURL *url.URL
	retErr error
}

func (m *mockRecordingRouter) Route(_ context.Context, u *url.URL, _ []byte) (*model.Route, error) {
	m.gotURL = u
	if m.retErr != nil {
		return nil, m.retErr
	}
	return &model.Route{TargetType: "url"}, nil
}

// makeStepCtxWithURL builds a minimal StepContext with the given raw URL string.
func makeStepCtxWithURL(rawURL string) *model.StepContext {
	req, _ := http.NewRequest(http.MethodPost, rawURL, nil)
	return &model.StepContext{
		Context:    context.Background(),
		Request:    req,
		Body:       []byte(`{"context":{"action":"search"}}`),
		RespHeader: http.Header{},
	}
}

// ---------------------------------------------------------------------------
// validateSchemaStep constructor + stripBasePath wiring tests
// ---------------------------------------------------------------------------

func TestNewValidateSchemaStep_NilValidator_ReturnsError(t *testing.T) {
	if _, err := newValidateSchemaStep(nil, ""); err == nil {
		t.Fatal("expected error for nil SchemaValidator")
	}
}

func TestValidateSchemaStep_Run_ExtractsAction(t *testing.T) {
	mv := &mockRecordingValidator{}
	step, err := newValidateSchemaStep(mv, "/bap/receiver/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ctx := makeStepCtxWithURL("http://localhost/bap/receiver/catalog/subscription")
	_ = step.Run(ctx)
	if mv.gotURL == nil || mv.gotURL.Path != "catalog/subscription" {
		t.Errorf("validator received path %q, want %q", mv.gotURL.Path, "catalog/subscription")
	}
}

func TestValidateSchemaStep_Run_NoBasePath_UsesRawAction(t *testing.T) {
	mv := &mockRecordingValidator{}
	step, _ := newValidateSchemaStep(mv, "")
	ctx := makeStepCtxWithURL("http://localhost/search")
	_ = step.Run(ctx)
	if mv.gotURL == nil || mv.gotURL.Path != "search" {
		t.Errorf("validator received path %q, want %q", mv.gotURL.Path, "search")
	}
}

func TestValidateSchemaStep_Run_EmptyBodyPost_ReturnsError(t *testing.T) {
	mv := &mockRecordingValidator{}
	step, _ := newValidateSchemaStep(mv, "")
	req, _ := http.NewRequest(http.MethodPost, "http://localhost/catalog/subscription", nil)
	ctx := &model.StepContext{
		Context:    context.Background(),
		Request:    req,
		Body:       []byte{},
		RespHeader: http.Header{},
	}
	err := step.Run(ctx)
	if err == nil {
		t.Fatal("expected error for empty-body POST, got nil")
	}
	var badReq *model.BadReqErr
	if !errors.As(err, &badReq) {
		t.Errorf("expected BadReqErr, got %T: %v", err, err)
	}
	if mv.gotURL != nil {
		t.Error("Validate should not have been called for empty-body POST")
	}
}

// ---------------------------------------------------------------------------
// addRouteStep constructor + stripBasePath wiring tests
// ---------------------------------------------------------------------------

func TestNewAddRouteStep_NilRouter_ReturnsError(t *testing.T) {
	if _, err := newAddRouteStep(nil, ""); err == nil {
		t.Fatal("expected error for nil Router")
	}
}

func TestAddRouteStep_Run_ExtractsAction(t *testing.T) {
	mr := &mockRecordingRouter{}
	step, err := newAddRouteStep(mr, "/bpp/caller/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ctx := makeStepCtxWithURL("http://localhost/bpp/caller/catalog/subscription")
	if err := step.Run(ctx); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if mr.gotURL == nil || mr.gotURL.Path != "catalog/subscription" {
		t.Errorf("router received path %q, want %q", mr.gotURL.Path, "catalog/subscription")
	}
}

func TestAddRouteStep_Run_NoBasePath_UsesRawAction(t *testing.T) {
	mr := &mockRecordingRouter{}
	step, _ := newAddRouteStep(mr, "")
	ctx := makeStepCtxWithURL("http://localhost/search")
	if err := step.Run(ctx); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if mr.gotURL == nil || mr.gotURL.Path != "search" {
		t.Errorf("router received path %q, want %q", mr.gotURL.Path, "search")
	}
}
