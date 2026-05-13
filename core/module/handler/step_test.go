package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// mockSignValidatorBasic is a simple SignValidator that always returns nil or a preset error.
type mockSignValidatorBasic struct {
	validateErr error
}

func (m *mockSignValidatorBasic) Validate(_ context.Context, _ []byte, _ string, _ string) error {
	return m.validateErr
}
func (m *mockSignValidatorBasic) ValidateAck(_ context.Context, _ []byte, _, _, _ string) error {
	return nil
}

// mockKMBasic is a simple KeyManager that returns a preset public key or error.
type mockKMBasic struct {
	publicKey string
	lookupErr error
}

func (m *mockKMBasic) GenerateKeyset() (*model.Keyset, error)                          { return nil, nil }
func (m *mockKMBasic) InsertKeyset(_ context.Context, _ string, _ *model.Keyset) error { return nil }
func (m *mockKMBasic) DeleteKeyset(_ context.Context, _ string) error                  { return nil }
func (m *mockKMBasic) Keyset(_ context.Context, _ string) (*model.Keyset, error)       { return nil, nil }
func (m *mockKMBasic) LookupNPKeys(_ context.Context, _, _ string) (string, string, error) {
	return m.publicKey, "", m.lookupErr
}

// mockOutboundStore is an in-memory outboundAuthStore for testing.
type mockOutboundStore struct {
	stored   []outboundAuthEntry
	storeErr error
	entries  map[string]*outboundAuthEntry // key: "messageID:action"
	getErr   error
}

func newMockOutboundStore() *mockOutboundStore {
	return &mockOutboundStore{entries: map[string]*outboundAuthEntry{}}
}

func (m *mockOutboundStore) Store(_ context.Context, entry outboundAuthEntry) error {
	if m.storeErr != nil {
		return m.storeErr
	}
	m.stored = append(m.stored, entry)
	m.entries[entry.MessageID+":"+entry.Action] = &entry
	return nil
}

func (m *mockOutboundStore) GetByMessageID(_ context.Context, messageID, action string) (*outboundAuthEntry, error) {
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

// solicitedCallbackAuthHeader builds a Signature header that declares
// "request-signature" in the headers attribute — identifying a solicited callback.
func solicitedCallbackAuthHeader(subscriberID, requestSigValue string) string {
	return `Signature keyId="` + subscriberID + `|key-1|ed25519",algorithm="ed25519",` +
		`created="1700000000",expires="1700003600",` +
		`headers="(created) (expires) digest request-signature",` +
		`signature="callbackSig==",` +
		`request-signature="` + requestSigValue + `"`
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

func TestNewValidateSignStep_NilOutboundStore_OK(t *testing.T) {
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

// ---------------------------------------------------------------------------
// extractRequestSignatureValue unit tests
// ---------------------------------------------------------------------------

func TestExtractRequestSignatureValue_Present(t *testing.T) {
	header := solicitedCallbackAuthHeader("bpp.example.com", "outboundSig==")
	got := extractRequestSignatureValue(header)
	if got != "outboundSig==" {
		t.Errorf("extractRequestSignatureValue() = %q, want %q", got, "outboundSig==")
	}
}

func TestExtractRequestSignatureValue_Absent(t *testing.T) {
	header := providerInitiatedAuthHeader("bpp.example.com")
	got := extractRequestSignatureValue(header)
	if got != "" {
		t.Errorf("extractRequestSignatureValue() = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// validateRequestSignatureChain tests
// ---------------------------------------------------------------------------

const onSearchBody = `{"context":{"action":"on_search","messageId":"msg-chain-001","version":"2.0.0"}}`

func TestValidateRequestSignatureChain_NilStore_Skips(t *testing.T) {
	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, nil)

	vStep := step.(*validateSignStep)
	ctx := makeReceiverStepCtx("2.0.0", "msg-chain-001", "bap.example.com",
		solicitedCallbackAuthHeader("bpp.example.com", "storedSig=="), onSearchBody)

	if err := vStep.validateRequestSignatureChain(ctx); err != nil {
		t.Fatalf("expected nil when outboundStore is nil, got: %v", err)
	}
}

func TestValidateRequestSignatureChain_PreV2Version_Skips(t *testing.T) {
	store := newMockOutboundStore()
	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, store)

	vStep := step.(*validateSignStep)
	ctx := makeReceiverStepCtx("1.1.0", "msg-chain-002", "bap.example.com",
		solicitedCallbackAuthHeader("bpp.example.com", "storedSig=="), onSearchBody)

	if err := vStep.validateRequestSignatureChain(ctx); err != nil {
		t.Fatalf("expected nil for pre-v2 version, got: %v", err)
	}
}

func TestValidateRequestSignatureChain_ProviderInitiated_Skips(t *testing.T) {
	store := newMockOutboundStore()
	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, store)

	vStep := step.(*validateSignStep)
	// headers= without request-signature → provider-initiated
	ctx := makeReceiverStepCtx("2.0.0", "msg-chain-003", "bap.example.com",
		providerInitiatedAuthHeader("bpp.example.com"), onSearchBody)

	if err := vStep.validateRequestSignatureChain(ctx); err != nil {
		t.Fatalf("expected nil for provider-initiated callback, got: %v", err)
	}
}

func TestValidateRequestSignatureChain_ValidChain(t *testing.T) {
	store := newMockOutboundStore()
	_ = store.Store(context.Background(), outboundAuthEntry{
		MessageID: "msg-chain-004",
		Action:    "search", // stored without "on_" prefix
		Signature: "originalCallerSig==",
	})

	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, store)

	vStep := step.(*validateSignStep)
	// BPP callback Authorization includes request-signature matching what BAP sent.
	ctx := makeReceiverStepCtx("2.0.0", "msg-chain-004", "bap.example.com",
		solicitedCallbackAuthHeader("bpp.example.com", "originalCallerSig=="), onSearchBody)

	if err := vStep.validateRequestSignatureChain(ctx); err != nil {
		t.Fatalf("expected nil for valid chain, got: %v", err)
	}
}

func TestValidateRequestSignatureChain_Mismatch_ReturnsError(t *testing.T) {
	store := newMockOutboundStore()
	_ = store.Store(context.Background(), outboundAuthEntry{
		MessageID: "msg-chain-005",
		Action:    "search",
		Signature: "originalCallerSig==",
	})

	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, store)

	vStep := step.(*validateSignStep)
	// BPP sends a different request-signature value — tampered.
	ctx := makeReceiverStepCtx("2.0.0", "msg-chain-005", "bap.example.com",
		solicitedCallbackAuthHeader("bpp.example.com", "tamperedSig=="), onSearchBody)

	if err := vStep.validateRequestSignatureChain(ctx); err == nil {
		t.Fatal("expected error for request-signature mismatch")
	}
}

func TestValidateRequestSignatureChain_NoEntry_ReturnsError(t *testing.T) {
	store := newMockOutboundStore() // empty — no stored entry

	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, store)

	vStep := step.(*validateSignStep)
	ctx := makeReceiverStepCtx("2.0.0", "msg-chain-006", "bap.example.com",
		solicitedCallbackAuthHeader("bpp.example.com", "anySig=="), onSearchBody)

	if err := vStep.validateRequestSignatureChain(ctx); err == nil {
		t.Fatal("expected error when no outbound entry found for message ID")
	}
}

func TestValidateRequestSignatureChain_StoreGetError_ReturnsError(t *testing.T) {
	store := newMockOutboundStore()
	store.getErr = errors.New("redis connection error")

	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, store)

	vStep := step.(*validateSignStep)
	ctx := makeReceiverStepCtx("2.0.0", "msg-chain-007", "bap.example.com",
		solicitedCallbackAuthHeader("bpp.example.com", "anySig=="), onSearchBody)

	if err := vStep.validateRequestSignatureChain(ctx); err == nil {
		t.Fatal("expected error when outboundStore.GetByMessageID returns error")
	}
}

// ---------------------------------------------------------------------------
// outboundStore.Store tests via ServeHTTP (integration-style, via mockOutboundStore)
// ---------------------------------------------------------------------------

// TestValidateSignStep_InitSteps_WithOutboundStore verifies that initSteps wires
// the outboundStore into the validateSign step without error.
func TestValidateSignStep_InitSteps_WithOutboundStore(t *testing.T) {
	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	store := newMockOutboundStore()

	h := &stdHandler{
		signValidator: sv,
		km:            km,
		outboundStore: store,
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
