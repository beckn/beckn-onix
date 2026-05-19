package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// mockSigner satisfies definition.Signer for testing.
type mockSigner struct {
	signAckCalled bool
	signAckErr    error
	returnSig     string
}

func (m *mockSigner) Sign(_ context.Context, _ []byte, _ string, _, _ int64) (string, error) {
	return "", nil
}

func (m *mockSigner) SignAck(_ context.Context, _ []byte, _ string, _ string, _, _ int64) (string, error) {
	m.signAckCalled = true
	if m.signAckErr != nil {
		return "", m.signAckErr
	}
	return m.returnSig, nil
}

// mockKM satisfies definition.KeyManager for testing (keyset-focused).
type mockKM struct {
	keyset *model.Keyset
	err    error
}

func (m *mockKM) GenerateKeyset() (*model.Keyset, error)                              { return nil, nil }
func (m *mockKM) InsertKeyset(_ context.Context, _ string, _ *model.Keyset) error    { return nil }
func (m *mockKM) DeleteKeyset(_ context.Context, _ string) error                     { return nil }
func (m *mockKM) LookupNPKeys(_ context.Context, _, _ string) (string, string, error) { return "", "", nil }
func (m *mockKM) Keyset(_ context.Context, _ string) (*model.Keyset, error) {
	return m.keyset, m.err
}

// mockSignValidatorWithAck satisfies definition.SignValidator with ValidateAck support.
type mockSignValidatorWithAck struct {
	validateAckCalled bool
	validateAckErr    error
}

func (m *mockSignValidatorWithAck) Validate(_ context.Context, _ []byte, _ string, _ string) error {
	return nil
}

func (m *mockSignValidatorWithAck) ValidateAck(_ context.Context, _ []byte, _, _, _ string) error {
	m.validateAckCalled = true
	return m.validateAckErr
}

// mockKMWithLookup extends mockKM with a controllable LookupNPKeys response.
type mockKMWithLookup struct {
	mockKM
	publicKey string
	lookupErr error
}

func (m *mockKMWithLookup) LookupNPKeys(_ context.Context, _, _ string) (string, string, error) {
	return m.publicKey, "", m.lookupErr
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeStepCtx builds a minimal StepContext for ackSigner tests.
func makeStepCtx(protocolVersion, messageID, subID, authSig string) *model.StepContext {
	ctx := context.WithValue(context.Background(), model.ContextKeyProtocolVersion, protocolVersion)
	ctx = context.WithValue(ctx, model.ContextKeyMsgID, messageID)
	return &model.StepContext{
		Context:              ctx,
		ProtocolVersion:      protocolVersion,
		MessageID:            messageID,
		SubID:                subID,
		InboundAuthSignature: authSig,
		RespHeader:           http.Header{},
	}
}

// makeCallerStepCtx builds a StepContext for validateAckSign tests (Caller path).
func makeCallerStepCtx(protocolVersion, messageID, subID, outboundAuth string) *model.StepContext {
	ctx := context.WithValue(context.Background(), model.ContextKeyProtocolVersion, protocolVersion)
	ctx = context.WithValue(ctx, model.ContextKeyMsgID, messageID)
	req, _ := http.NewRequest(http.MethodPost, "/bap/caller/", strings.NewReader(`{}`))
	if outboundAuth != "" {
		req.Header.Set(model.AuthHeaderSubscriber, outboundAuth)
	}
	return &model.StepContext{
		Context:         ctx,
		Request:         req,
		ProtocolVersion: protocolVersion,
		MessageID:       messageID,
		SubID:           subID,
		RespHeader:      http.Header{},
	}
}

// makeAckResponse builds a synthetic upstream ACK *http.Response for testing.
func makeAckResponse(body string, sig string) *http.Response {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	if sig != "" {
		resp.Header.Set("Signature", sig)
	}
	return resp
}

const testSigHeader = `Signature keyId="bpp.example.com|key-1|ed25519",algorithm="ed25519",created="1700000000",expires="1700000300",headers="(created) (expires) digest request-signature",signature="dGVzdA=="`

// ---------------------------------------------------------------------------
// ackSignerStep tests
// ---------------------------------------------------------------------------

func TestAckSignerStep_V2_SetsSignatureHeader(t *testing.T) {
	signer := &mockSigner{returnSig: "base64sig=="}
	km := &mockKM{keyset: &model.Keyset{UniqueKeyID: "key-1", SigningPrivate: "priv"}}

	step, err := newAckSignerStep(signer, km)
	if err != nil {
		t.Fatalf("newAckSignerStep() unexpected error: %v", err)
	}

	ctx := makeStepCtx("2.0.0", "msg-001", "bpp.example.com", "inboundSig==")
	if err := step.RunOnResponse(ctx, nil); err != nil {
		t.Fatalf("RunOnResponse() unexpected error: %v", err)
	}

	if !signer.signAckCalled {
		t.Error("expected SignAck to be called")
	}
	sig := ctx.RespHeader.Get("Signature")
	if sig == "" {
		t.Fatal("expected Signature header to be set")
	}
	if !strings.Contains(sig, "bpp.example.com|key-1|ed25519") {
		t.Errorf("Signature header missing keyId: %s", sig)
	}
	if !strings.Contains(sig, `signature="base64sig=="`) {
		t.Errorf("Signature header missing signature value: %s", sig)
	}
	if !strings.Contains(sig, `headers="(created) (expires) digest request-signature"`) {
		t.Errorf("Signature header missing headers attribute: %s", sig)
	}
}

func TestAckSignerStep_FutureVersion_SetsSignatureHeader(t *testing.T) {
	signer := &mockSigner{returnSig: "futureSig=="}
	km := &mockKM{keyset: &model.Keyset{UniqueKeyID: "key-2", SigningPrivate: "priv"}}

	step, _ := newAckSignerStep(signer, km)
	ctx := makeStepCtx("3.0.0", "msg-future", "bap.example.com", "")

	if err := step.RunOnResponse(ctx, nil); err != nil {
		t.Fatalf("RunOnResponse() unexpected error: %v", err)
	}
	if !signer.signAckCalled {
		t.Error("expected SignAck to be called for future version")
	}
	if ctx.RespHeader.Get("Signature") == "" {
		t.Error("expected Signature header to be set for future version")
	}
}

func TestAckSignerStep_PreV2Version_Skips(t *testing.T) {
	signer := &mockSigner{}
	km := &mockKM{keyset: &model.Keyset{}}

	step, _ := newAckSignerStep(signer, km)
	ctx := makeStepCtx("1.1.0", "msg-pre-v2", "sub.example.com", "")

	if err := step.RunOnResponse(ctx, nil); err != nil {
		t.Fatalf("RunOnResponse() unexpected error: %v", err)
	}
	if signer.signAckCalled {
		t.Error("expected SignAck NOT to be called for pre-v2 version")
	}
	if ctx.RespHeader.Get("Signature") != "" {
		t.Error("expected no Signature header for pre-v2 version")
	}
}

func TestAckSignerStep_EmptyVersion_Skips(t *testing.T) {
	signer := &mockSigner{}
	km := &mockKM{keyset: &model.Keyset{}}

	step, _ := newAckSignerStep(signer, km)
	ctx := makeStepCtx("", "msg-empty", "sub.example.com", "")

	if err := step.RunOnResponse(ctx, nil); err != nil {
		t.Fatalf("RunOnResponse() unexpected error: %v", err)
	}
	if signer.signAckCalled {
		t.Error("expected SignAck NOT to be called for empty version")
	}
}

func TestAckSignerStep_KeyManagerError_ReturnsError(t *testing.T) {
	signer := &mockSigner{}
	km := &mockKM{err: errors.New("vault unavailable")}

	step, _ := newAckSignerStep(signer, km)
	ctx := makeStepCtx("2.0.0", "msg-002", "sub.example.com", "sig==")

	if err := step.RunOnResponse(ctx, nil); err == nil {
		t.Fatal("expected error when KeyManager fails")
	}
}

func TestAckSignerStep_SignerError_ReturnsError(t *testing.T) {
	signer := &mockSigner{signAckErr: errors.New("sign failed")}
	km := &mockKM{keyset: &model.Keyset{UniqueKeyID: "key-1", SigningPrivate: "priv"}}

	step, _ := newAckSignerStep(signer, km)
	ctx := makeStepCtx("2.0.0", "msg-003", "sub.example.com", "sig==")

	if err := step.RunOnResponse(ctx, nil); err == nil {
		t.Fatal("expected error when signer fails")
	}
}

func TestAckSignerStep_MissingSubID_ReturnsError(t *testing.T) {
	signer := &mockSigner{}
	km := &mockKM{keyset: &model.Keyset{}}

	step, _ := newAckSignerStep(signer, km)
	ctx := makeStepCtx("2.0.0", "msg-004", "", "sig==") // empty subID

	if err := step.RunOnResponse(ctx, nil); err == nil {
		t.Fatal("expected error when SubID is empty")
	}
}

func TestNewAckSignerStep_NilSigner_ReturnsError(t *testing.T) {
	km := &mockKM{}
	if _, err := newAckSignerStep(nil, km); err == nil {
		t.Fatal("expected error for nil signer")
	}
}

func TestNewAckSignerStep_NilKM_ReturnsError(t *testing.T) {
	signer := &mockSigner{}
	if _, err := newAckSignerStep(signer, nil); err == nil {
		t.Fatal("expected error for nil key manager")
	}
}

func TestAckSignerStep_URLRoutingPath_409_SetsSignatureOnResponse(t *testing.T) {
	// 409 AckNoCallback: app decides, ONIX relays. ackSigner must still sign
	// the response so the caller can verify the Signature header regardless of
	// status code.
	signer := &mockSigner{returnSig: "sig409=="}
	km := &mockKM{keyset: &model.Keyset{UniqueKeyID: "key-409", SigningPrivate: "priv"}}

	step, err := newAckSignerStep(signer, km)
	if err != nil {
		t.Fatalf("newAckSignerStep() unexpected error: %v", err)
	}

	ctx := makeStepCtx("2.0.0", "msg-409", "bpp.example.com", "inboundSig==")

	body := `{"message":{"status":"ACK","error":{"code":"40901","message":"no matching catalog"}}}`
	resp := &http.Response{
		StatusCode: http.StatusConflict,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	if err := step.RunOnResponse(ctx, resp); err != nil {
		t.Fatalf("RunOnResponse() unexpected error on 409: %v", err)
	}

	if !signer.signAckCalled {
		t.Error("expected SignAck to be called for 409 response")
	}
	if resp.Header.Get("Signature") == "" {
		t.Fatal("expected Signature header on 409 response")
	}
	// Body must be restored so ReverseProxy forwards the original 409 body.
	restored, _ := io.ReadAll(resp.Body)
	if string(restored) != body {
		t.Errorf("resp.Body not restored: got %q, want %q", restored, body)
	}
}

func TestAckSignerStep_URLRoutingPath_SetsSignatureOnResponse(t *testing.T) {
	signer := &mockSigner{returnSig: "urlSig=="}
	km := &mockKM{keyset: &model.Keyset{UniqueKeyID: "key-url", SigningPrivate: "priv"}}

	step, err := newAckSignerStep(signer, km)
	if err != nil {
		t.Fatalf("newAckSignerStep() unexpected error: %v", err)
	}

	ctx := makeStepCtx("2.0.0", "msg-url", "bpp.url.com", "inboundSig==")

	body := `{"message":{"ack":{"status":"ACK"}}}`
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(strings.NewReader(body)),
	}

	if err := step.RunOnResponse(ctx, resp); err != nil {
		t.Fatalf("RunOnResponse() unexpected error: %v", err)
	}

	if !signer.signAckCalled {
		t.Error("expected SignAck to be called on URL-routing path")
	}
	// Signature header must be on the upstream response, not ctx.RespHeader.
	if resp.Header.Get("Signature") == "" {
		t.Fatal("expected Signature header on upstream response")
	}
	if ctx.RespHeader.Get("Signature") != "" {
		t.Error("expected Signature header NOT set on ctx.RespHeader for URL-routing path")
	}
	// Body must be restored so the proxy can forward it.
	restored, _ := io.ReadAll(resp.Body)
	if string(restored) != body {
		t.Errorf("resp.Body not restored: got %q, want %q", restored, body)
	}
}

func TestInitSteps_SignAckAppendsToResponseSteps(t *testing.T) {
	signer := &mockSigner{}
	km := &mockKM{keyset: &model.Keyset{}}

	h := &stdHandler{
		signer:        signer,
		km:            km,
		steps:         []definition.Step{},
		responseSteps: []definition.ResponseStep{},
	}

	cfg := &Config{Steps: []string{"signAck"}}
	if err := h.initSteps(context.Background(), noopPluginManager{}, cfg); err != nil {
		t.Fatalf("initSteps() unexpected error: %v", err)
	}
	if len(h.steps) != 0 {
		t.Errorf("expected 0 inbound steps, got %d", len(h.steps))
	}
	if len(h.responseSteps) != 1 {
		t.Errorf("expected 1 response step, got %d", len(h.responseSteps))
	}
}

// ---------------------------------------------------------------------------
// validateAckSignatureStep tests
// ---------------------------------------------------------------------------

func TestValidateAckSignatureStep_V2_ValidSignature(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, err := newValidateAckSignatureStep(sv, km)
	if err != nil {
		t.Fatalf("newValidateAckSignatureStep() unexpected error: %v", err)
	}

	ctx := makeCallerStepCtx("2.0.0", "msg-001", "bap.example.com", `Signature keyId="bap.example.com|key-1|ed25519",signature="outboundSig=="`)
	resp := makeAckResponse(`{"message":{"status":"ACK","messageId":"msg-001"}}`, testSigHeader)

	if err := step.RunOnResponse(ctx, resp); err != nil {
		t.Fatalf("RunOnResponse() unexpected error: %v", err)
	}

	if !sv.validateAckCalled {
		t.Error("expected ValidateAck to be called")
	}
	// Body must be restored.
	restored, _ := io.ReadAll(resp.Body)
	if len(restored) == 0 {
		t.Error("expected resp.Body to be restored after read")
	}
}

func TestValidateAckSignatureStep_PublisherPath_Skips(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("2.0.0", "msg-002", "bap.example.com", "outboundAuth==")

	// resp=nil is the publisher path.
	if err := step.RunOnResponse(ctx, nil); err != nil {
		t.Fatalf("RunOnResponse() unexpected error on publisher path: %v", err)
	}
	if sv.validateAckCalled {
		t.Error("expected ValidateAck NOT to be called on publisher path")
	}
}

func TestValidateAckSignatureStep_PreV2Version_Skips(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("1.1.0", "msg-003", "bap.example.com", "outboundAuth==")
	resp := makeAckResponse(`{"message":{"ack":{"status":"ACK"}}}`, "")

	if err := step.RunOnResponse(ctx, resp); err != nil {
		t.Fatalf("RunOnResponse() unexpected error on pre-v2 version: %v", err)
	}
	if sv.validateAckCalled {
		t.Error("expected ValidateAck NOT to be called for pre-v2 version")
	}
}

// TestValidateAckSignatureStep_MissingSignatureHeader_Degrades verifies that a
// missing Signature header produces a degraded-trust warning but does NOT
// reject the response (NFH-007 CON-004-02 SHOULD NOT invalidate the transaction).
func TestValidateAckSignatureStep_MissingSignatureHeader_Degrades(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("2.0.0", "msg-004", "bap.example.com", "outboundAuth==")
	resp := makeAckResponse(`{"message":{"status":"ACK"}}`, "") // no Signature header

	if err := step.RunOnResponse(ctx, resp); err != nil {
		t.Fatalf("expected nil (degrade) for missing Signature header, got: %v", err)
	}
	if sv.validateAckCalled {
		t.Error("expected ValidateAck NOT to be called when Signature header is absent")
	}
}

// TestValidateAckSignatureStep_InvalidSignatureHeader_Degrades verifies that a
// malformed Signature header degrades (warning) rather than rejecting.
func TestValidateAckSignatureStep_InvalidSignatureHeader_Degrades(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("2.0.0", "msg-005", "bap.example.com", "outboundAuth==")
	resp := makeAckResponse(`{"message":{"status":"ACK"}}`, "malformed-header-no-keyId")

	if err := step.RunOnResponse(ctx, resp); err != nil {
		t.Fatalf("expected nil (degrade) for malformed Signature header, got: %v", err)
	}
}

// TestValidateAckSignatureStep_KeyManagerError_Degrades verifies that a key
// manager lookup failure degrades (warning) rather than rejecting.
func TestValidateAckSignatureStep_KeyManagerError_Degrades(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{lookupErr: errors.New("registry unavailable")}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("2.0.0", "msg-006", "bap.example.com", "outboundAuth==")
	resp := makeAckResponse(`{"message":{"status":"ACK"}}`, testSigHeader)

	if err := step.RunOnResponse(ctx, resp); err != nil {
		t.Fatalf("expected nil (degrade) when KeyManager lookup fails, got: %v", err)
	}
}

// TestValidateAckSignatureStep_ValidatorError_Degrades verifies that a
// cryptographic verification failure degrades (warning) rather than rejecting.
func TestValidateAckSignatureStep_ValidatorError_Degrades(t *testing.T) {
	sv := &mockSignValidatorWithAck{validateAckErr: errors.New("signature mismatch")}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("2.0.0", "msg-007", "bap.example.com", "outboundAuth==")
	resp := makeAckResponse(`{"message":{"status":"ACK"}}`, testSigHeader)

	if err := step.RunOnResponse(ctx, resp); err != nil {
		t.Fatalf("expected nil (degrade) when ValidateAck fails, got: %v", err)
	}
}

// TestValidateAckSignatureStep_MissingSignature_AllStatusCodes_Degrades verifies
// that a missing Signature header on ANY status code (200, 4xx, 409, 500) causes
// a degrade warning — not a rejection. Per NFH-007 CON-004-02 every synchronous
// response MUST carry a Signature; per conformance SHOULD NOT invalidate the
// transaction when it is absent.
func TestValidateAckSignatureStep_MissingSignature_AllStatusCodes_Degrades(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("2.0.0", "msg-nack-401", "bap.example.com", "outboundAuth==")

	codes := []int{
		http.StatusOK,
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusNotFound,
		http.StatusConflict,       // 409 AckNoCallback
		http.StatusInternalServerError,
	}
	for _, code := range codes {
		resp := &http.Response{
			StatusCode: code,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"message":{"ack":{"status":"NACK"}}}`)),
		}
		if err := step.RunOnResponse(ctx, resp); err != nil {
			t.Errorf("status %d: expected nil (degrade), got %v", code, err)
		}
		if sv.validateAckCalled {
			t.Errorf("status %d: expected ValidateAck NOT to be called (no Signature header)", code)
		}
		sv.validateAckCalled = false
	}
}

// TestValidateAckSignatureStep_ValidSignature_AllStatusCodes verifies that when
// a Signature header IS present, validation is attempted for every status code.
func TestValidateAckSignatureStep_ValidSignature_AllStatusCodes(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("2.0.0", "msg-409", "bap.example.com", `Signature keyId="bap.example.com|key-1|ed25519",signature="outSig=="`)

	codes := []int{
		http.StatusOK,
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusConflict, // 409 AckNoCallback
		http.StatusInternalServerError,
	}
	for _, code := range codes {
		sv.validateAckCalled = false
		resp := &http.Response{
			StatusCode: code,
			Header:     http.Header{"Signature": []string{testSigHeader}},
			Body:       io.NopCloser(strings.NewReader(`{"message":{"status":"ACK"}}`)),
		}
		if err := step.RunOnResponse(ctx, resp); err != nil {
			t.Errorf("status %d: unexpected error: %v", code, err)
		}
		if !sv.validateAckCalled {
			t.Errorf("status %d: expected ValidateAck to be called when Signature header is present", code)
		}
	}
}

// TestAckSignerStep_UpstreamApp_4xx_Signs verifies that ackSignerStep signs all
// upstream responses regardless of status code — per NFH-007 CON-004-02 there
// is no status-code filter on the Receiver signing path.
func TestAckSignerStep_UpstreamApp_4xx_Signs(t *testing.T) {
	signer := &mockSigner{returnSig: "nackSig=="}
	km := &mockKM{keyset: &model.Keyset{UniqueKeyID: "key-nack", SigningPrivate: "priv"}}

	step, _ := newAckSignerStep(signer, km)
	ctx := makeStepCtx("2.0.0", "msg-nack", "bpp.example.com", "inboundSig==")

	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"message":{"ack":{"status":"NACK"}}}`)),
	}
	if err := step.RunOnResponse(ctx, resp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !signer.signAckCalled {
		t.Error("expected SignAck to be called even for upstream 4xx")
	}
	if resp.Header.Get("Signature") == "" {
		t.Error("expected Signature header to be set on upstream 4xx response")
	}
}

// TestAckSignerStep_SignBodyAndSetHeader verifies that signBodyAndSetHeader
// sets the Signature response header on ctx.RespHeader — the primitive used to
// sign pipeline-NACK responses from ServeHTTP (NFH-007 CON-004-02).
func TestAckSignerStep_SignBodyAndSetHeader(t *testing.T) {
	signer := &mockSigner{returnSig: "nackBodySig=="}
	km := &mockKM{keyset: &model.Keyset{UniqueKeyID: "key-1", SigningPrivate: "priv"}}

	concreteStep := &ackSignerStep{signer: signer, km: km}
	ctx := makeStepCtx("2.0.0", "msg-pnack", "bpp.example.com", "inboundSig==")

	nackBody := []byte(`{"message":{"status":"NACK","messageId":"msg-pnack"}}`)
	if err := concreteStep.signBodyAndSetHeader(ctx, nackBody); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !signer.signAckCalled {
		t.Error("expected SignAck to be called")
	}
	sig := ctx.RespHeader.Get("Signature")
	if sig == "" {
		t.Error("expected Signature header to be set on ctx.RespHeader")
	}
	if !strings.Contains(sig, "nackBodySig==") {
		t.Errorf("Signature header does not contain expected sig value: %s", sig)
	}
}

func TestNewValidateAckSignatureStep_NilSignValidator_ReturnsError(t *testing.T) {
	km := &mockKMWithLookup{}
	if _, err := newValidateAckSignatureStep(nil, km); err == nil {
		t.Fatal("expected error for nil SignValidator")
	}
}

func TestNewValidateAckSignatureStep_NilKM_ReturnsError(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	if _, err := newValidateAckSignatureStep(sv, nil); err == nil {
		t.Fatal("expected error for nil KeyManager")
	}
}

func TestInitSteps_ValidateAckSignAppendsToResponseSteps(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{}

	h := &stdHandler{
		signValidator: sv,
		km:            km,
		steps:         []definition.Step{},
		responseSteps: []definition.ResponseStep{},
	}

	cfg := &Config{Steps: []string{"validateAckSign"}}
	if err := h.initSteps(context.Background(), noopPluginManager{}, cfg); err != nil {
		t.Fatalf("initSteps() unexpected error: %v", err)
	}
	if len(h.steps) != 0 {
		t.Errorf("expected 0 inbound steps, got %d", len(h.steps))
	}
	if len(h.responseSteps) != 1 {
		t.Errorf("expected 1 response step, got %d", len(h.responseSteps))
	}
}
