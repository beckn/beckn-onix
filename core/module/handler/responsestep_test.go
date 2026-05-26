package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// ---------------------------------------------------------------------------
// Response writer helpers
// ---------------------------------------------------------------------------

type errorResponseWriter struct{}

func (e *errorResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("write error")
}
func (e *errorResponseWriter) WriteHeader(statusCode int) {}
func (e *errorResponseWriter) Header() http.Header {
	return http.Header{}
}

// badMessage forces a JSON marshal error.
type badMessage struct{}

func (b *badMessage) MarshalJSON() ([]byte, error) {
	return nil, errors.New("marshal error")
}

// v2Ctx returns a context with protocol version v2 and the given messageID.
func v2Ctx(msgID string) context.Context {
	ctx := context.WithValue(context.Background(), model.ContextKeyProtocolVersion, model.ProtocolVersionV2)
	return context.WithValue(ctx, model.ContextKeyMsgID, msgID)
}

func compareJSON(expected, actual map[string]interface{}) bool {
	expectedBytes, _ := json.Marshal(expected)
	actualBytes, _ := json.Marshal(actual)
	return bytes.Equal(expectedBytes, actualBytes)
}

// ---------------------------------------------------------------------------
// sendAck / sendNack / nack tests
// ---------------------------------------------------------------------------

func TestSendAck(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected string
	}{
		{
			name:     "pre-v2 — no protocol version",
			ctx:      context.Background(),
			expected: `{"message":{"ack":{"status":"ACK"}}}`,
		},
		{
			name:     "pre-v2 — version below 2.0.0",
			ctx:      context.WithValue(context.Background(), model.ContextKeyProtocolVersion, "1.1.0"),
			expected: `{"message":{"ack":{"status":"ACK"}}}`,
		},
		{
			name: "v2.0.0 — includes messageId",
			ctx: func() context.Context {
				ctx := context.WithValue(context.Background(), model.ContextKeyProtocolVersion, "2.0.0")
				return context.WithValue(ctx, model.ContextKeyMsgID, "550e8400-e29b-41d4-a716-446655440000")
			}(),
			expected: `{"message":{"status":"ACK","messageId":"550e8400-e29b-41d4-a716-446655440000"}}`,
		},
		{
			name:     "v2.0.0 — empty messageId omitted",
			ctx:      context.WithValue(context.Background(), model.ContextKeyProtocolVersion, "2.0.0"),
			expected: `{"message":{"status":"ACK"}}`,
		},
		{
			name: "future v2.1.0 — uses v2 envelope",
			ctx: func() context.Context {
				ctx := context.WithValue(context.Background(), model.ContextKeyProtocolVersion, "2.1.0")
				return context.WithValue(ctx, model.ContextKeyMsgID, "future-msg-id")
			}(),
			expected: `{"message":{"status":"ACK","messageId":"future-msg-id"}}`,
		},
		{
			name: "future v3.0.0 — uses v2 envelope",
			ctx: func() context.Context {
				ctx := context.WithValue(context.Background(), model.ContextKeyProtocolVersion, "3.0.0")
				return context.WithValue(ctx, model.ContextKeyMsgID, "v3-msg-id")
			}(),
			expected: `{"message":{"status":"ACK","messageId":"v3-msg-id"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			sendAck(tt.ctx, rr)

			if rr.Code != http.StatusOK {
				t.Errorf("wanted status code %d, got %d", http.StatusOK, rr.Code)
			}
			if rr.Body.String() != tt.expected {
				t.Errorf("body = %s, want %s", rr.Body.String(), tt.expected)
			}
		})
	}
}

func TestSendAck_WriteError(t *testing.T) {
	w := &errorResponseWriter{}
	sendAck(context.Background(), w)
}

func TestSendNack(t *testing.T) {
	ctx := context.WithValue(context.Background(), model.ContextKeyMsgID, "123456")

	tests := []struct {
		name     string
		err      error
		expected string
		status   int
	}{
		{
			name: "SchemaValidationErr",
			err: &model.SchemaValidationErr{
				Errors: []model.Error{
					{Paths: "/path1", Message: "Error 1"},
					{Paths: "/path2", Message: "Error 2"},
				},
			},
			status:   http.StatusBadRequest,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"Bad Request","paths":"/path1;/path2","message":"Error 1; Error 2"}}}`,
		},
		{
			name:     "SignValidationErr",
			err:      model.NewSignValidationErr(errors.New("signature invalid")),
			status:   http.StatusUnauthorized,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"Unauthorized","message":"Signature Validation Error: signature invalid"}}}`,
		},
		{
			name:     "BadReqErr",
			err:      model.NewBadReqErr(errors.New("bad request error")),
			status:   http.StatusBadRequest,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"Bad Request","message":"BAD Request: bad request error"}}}`,
		},
		{
			name:     "NotFoundErr",
			err:      model.NewNotFoundErr(errors.New("endpoint not found")),
			status:   http.StatusNotFound,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"Not Found","message":"Endpoint not found: endpoint not found"}}}`,
		},
		{
			name:     "InternalServerError",
			err:      errors.New("unexpected error"),
			status:   http.StatusInternalServerError,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"Internal Server Error","message":"Internal server error, MessageID: 123456"}}}`,
		},
	}

	v2Tests := []struct {
		name     string
		err      error
		expected string
		status   int
	}{
		{
			name:     "v2 SchemaValidationErr",
			err:      model.NewBadReqErr(errors.New("field missing")),
			status:   http.StatusBadRequest,
			expected: `{"message":{"status":"NACK","messageId":"msg-v2-1","error":{"code":"Bad Request","message":"BAD Request: field missing"}}}`,
		},
		{
			name:     "v2 SignValidationErr",
			err:      model.NewSignValidationErr(errors.New("signature expired")),
			status:   http.StatusUnauthorized,
			expected: `{"message":{"status":"NACK","messageId":"msg-v2-1","error":{"code":"Unauthorized","message":"Signature Validation Error: signature expired"}}}`,
		},
		{
			name: "v2 AckNoCallbackErr ACK status",
			err: model.NewAckNoCallbackErr(model.StatusACK, &model.Error{
				Code:    "NO_CATALOG",
				Message: "no matching catalog",
			}),
			status:   http.StatusAccepted,
			expected: `{"message":{"status":"ACK","messageId":"msg-v2-1","error":{"code":"NO_CATALOG","message":"no matching catalog"}}}`,
		},
		{
			name: "v2 AckNoCallbackErr NACK status",
			err: model.NewAckNoCallbackErr(model.StatusNACK, &model.Error{
				Code:    "PROVIDER_CLOSED",
				Message: "provider closed",
			}),
			status:   http.StatusAccepted,
			expected: `{"message":{"status":"NACK","messageId":"msg-v2-1","error":{"code":"PROVIDER_CLOSED","message":"provider closed"}}}`,
		},
	}

	v2Context := v2Ctx("msg-v2-1")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := http.NewRequest("GET", "/", nil)
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()

			sendNack(ctx, rr, tt.err)

			if rr.Code != tt.status {
				t.Errorf("wanted status code %d, got %d", tt.status, rr.Code)
			}

			var actual map[string]interface{}
			err = json.Unmarshal(rr.Body.Bytes(), &actual)
			if err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			var expected map[string]interface{}
			err = json.Unmarshal([]byte(tt.expected), &expected)
			if err != nil {
				t.Fatalf("failed to unmarshal expected response: %v", err)
			}

			if !compareJSON(expected, actual) {
				t.Errorf("err.Error() = %s, want %s", actual, expected)
			}
		})
	}

	for _, tt := range v2Tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			sendNack(v2Context, rr, tt.err)

			if rr.Code != tt.status {
				t.Errorf("wanted status code %d, got %d", tt.status, rr.Code)
			}

			var actual map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &actual); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}
			var expected map[string]interface{}
			if err := json.Unmarshal([]byte(tt.expected), &expected); err != nil {
				t.Fatalf("failed to unmarshal expected: %v", err)
			}
			if !compareJSON(expected, actual) {
				t.Errorf("body = %s, want %s", rr.Body.String(), tt.expected)
			}
		})
	}
}

// TestSendNack_AckNoCallback_PreV2_FallsThrough verifies that AckNoCallbackErr on a
// pre-v2 request is mapped to 500 Internal Server Error with the pre-v2 body shape,
// not a 202 with the v2 shape.
func TestSendNack_AckNoCallback_PreV2_FallsThrough(t *testing.T) {
	ctx := context.WithValue(context.Background(), model.ContextKeyMsgID, "pre-v2-msg")
	// No protocol version in context → pre-v2 path.

	rr := httptest.NewRecorder()
	err := model.NewAckNoCallbackErr(model.StatusACK, &model.Error{
		Code:    "NO_CATALOG",
		Message: "no matching catalog",
	})
	sendNack(ctx, rr, err)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("pre-v2 AckNoCallbackErr: want status 500, got %d", rr.Code)
	}
	// Body must use the pre-v2 envelope with NACK status, not the 202 ACK shape.
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	msg, _ := body["message"].(map[string]interface{})
	if msg == nil {
		t.Fatal("response body missing 'message' field")
	}
	// Pre-v2 shape uses message.ack.status, not message.status.
	if _, hasStatus := msg["status"]; hasStatus {
		t.Error("pre-v2 body must not have message.status (v2 field)")
	}
	ack, _ := msg["ack"].(map[string]interface{})
	if ack == nil {
		t.Fatal("pre-v2 body missing message.ack")
	}
	if ack["status"] != "NACK" {
		t.Errorf("pre-v2 body message.ack.status = %v, want NACK", ack["status"])
	}
}

// TestNackBytes_AckNoCallback verifies that nackBytes (used by signNackResponse
// before the 202 is written to the wire) produces the correct signed body for
// AckNoCallbackErr — same bytes that sendNack will subsequently write.
func TestNackBytes_AckNoCallback(t *testing.T) {
	ctx := v2Ctx("sign-msg-1")

	err := model.NewAckNoCallbackErr(model.StatusACK, &model.Error{
		Code:    "NO_CATALOG",
		Message: "no matching catalog",
	})
	body := nackBytes(ctx, err)

	var parsed map[string]interface{}
	if jerr := json.Unmarshal(body, &parsed); jerr != nil {
		t.Fatalf("nackBytes produced invalid JSON: %v", jerr)
	}
	msg, _ := parsed["message"].(map[string]interface{})
	if msg == nil {
		t.Fatal("nackBytes body missing 'message'")
	}
	if msg["status"] != "ACK" {
		t.Errorf("nackBytes message.status = %v, want ACK", msg["status"])
	}
	if msg["messageId"] != "sign-msg-1" {
		t.Errorf("nackBytes message.messageId = %v, want sign-msg-1", msg["messageId"])
	}
	errField, _ := msg["error"].(map[string]interface{})
	if errField == nil {
		t.Fatal("nackBytes body missing message.error")
	}
	if errField["code"] != "NO_CATALOG" {
		t.Errorf("nackBytes error.code = %v, want 40901", errField["code"])
	}

	// Also verify the body matches what sendNack writes — sign and send must be identical.
	rr := httptest.NewRecorder()
	sendNack(ctx, rr, err)
	if !bytes.Equal(body, rr.Body.Bytes()) {
		t.Errorf("nackBytes and sendNack produced different bodies:\n  nackBytes: %s\n  sendNack:  %s", body, rr.Body.String())
	}
}

func TestNack_1(t *testing.T) {
	tests := []struct {
		name        string
		err         *model.Error
		status      int
		expected    string
		useBadJSON  bool
		useBadWrite bool
	}{
		{
			name: "Schema Validation Error",
			err: &model.Error{
				Code:    "BAD_REQUEST",
				Paths:   "/test/path",
				Message: "Invalid schema",
			},
			status:   http.StatusBadRequest,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"BAD_REQUEST","paths":"/test/path","message":"Invalid schema"}}}`,
		},
		{
			name: "Internal Server Error",
			err: &model.Error{
				Code:    "INTERNAL_SERVER_ERROR",
				Message: "Something went wrong",
			},
			status:   http.StatusInternalServerError,
			expected: `{"message":{"ack":{"status":"NACK"},"error":{"code":"INTERNAL_SERVER_ERROR","message":"Something went wrong"}}}`,
		},
		{
			name:       "JSON Marshal Error",
			err:        nil,
			status:     http.StatusInternalServerError,
			expected:   `Internal server error, MessageID: 12345`,
			useBadJSON: true,
		},
		{
			name: "Write Error",
			err: &model.Error{
				Code:    "WRITE_ERROR",
				Message: "Failed to write response",
			},
			status:      http.StatusInternalServerError,
			expected:    `Internal server error, MessageID: 12345`,
			useBadWrite: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/", nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(req.Context(), model.ContextKeyMsgID, "12345")

			var w http.ResponseWriter
			if tt.useBadWrite {
				w = &errorResponseWriter{}
			} else {
				w = httptest.NewRecorder()
			}

			if tt.useBadJSON {
				data, _ := json.Marshal(&badMessage{})
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, err := w.Write(data)
				if err != nil {
					http.Error(w, "Failed to write response", http.StatusInternalServerError)
					return
				}
				return
			}

			nack(ctx, w, tt.err, tt.status, model.StatusNACK)
			if !tt.useBadWrite {
				recorder, ok := w.(*httptest.ResponseRecorder)
				if !ok {
					t.Fatal("Failed to cast response recorder")
				}

				if recorder.Code != tt.status {
					t.Errorf("wanted status code %d, got %d", tt.status, recorder.Code)
				}

				body := recorder.Body.String()
				if body != tt.expected {
					t.Errorf("err.Error() = %s, want %s", body, tt.expected)
				}
			}
		})
	}
}

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

func (m *mockKM) GenerateKeyset() (*model.Keyset, error)                               { return nil, nil }
func (m *mockKM) InsertKeyset(_ context.Context, _ string, _ *model.Keyset) error     { return nil }
func (m *mockKM) DeleteKeyset(_ context.Context, _ string) error                      { return nil }
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

// makeResponseStepContext builds a synthetic ResponseStepContext for testing,
// mirroring what the handler constructs from *http.Response in modifyResponse.
func makeResponseStepContext(statusCode int, body string, sig string) *model.ResponseStepContext {
	h := http.Header{}
	if sig != "" {
		h.Set("Signature", sig)
	}
	return &model.ResponseStepContext{
		StatusCode: statusCode,
		Header:     h,
		Body:       []byte(body),
	}
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

func TestAckSignerStep_URLRoutingPath_202_SetsSignatureOnResponse(t *testing.T) {
	// 202 AckNoCallback: app decides, ONIX relays. ackSigner must still sign
	// the response so the caller can verify the Signature header regardless of
	// status code.
	signer := &mockSigner{returnSig: "sig202=="}
	km := &mockKM{keyset: &model.Keyset{UniqueKeyID: "key-202", SigningPrivate: "priv"}}

	step, err := newAckSignerStep(signer, km)
	if err != nil {
		t.Fatalf("newAckSignerStep() unexpected error: %v", err)
	}

	ctx := makeStepCtx("2.0.0", "msg-202", "bpp.example.com", "inboundSig==")
	body := `{"message":{"status":"ACK","error":{"code":"NO_CATALOG","message":"no matching catalog"}}}`
	rctx := makeResponseStepContext(http.StatusAccepted, body, "")

	if err := step.RunOnResponse(ctx, rctx); err != nil {
		t.Fatalf("RunOnResponse() unexpected error on 202: %v", err)
	}

	if !signer.signAckCalled {
		t.Error("expected SignAck to be called for 202 response")
	}
	if rctx.Header.Get("Signature") == "" {
		t.Fatal("expected Signature header on 202 response")
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
	rctx := makeResponseStepContext(http.StatusOK, `{"message":{"ack":{"status":"ACK"}}}`, "")

	if err := step.RunOnResponse(ctx, rctx); err != nil {
		t.Fatalf("RunOnResponse() unexpected error: %v", err)
	}

	if !signer.signAckCalled {
		t.Error("expected SignAck to be called on URL-routing path")
	}
	// Signature must be on rctx.Header (shared ref to resp.Header), not ctx.RespHeader.
	if rctx.Header.Get("Signature") == "" {
		t.Fatal("expected Signature header on rctx.Header")
	}
	if ctx.RespHeader.Get("Signature") != "" {
		t.Error("expected Signature header NOT set on ctx.RespHeader for URL-routing path")
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
	rctx := makeResponseStepContext(http.StatusOK, `{"message":{"status":"ACK","messageId":"msg-001"}}`, testSigHeader)

	if err := step.RunOnResponse(ctx, rctx); err != nil {
		t.Fatalf("RunOnResponse() unexpected error: %v", err)
	}

	if !sv.validateAckCalled {
		t.Error("expected ValidateAck to be called")
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
	rctx := makeResponseStepContext(http.StatusOK, `{"message":{"ack":{"status":"ACK"}}}`, "")

	if err := step.RunOnResponse(ctx, rctx); err != nil {
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
	rctx := makeResponseStepContext(http.StatusOK, `{"message":{"status":"ACK"}}`, "") // no Signature header

	if err := step.RunOnResponse(ctx, rctx); err != nil {
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
	rctx := makeResponseStepContext(http.StatusOK, `{"message":{"status":"ACK"}}`, "malformed-header-no-keyId")

	if err := step.RunOnResponse(ctx, rctx); err != nil {
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
	rctx := makeResponseStepContext(http.StatusOK, `{"message":{"status":"ACK"}}`, testSigHeader)

	if err := step.RunOnResponse(ctx, rctx); err != nil {
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
	rctx := makeResponseStepContext(http.StatusOK, `{"message":{"status":"ACK"}}`, testSigHeader)

	if err := step.RunOnResponse(ctx, rctx); err != nil {
		t.Fatalf("expected nil (degrade) when ValidateAck fails, got: %v", err)
	}
}

// TestValidateAckSignatureStep_MissingSignature_AllStatusCodes_Degrades verifies
// that a missing Signature header on ANY status code (200, 202, 4xx, 500) causes
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
		http.StatusAccepted,        // 202 AckNoCallback
		http.StatusInternalServerError,
	}
	for _, code := range codes {
		rctx := makeResponseStepContext(code, `{"message":{"ack":{"status":"NACK"}}}`, "")
		if err := step.RunOnResponse(ctx, rctx); err != nil {
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
	ctx := makeCallerStepCtx("2.0.0", "msg-202", "bap.example.com", `Signature keyId="bap.example.com|key-1|ed25519",signature="outSig=="`)

	codes := []int{
		http.StatusOK,
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusAccepted, // 202 AckNoCallback
		http.StatusInternalServerError,
	}
	for _, code := range codes {
		sv.validateAckCalled = false
		rctx := makeResponseStepContext(code, `{"message":{"status":"ACK"}}`, testSigHeader)
		if err := step.RunOnResponse(ctx, rctx); err != nil {
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
	rctx := makeResponseStepContext(http.StatusBadRequest, `{"message":{"ack":{"status":"NACK"}}}`, "")

	if err := step.RunOnResponse(ctx, rctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !signer.signAckCalled {
		t.Error("expected SignAck to be called even for upstream 4xx")
	}
	if rctx.Header.Get("Signature") == "" {
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
