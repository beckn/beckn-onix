package signvalidator

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// generateTestKeyPair generates a new ED25519 key pair for testing.
func generateTestKeyPair() (string, string) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	return base64.StdEncoding.EncodeToString(privateKey), base64.StdEncoding.EncodeToString(publicKey)
}

// signTestData creates a valid signature for test cases.
func signTestData(privateKeyBase64 string, body []byte, createdAt, expiresAt int64) string {
	privateKeyBytes, _ := base64.StdEncoding.DecodeString(privateKeyBase64)
	privateKey := ed25519.PrivateKey(privateKeyBytes)

	signingString := hash(body, createdAt, expiresAt)
	signature := ed25519.Sign(privateKey, []byte(signingString))

	return base64.StdEncoding.EncodeToString(signature)
}

// signedHeaderWithKeyID builds a full Authorization header including keyId so that
// extractSubscriberIDFromHeader can extract the subscriber ID for identity checks.
func signedHeaderWithKeyID(subscriberID, privateKeyBase64 string, body []byte, createdAt, expiresAt int64) string {
	sig := signTestData(privateKeyBase64, body, createdAt, expiresAt)
	return fmt.Sprintf(
		`Signature keyId="%s|key-1|ed25519",algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		subscriberID, createdAt, expiresAt, sig,
	)
}

// makeCtx builds a minimal StepContext. When subscriberHeader is non-empty it is
// set as the Authorization header on the request, which causes the identity check
// gate inside Validate/ValidateAck to open.
func makeCtx(body []byte, role model.Role, subscriberHeader string) *model.StepContext {
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	if subscriberHeader != "" {
		req.Header.Set(model.AuthHeaderSubscriber, subscriberHeader)
	}
	return &model.StepContext{
		Context:    context.Background(),
		Body:       body,
		Role:       role,
		Request:    req,
		RespHeader: http.Header{},
	}
}

// makeCtxWithCallerID is like makeCtx but also injects a callerID into the Go
// context, simulating what reqpreprocessor writes to model.ContextKeyRemoteID.
func makeCtxWithCallerID(body []byte, role model.Role, subscriberHeader, callerID string) *model.StepContext {
	goCtx := context.WithValue(context.Background(), model.ContextKeyRemoteID, callerID)
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	if subscriberHeader != "" {
		req.Header.Set(model.AuthHeaderSubscriber, subscriberHeader)
	}
	return &model.StepContext{
		Context:    goCtx,
		Body:       body,
		Role:       role,
		Request:    req,
		RespHeader: http.Header{},
	}
}

// ---------------------------------------------------------------------------
// Crypto verification — Validate
// ---------------------------------------------------------------------------

// TestVerifySuccessCases tests all valid signature verification cases.
func TestVerifySuccess(t *testing.T) {
	privateKeyBase64, publicKeyBase64 := generateTestKeyPair()

	tests := []struct {
		name      string
		body      []byte
		createdAt int64
		expiresAt int64
	}{
		{
			name:      "Valid Signature",
			body:      []byte("Test Payload"),
			createdAt: time.Now().Unix(),
			expiresAt: time.Now().Unix() + 3600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signature := signTestData(privateKeyBase64, tt.body, tt.createdAt, tt.expiresAt)
			header := `Signature algorithm="ed25519", created="` + strconv.FormatInt(tt.createdAt, 10) +
				`", expires="` + strconv.FormatInt(tt.expiresAt, 10) +
				`", signature="` + signature + `"`

			verifier, close, _ := New(context.Background(), &Config{})
			// subscriberHeader not set — identity check gate evaluates false, check skipped.
			err := verifier.Validate(makeCtx(tt.body, "", ""), header, publicKeyBase64)

			if err != nil {
				t.Fatalf("Expected no error, but got: %v", err)
			}
			if close != nil {
				if err := close(); err != nil {
					t.Fatalf("Test %q failed: cleanup function returned an error: %v", tt.name, err)
				}
			}
		})
	}
}

// TestVerifyFailureCases tests all invalid signature verification cases.
func TestVerifyFailure(t *testing.T) {
	privateKeyBase64, publicKeyBase64 := generateTestKeyPair()
	_, wrongPublicKeyBase64 := generateTestKeyPair()

	tests := []struct {
		name        string
		body        []byte
		header      string
		pubKey      string
		errContains string
	}{
		{
			name:   "Missing Authorization Header",
			body:   []byte("Test Payload"),
			header: "",
			pubKey: publicKeyBase64,
		},
		{
			name:   "Malformed Header",
			body:   []byte("Test Payload"),
			header: `InvalidSignature created="wrong"`,
			pubKey: publicKeyBase64,
		},
		{
			name: "Unsupported Algorithm",
			body: []byte("Test Payload"),
			header: `Signature algorithm="rsa", created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+3600, 10) +
				`", signature="somesig=="`,
			pubKey: publicKeyBase64,
		},
		{
			name: "Missing Algorithm",
			body: []byte("Test Payload"),
			header: `Signature created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+3600, 10) +
				`", signature="somesig=="`,
			pubKey: publicKeyBase64,
		},
		{
			name: "Invalid Base64 Signature",
			body: []byte("Test Payload"),
			header: `Signature algorithm="ed25519", created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+3600, 10) +
				`", signature="!!INVALIDBASE64!!"`,
			pubKey: publicKeyBase64,
		},
		{
			name: "Expired Signature",
			body: []byte("Test Payload"),
			header: `Signature algorithm="ed25519", created="` + strconv.FormatInt(time.Now().Unix()-7200, 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()-3600, 10) +
				`", signature="` + signTestData(privateKeyBase64, []byte("Test Payload"), time.Now().Unix()-7200, time.Now().Unix()-3600) + `"`,
			pubKey:      publicKeyBase64,
			errContains: "expired_by=",
		},
		{
			name: "Invalid Public Key",
			body: []byte("Test Payload"),
			header: `Signature algorithm="ed25519", created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+3600, 10) +
				`", signature="` + signTestData(privateKeyBase64, []byte("Test Payload"), time.Now().Unix(), time.Now().Unix()+3600) + `"`,
			pubKey: wrongPublicKeyBase64,
		},
		{
			name: "Invalid Expires Timestamp",
			body: []byte("Test Payload"),
			header: `Signature algorithm="ed25519", created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="invalid_timestamp"`,
			pubKey: publicKeyBase64,
		},
		{
			name: "Signature Missing in Headers",
			body: []byte("Test Payload"),
			header: `Signature algorithm="ed25519", created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+3600, 10) + `"`,
			pubKey: publicKeyBase64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier, close, _ := New(context.Background(), &Config{})
			// subscriberHeader not set — identity check gate evaluates false, check skipped.
			err := verifier.Validate(makeCtx(tt.body, "", ""), tt.header, tt.pubKey)

			if err == nil {
				t.Fatal("Expected an error but got none")
			}
			if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("Expected error to contain %q, got: %v", tt.errContains, err)
			}
			if close != nil {
				if err := close(); err != nil {
					t.Fatalf("Test %q failed: cleanup function returned an error: %v", tt.name, err)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Subscriber identity check — Validate
// ---------------------------------------------------------------------------

func TestValidate_SubIdentity_FromContext_Match(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search","bap_id":"bap.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedHeaderWithKeyID("bap.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtxWithCallerID(body, model.RoleBPP, header, "bap.example.com")
	if err := verifier.Validate(ctx, header, publicKey); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidate_SubIdentity_FromContext_Mismatch(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search","bap_id":"bap.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedHeaderWithKeyID("evil.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtxWithCallerID(body, model.RoleBPP, header, "bap.example.com")
	if err := verifier.Validate(ctx, header, publicKey); err == nil {
		t.Fatal("expected error: signer evil.com does not match callerID bap.example.com")
	}
}

func TestValidate_SubIdentity_FromBody_Match(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search","bap_id":"bap.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedHeaderWithKeyID("bap.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	// No ContextKeyRemoteID — check falls back to body.
	ctx := makeCtx(body, model.RoleBPP, header)
	if err := verifier.Validate(ctx, header, publicKey); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidate_SubIdentity_FromBody_Mismatch(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search","bap_id":"bap.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedHeaderWithKeyID("evil.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtx(body, model.RoleBPP, header)
	if err := verifier.Validate(ctx, header, publicKey); err == nil {
		t.Fatal("expected error: signer evil.com does not match body bap_id bap.example.com")
	}
}

func TestValidate_SubIdentity_V2Alias_SenderId(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search","senderId":"bap.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedHeaderWithKeyID("bap.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtx(body, model.RoleBPP, header)
	if err := verifier.Validate(ctx, header, publicKey); err != nil {
		t.Fatalf("expected no error with senderId alias, got: %v", err)
	}
}

func TestValidate_SubIdentity_V2Alias_ReceiverId(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"on_search","receiverId":"bpp.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedHeaderWithKeyID("bpp.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtx(body, model.RoleBAP, header)
	if err := verifier.Validate(ctx, header, publicKey); err != nil {
		t.Fatalf("expected no error with receiverId alias, got: %v", err)
	}
}

func TestValidate_SubIdentity_NoCallerIDField_Skips(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	// Body has no bap_id/bapId/senderId — identity check must be skipped.
	body := []byte(`{"context":{"action":"search"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedHeaderWithKeyID("anyone.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtx(body, model.RoleBPP, header)
	if err := verifier.Validate(ctx, header, publicKey); err != nil {
		t.Fatalf("expected no error when no caller ID field in body, got: %v", err)
	}
}

func TestValidate_SubIdentity_GatewayRole_Skips(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search","bap_id":"bap.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedHeaderWithKeyID("gateway.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	// Gate is open (header set), but RoleGateway causes ResolveCallerID to return "" → skipped.
	ctx := makeCtx(body, model.RoleGateway, header)
	if err := verifier.Validate(ctx, header, publicKey); err != nil {
		t.Fatalf("expected no error for Gateway role, got: %v", err)
	}
}

func TestValidate_SubIdentity_MalformedBody_Skips(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`not-valid-json`)
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedHeaderWithKeyID("anyone.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtx(body, model.RoleBPP, header)
	if err := verifier.Validate(ctx, header, publicKey); err != nil {
		t.Fatalf("expected identity check to be skipped on malformed body, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Subscriber identity check — ValidateAck
// ---------------------------------------------------------------------------

// signAckTestData signs a body using the 4-line ACK signing string (NFH-004 §3.4).
func signAckTestData(privateKeyBase64 string, body []byte, outboundAuth string, createdAt, expiresAt int64) string {
	privateKeyBytes, _ := base64.StdEncoding.DecodeString(privateKeyBase64)
	signingString := hashAck(body, createdAt, expiresAt, outboundAuth)
	signature := ed25519.Sign(ed25519.PrivateKey(privateKeyBytes), []byte(signingString))
	return base64.StdEncoding.EncodeToString(signature)
}

// signedAckHeaderWithKeyID builds a full ACK Signature header including keyId.
func signedAckHeaderWithKeyID(subscriberID, privateKeyBase64 string, body []byte, outboundAuth string, createdAt, expiresAt int64) string {
	sig := signAckTestData(privateKeyBase64, body, outboundAuth, createdAt, expiresAt)
	return fmt.Sprintf(
		`Signature keyId="%s|key-1|ed25519",algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		subscriberID, createdAt, expiresAt, sig,
	)
}

func TestValidateAck_SubIdentity_FromContext_Match(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"on_search","bap_id":"bap.example.com"}}`)
	outboundAuth := "outbound-sig-value=="
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedAckHeaderWithKeyID("bap.example.com", privateKey, body, outboundAuth, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtxWithCallerID(body, model.RoleBPP, header, "bap.example.com")
	if err := verifier.ValidateAck(ctx, body, header, outboundAuth, publicKey); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateAck_SubIdentity_FromContext_Mismatch(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"on_search","bap_id":"bap.example.com"}}`)
	outboundAuth := "outbound-sig-value=="
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedAckHeaderWithKeyID("evil.com", privateKey, body, outboundAuth, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtxWithCallerID(body, model.RoleBPP, header, "bap.example.com")
	if err := verifier.ValidateAck(ctx, body, header, outboundAuth, publicKey); err == nil {
		t.Fatal("expected error: signer evil.com does not match callerID bap.example.com")
	}
}

func TestValidateAck_SubIdentity_FromBody_Match(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"on_search","bap_id":"bap.example.com"}}`)
	outboundAuth := "outbound-sig-value=="
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedAckHeaderWithKeyID("bap.example.com", privateKey, body, outboundAuth, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtx(body, model.RoleBPP, header)
	if err := verifier.ValidateAck(ctx, body, header, outboundAuth, publicKey); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateAck_SubIdentity_FromBody_Mismatch(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"on_search","bap_id":"bap.example.com"}}`)
	outboundAuth := "outbound-sig-value=="
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	header := signedAckHeaderWithKeyID("evil.com", privateKey, body, outboundAuth, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtx(body, model.RoleBPP, header)
	if err := verifier.ValidateAck(ctx, body, header, outboundAuth, publicKey); err == nil {
		t.Fatal("expected error: signer evil.com does not match body bap_id bap.example.com")
	}
}

func TestValidateAck_SubIdentity_GateClosedForAckResponse(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"on_search","bap_id":"bap.example.com"}}`)
	outboundAuth := "outbound-sig-value=="
	now, exp := time.Now().Unix(), time.Now().Unix()+3600
	// Signer is mismatched — but gate must stay closed, so identity check must be skipped.
	header := signedAckHeaderWithKeyID("evil.com", privateKey, body, outboundAuth, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	// Authorization header is NOT set to match the ACK Signature header — gate evaluates false.
	ctx := makeCtx(body, model.RoleBPP, "")
	if err := verifier.ValidateAck(ctx, body, header, outboundAuth, publicKey); err != nil {
		t.Fatalf("expected identity check to be skipped when gate is closed, got: %v", err)
	}
}
