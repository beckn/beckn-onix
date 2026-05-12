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

// mockKM satisfies definition.KeyManager for testing.
type mockKM struct {
	keyset *model.Keyset
	err    error
}

func (m *mockKM) GenerateKeyset() (*model.Keyset, error)                                       { return nil, nil }
func (m *mockKM) InsertKeyset(_ context.Context, _ string, _ *model.Keyset) error              { return nil }
func (m *mockKM) DeleteKeyset(_ context.Context, _ string) error                               { return nil }
func (m *mockKM) LookupNPKeys(_ context.Context, _, _ string) (string, string, error)          { return "", "", nil }
func (m *mockKM) Keyset(_ context.Context, _ string) (*model.Keyset, error) {
	return m.keyset, m.err
}

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

func TestAckSignerStep_LTS_SetsSignatureHeader(t *testing.T) {
	signer := &mockSigner{returnSig: "base64sig=="}
	km := &mockKM{keyset: &model.Keyset{UniqueKeyID: "key-1", SigningPrivate: "priv"}}

	step, err := newAckSignerStep(signer, km)
	if err != nil {
		t.Fatalf("newAckSignerStep() unexpected error: %v", err)
	}

	ctx := makeStepCtx("2.0.0", "msg-001", "bpp.example.com", "inboundSig==")
	if err := step.RunOnResponse(ctx); err != nil {
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

	if err := step.RunOnResponse(ctx); err != nil {
		t.Fatalf("RunOnResponse() unexpected error: %v", err)
	}
	if !signer.signAckCalled {
		t.Error("expected SignAck to be called for future version")
	}
	if ctx.RespHeader.Get("Signature") == "" {
		t.Error("expected Signature header to be set for future version")
	}
}

func TestAckSignerStep_LegacyVersion_Skips(t *testing.T) {
	signer := &mockSigner{}
	km := &mockKM{keyset: &model.Keyset{}}

	step, _ := newAckSignerStep(signer, km)
	ctx := makeStepCtx("1.1.0", "msg-legacy", "sub.example.com", "")

	if err := step.RunOnResponse(ctx); err != nil {
		t.Fatalf("RunOnResponse() unexpected error: %v", err)
	}
	if signer.signAckCalled {
		t.Error("expected SignAck NOT to be called for legacy version")
	}
	if ctx.RespHeader.Get("Signature") != "" {
		t.Error("expected no Signature header for legacy version")
	}
}

func TestAckSignerStep_EmptyVersion_Skips(t *testing.T) {
	signer := &mockSigner{}
	km := &mockKM{keyset: &model.Keyset{}}

	step, _ := newAckSignerStep(signer, km)
	ctx := makeStepCtx("", "msg-empty", "sub.example.com", "")

	if err := step.RunOnResponse(ctx); err != nil {
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

	if err := step.RunOnResponse(ctx); err == nil {
		t.Fatal("expected error when KeyManager fails")
	}
}

func TestAckSignerStep_SignerError_ReturnsError(t *testing.T) {
	signer := &mockSigner{signAckErr: errors.New("sign failed")}
	km := &mockKM{keyset: &model.Keyset{UniqueKeyID: "key-1", SigningPrivate: "priv"}}

	step, _ := newAckSignerStep(signer, km)
	ctx := makeStepCtx("2.0.0", "msg-003", "sub.example.com", "sig==")

	if err := step.RunOnResponse(ctx); err == nil {
		t.Fatal("expected error when signer fails")
	}
}

func TestAckSignerStep_MissingSubID_ReturnsError(t *testing.T) {
	signer := &mockSigner{}
	km := &mockKM{keyset: &model.Keyset{}}

	step, _ := newAckSignerStep(signer, km)
	ctx := makeStepCtx("2.0.0", "msg-004", "", "sig==") // empty subID

	if err := step.RunOnResponse(ctx); err == nil {
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
