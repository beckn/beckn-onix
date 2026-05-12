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

// mockSignValidatorWithAck extends mockSignValidator with ValidateAck support.
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

// mockKMWithLookup extends mockKM with LookupNPKeys support.
type mockKMWithLookup struct {
	mockKM
	publicKey string
	lookupErr error
}

func (m *mockKMWithLookup) LookupNPKeys(_ context.Context, _, _ string) (string, string, error) {
	return m.publicKey, "", m.lookupErr
}

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

func TestValidateAckSignatureStep_LegacyVersion_Skips(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("1.1.0", "msg-003", "bap.example.com", "outboundAuth==")
	resp := makeAckResponse(`{"message":{"ack":{"status":"ACK"}}}`, "")

	if err := step.RunOnResponse(ctx, resp); err != nil {
		t.Fatalf("RunOnResponse() unexpected error on legacy version: %v", err)
	}
	if sv.validateAckCalled {
		t.Error("expected ValidateAck NOT to be called for legacy version")
	}
}

func TestValidateAckSignatureStep_MissingSignatureHeader_ReturnsError(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("2.0.0", "msg-004", "bap.example.com", "outboundAuth==")
	resp := makeAckResponse(`{"message":{"status":"ACK"}}`, "") // no Signature header

	if err := step.RunOnResponse(ctx, resp); err == nil {
		t.Fatal("expected error for missing Signature header on v2 ACK")
	}
}

func TestValidateAckSignatureStep_InvalidSignatureHeader_ReturnsError(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("2.0.0", "msg-005", "bap.example.com", "outboundAuth==")
	resp := makeAckResponse(`{"message":{"status":"ACK"}}`, "malformed-header-no-keyId")

	if err := step.RunOnResponse(ctx, resp); err == nil {
		t.Fatal("expected error for malformed Signature header")
	}
}

func TestValidateAckSignatureStep_KeyManagerError_ReturnsError(t *testing.T) {
	sv := &mockSignValidatorWithAck{}
	km := &mockKMWithLookup{lookupErr: errors.New("registry unavailable")}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("2.0.0", "msg-006", "bap.example.com", "outboundAuth==")
	resp := makeAckResponse(`{"message":{"status":"ACK"}}`, testSigHeader)

	if err := step.RunOnResponse(ctx, resp); err == nil {
		t.Fatal("expected error when KeyManager lookup fails")
	}
}

func TestValidateAckSignatureStep_ValidatorError_ReturnsError(t *testing.T) {
	sv := &mockSignValidatorWithAck{validateAckErr: errors.New("signature mismatch")}
	km := &mockKMWithLookup{publicKey: "pubKey=="}

	step, _ := newValidateAckSignatureStep(sv, km)
	ctx := makeCallerStepCtx("2.0.0", "msg-007", "bap.example.com", "outboundAuth==")
	resp := makeAckResponse(`{"message":{"status":"ACK"}}`, testSigHeader)

	if err := step.RunOnResponse(ctx, resp); err == nil {
		t.Fatal("expected error when ValidateAck fails")
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
