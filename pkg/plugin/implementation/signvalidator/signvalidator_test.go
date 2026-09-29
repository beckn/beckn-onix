package signvalidator

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
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

// signedHeaderWithKeyID builds a full Authorization header including keyId.
func signedHeaderWithKeyID(subscriberID, privateKeyBase64 string, body []byte, createdAt, expiresAt int64) string {
	sig := signTestData(privateKeyBase64, body, createdAt, expiresAt)
	return fmt.Sprintf(
		`Signature keyId="%s|key-1|ed25519",algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		subscriberID, createdAt, expiresAt, sig,
	)
}

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

// makeCtx builds a minimal StepContext.
func makeCtx(body []byte, role model.Role) *model.StepContext {
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	return &model.StepContext{
		Context:    context.Background(),
		Body:       body,
		Role:       role,
		Request:    req,
		RespHeader: http.Header{},
	}
}

// makeCtxWithCallerID is like makeCtx but injects a callerID into the Go context,
// simulating what reqpreprocessor writes to model.ContextKeyRemoteID.
func makeCtxWithCallerID(body []byte, role model.Role, callerID string) *model.StepContext {
	goCtx := context.WithValue(context.Background(), model.ContextKeyRemoteID, callerID)
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	return &model.StepContext{
		Context:    goCtx,
		Body:       body,
		Role:       role,
		Request:    req,
		RespHeader: http.Header{},
	}
}

// ---------------------------------------------------------------------------
// Clock-skew tolerance
// ---------------------------------------------------------------------------

func TestClockSkewTolerance_Default(t *testing.T) {
	// With no explicit tolerance configured, a created timestamp 3 s in the
	// future should be accepted (within the 5 s spec default).
	privateKey, publicKey := generateTestKeyPair()
	body := []byte("payload")
	now := time.Now().Unix()
	created := now + 3
	expires := now + 120
	header := fmt.Sprintf(
		`Signature algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		created, expires, signTestData(privateKey, body, created, expires),
	)
	verifier, _, _ := New(context.Background(), &Config{})
	if err := verifier.Validate(makeCtx(body, ""), header, publicKey, false); err != nil {
		t.Fatalf("expected default 5 s tolerance to accept created+3s, got: %v", err)
	}
}

func TestClockSkewTolerance_CreatedBeyondTolerance_Rejected(t *testing.T) {
	// created is 7 s in the future — beyond the 5 s default tolerance.
	privateKey, publicKey := generateTestKeyPair()
	body := []byte("payload")
	now := time.Now().Unix()
	created := now + 7
	expires := now + 120
	header := fmt.Sprintf(
		`Signature algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		created, expires, signTestData(privateKey, body, created, expires),
	)
	verifier, _, _ := New(context.Background(), &Config{})
	if err := verifier.Validate(makeCtx(body, ""), header, publicKey, false); err == nil {
		t.Fatal("expected rejection when created exceeds tolerance")
	}
}

func TestClockSkewTolerance_ExpiredNeverTolerated(t *testing.T) {
	// expires is 1 s in the past — must be rejected regardless of tolerance config.
	privateKey, publicKey := generateTestKeyPair()
	body := []byte("payload")
	now := time.Now().Unix()
	created := now - 60
	expires := now - 1
	header := fmt.Sprintf(
		`Signature algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		created, expires, signTestData(privateKey, body, created, expires),
	)
	// Use a large tolerance to confirm it has no effect on expires.
	d := 30 * time.Second
	verifier, _, _ := New(context.Background(), &Config{ClockSkewTolerance: &d})
	if err := verifier.Validate(makeCtx(body, ""), header, publicKey, false); err == nil {
		t.Fatal("expected rejection of expired signature even with large tolerance")
	}
}

func TestClockSkewTolerance_CustomTolerance(t *testing.T) {
	// created is 8 s in the future — rejected with default 5 s, accepted with 10 s tolerance.
	privateKey, publicKey := generateTestKeyPair()
	body := []byte("payload")
	now := time.Now().Unix()
	created := now + 8
	expires := now + 120
	header := fmt.Sprintf(
		`Signature algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		created, expires, signTestData(privateKey, body, created, expires),
	)
	d := 10 * time.Second
	verifier, _, _ := New(context.Background(), &Config{ClockSkewTolerance: &d})
	if err := verifier.Validate(makeCtx(body, ""), header, publicKey, false); err != nil {
		t.Fatalf("expected acceptance with 10 s tolerance, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Crypto verification — Validate
// ---------------------------------------------------------------------------

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
			expiresAt: time.Now().Unix() + 120,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signature := signTestData(privateKeyBase64, tt.body, tt.createdAt, tt.expiresAt)
			header := `Signature algorithm="ed25519", created="` + strconv.FormatInt(tt.createdAt, 10) +
				`", expires="` + strconv.FormatInt(tt.expiresAt, 10) +
				`", signature="` + signature + `"`

			verifier, close, _ := New(context.Background(), &Config{})
			err := verifier.Validate(makeCtx(tt.body, ""), header, publicKeyBase64, false)

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

func TestVerifyFailure(t *testing.T) {
	privateKeyBase64, publicKeyBase64 := generateTestKeyPair()
	_, wrongPublicKeyBase64 := generateTestKeyPair()

	tests := []struct {
		name        string
		body        []byte
		header      string
		pubKey      string
		errContains string
		wantCode    string
	}{
		{
			name:     "Missing Authorization Header",
			body:     []byte("Test Payload"),
			header:   "",
			pubKey:   publicKeyBase64,
			wantCode: codeSignatureInvalid,
		},
		{
			name:     "Malformed Header",
			body:     []byte("Test Payload"),
			header:   `InvalidSignature created="wrong"`,
			pubKey:   publicKeyBase64,
			wantCode: codeSignatureInvalid,
		},
		{
			name: "Unsupported Algorithm",
			body: []byte("Test Payload"),
			header: `Signature algorithm="rsa", created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+120, 10) +
				`", signature="somesig=="`,
			pubKey:   publicKeyBase64,
			wantCode: codeSignatureInvalid,
		},
		{
			name: "Missing Algorithm",
			body: []byte("Test Payload"),
			header: `Signature created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+120, 10) +
				`", signature="somesig=="`,
			pubKey:   publicKeyBase64,
			wantCode: codeSignatureInvalid,
		},
		{
			name: "Invalid Base64 Signature",
			body: []byte("Test Payload"),
			header: `Signature algorithm="ed25519", created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+120, 10) +
				`", signature="!!INVALIDBASE64!!"`,
			pubKey:   publicKeyBase64,
			wantCode: codeSignatureInvalid,
		},
		{
			name: "Expired Signature",
			body: []byte("Test Payload"),
			header: `Signature algorithm="ed25519", created="` + strconv.FormatInt(time.Now().Unix()-7200, 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()-3600, 10) +
				`", signature="` + signTestData(privateKeyBase64, []byte("Test Payload"), time.Now().Unix()-7200, time.Now().Unix()-3600) + `"`,
			pubKey:      publicKeyBase64,
			errContains: "expired_by=",
			wantCode:    codeSignatureInvalid,
		},
		{
			name: "Invalid Public Key",
			body: []byte("Test Payload"),
			header: `Signature algorithm="ed25519", created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+120, 10) +
				`", signature="` + signTestData(privateKeyBase64, []byte("Test Payload"), time.Now().Unix(), time.Now().Unix()+120) + `"`,
			pubKey:   wrongPublicKeyBase64,
			wantCode: codeSignatureInvalid,
		},
		{
			name: "Invalid Expires Timestamp",
			body: []byte("Test Payload"),
			header: `Signature algorithm="ed25519", created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="invalid_timestamp"`,
			pubKey:   publicKeyBase64,
			wantCode: codeSignatureInvalid,
		},
		{
			name: "Signature Missing in Headers",
			body: []byte("Test Payload"),
			header: `Signature algorithm="ed25519", created="` + strconv.FormatInt(time.Now().Unix(), 10) +
				`", expires="` + strconv.FormatInt(time.Now().Unix()+120, 10) + `"`,
			pubKey:   publicKeyBase64,
			wantCode: codeSignatureMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier, close, _ := New(context.Background(), &Config{})
			err := verifier.Validate(makeCtx(tt.body, ""), tt.header, tt.pubKey, false)

			if err == nil {
				t.Fatal("Expected an error but got none")
			}
			if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("Expected error to contain %q, got: %v", tt.errContains, err)
			}
			var signErr *model.CodedErr
			if !errors.As(err, &signErr) {
				t.Fatalf("expected err to be a *model.CodedErr, got: %T (%v)", err, err)
			}
			if got := signErr.HTTPStatus(); got != http.StatusUnauthorized {
				t.Errorf("HTTPStatus() = %d, want %d", got, http.StatusUnauthorized)
			}
			if signErr.Code != tt.wantCode {
				t.Errorf("signErr.Code = %s, want %s", signErr.Code, tt.wantCode)
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
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedHeaderWithKeyID("bap.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtxWithCallerID(body, model.RoleBPP, "bap.example.com")
	if err := verifier.Validate(ctx, header, publicKey, true); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidate_SubIdentity_FromContext_Mismatch(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search","bap_id":"bap.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedHeaderWithKeyID("evil.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtxWithCallerID(body, model.RoleBPP, "bap.example.com")
	err := verifier.Validate(ctx, header, publicKey, true)
	if err == nil {
		t.Fatal("expected error: signer evil.com does not match callerID bap.example.com")
	}
	var signErr *model.CodedErr
	if !errors.As(err, &signErr) {
		t.Fatalf("expected err to be a *model.CodedErr, got: %T (%v)", err, err)
	}
	if signErr.Code != codeUnauthorizedAction {
		t.Errorf("signErr.Code = %s, want %s", signErr.Code, codeUnauthorizedAction)
	}
}

func TestValidate_SubIdentity_FromBody_Match(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search","bap_id":"bap.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedHeaderWithKeyID("bap.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtx(body, model.RoleBPP)
	if err := verifier.Validate(ctx, header, publicKey, true); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidate_SubIdentity_FromBody_Mismatch(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search","bap_id":"bap.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedHeaderWithKeyID("evil.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtx(body, model.RoleBPP)
	if err := verifier.Validate(ctx, header, publicKey, true); err == nil {
		t.Fatal("expected error: signer evil.com does not match body bap_id bap.example.com")
	}
}

func TestValidate_SubIdentity_V2Alias_SenderId(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search","senderId":"bap.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedHeaderWithKeyID("bap.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	if err := verifier.Validate(makeCtx(body, model.RoleBPP), header, publicKey, true); err != nil {
		t.Fatalf("expected no error with senderId alias, got: %v", err)
	}
}

func TestValidate_SubIdentity_V2Alias_ReceiverId(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"on_search","receiverId":"bpp.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedHeaderWithKeyID("bpp.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	if err := verifier.Validate(makeCtx(body, model.RoleBAP), header, publicKey, true); err != nil {
		t.Fatalf("expected no error with receiverId alias, got: %v", err)
	}
}

func TestValidate_SubIdentity_NoCallerIDField_Skips(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedHeaderWithKeyID("anyone.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	if err := verifier.Validate(makeCtx(body, model.RoleBPP), header, publicKey, true); err != nil {
		t.Fatalf("expected identity check skipped when no caller ID in body, got: %v", err)
	}
}

func TestValidate_SubIdentity_GatewayRole_Skips(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"search","bap_id":"bap.example.com"}}`)
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedHeaderWithKeyID("gateway.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	// RoleGateway causes ResolveCallerID to return "" → check skipped.
	if err := verifier.Validate(makeCtx(body, model.RoleGateway), header, publicKey, true); err != nil {
		t.Fatalf("expected no error for Gateway role, got: %v", err)
	}
}

func TestValidate_SubIdentity_MalformedBody_Skips(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`not-valid-json`)
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedHeaderWithKeyID("anyone.example.com", privateKey, body, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	if err := verifier.Validate(makeCtx(body, model.RoleBPP), header, publicKey, true); err != nil {
		t.Fatalf("expected identity check skipped on malformed body, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Subscriber identity check — ValidateAck
// ---------------------------------------------------------------------------

func TestValidateAck_SubIdentity_FromContext_Match(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"on_search","bap_id":"bap.example.com"}}`)
	outboundAuth := "outbound-sig-value=="
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedAckHeaderWithKeyID("bap.example.com", privateKey, body, outboundAuth, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtxWithCallerID(body, model.RoleBPP, "bap.example.com")
	if err := verifier.ValidateAck(ctx, body, header, outboundAuth, publicKey, true); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateAck_SubIdentity_FromContext_Mismatch(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"on_search","bap_id":"bap.example.com"}}`)
	outboundAuth := "outbound-sig-value=="
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedAckHeaderWithKeyID("evil.com", privateKey, body, outboundAuth, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	ctx := makeCtxWithCallerID(body, model.RoleBPP, "bap.example.com")
	err := verifier.ValidateAck(ctx, body, header, outboundAuth, publicKey, true)
	if err == nil {
		t.Fatal("expected error: signer evil.com does not match callerID bap.example.com")
	}
	var signErr *model.CodedErr
	if !errors.As(err, &signErr) {
		t.Fatalf("expected err to be a *model.CodedErr, got: %T (%v)", err, err)
	}
	if signErr.Code != codeUnauthorizedAction {
		t.Errorf("signErr.Code = %s, want %s", signErr.Code, codeUnauthorizedAction)
	}
}

func TestValidateAck_SubIdentity_FromBody_Match(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"on_search","bap_id":"bap.example.com"}}`)
	outboundAuth := "outbound-sig-value=="
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedAckHeaderWithKeyID("bap.example.com", privateKey, body, outboundAuth, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	if err := verifier.ValidateAck(makeCtx(body, model.RoleBPP), body, header, outboundAuth, publicKey, true); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateAck_SubIdentity_FromBody_Mismatch(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte(`{"context":{"action":"on_search","bap_id":"bap.example.com"}}`)
	outboundAuth := "outbound-sig-value=="
	now, exp := time.Now().Unix(), time.Now().Unix()+120
	header := signedAckHeaderWithKeyID("evil.com", privateKey, body, outboundAuth, now, exp)

	verifier, _, _ := New(context.Background(), &Config{})
	if err := verifier.ValidateAck(makeCtx(body, model.RoleBPP), body, header, outboundAuth, publicKey, true); err == nil {
		t.Fatal("expected error: signer evil.com does not match body bap_id bap.example.com")
	}
}

// ---------------------------------------------------------------------------
// Signature validity window
// ---------------------------------------------------------------------------

// validityHeader builds a signed header whose window is `window` seconds wide
// and still open, isolating the width check from the liveness checks.
func validityHeader(privateKey string, body []byte, window int64) string {
	created := time.Now().Unix()
	expires := created + window
	return fmt.Sprintf(
		`Signature algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		created, expires, signTestData(privateKey, body, created, expires),
	)
}

func TestValidityWindow_DefaultAcceptsSpecCompliantWindow(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte("payload")

	verifier, _, _ := New(context.Background(), &Config{})
	// 5 minutes is exactly what ONIX itself signs with, so it must pass.
	if err := verifier.Validate(makeCtx(body, ""), validityHeader(privateKey, body, 300), publicKey, false); err != nil {
		t.Fatalf("expected a 300 s window to be accepted, got: %v", err)
	}
}

func TestValidityWindow_DefaultRejectsOverlongWindow(t *testing.T) {
	// The core of the fix: a peer chooses both created and expires, so without
	// this bound one signature can be made to stay acceptable indefinitely.
	privateKey, publicKey := generateTestKeyPair()
	body := []byte("payload")

	verifier, _, _ := New(context.Background(), &Config{})
	err := verifier.Validate(makeCtx(body, ""), validityHeader(privateKey, body, 86400), publicKey, false)
	if err == nil {
		t.Fatal("expected a 24 h validity window to be rejected")
	}
	if !strings.Contains(err.Error(), "validity window too long") {
		t.Errorf("expected a validity-window error, got: %v", err)
	}

	var signErr *model.CodedErr
	if !errors.As(err, &signErr) {
		t.Fatalf("expected *model.CodedErr, got %T", err)
	}
	if got := signErr.BecknError().Code; got != codeSignatureInvalid {
		t.Errorf("code = %s, want %s", got, codeSignatureInvalid)
	}
}

func TestValidityWindow_ConfiguredBoundIsHonoured(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair()
	body := []byte("payload")

	tolerance := 60 * time.Second
	verifier, _, _ := New(context.Background(), &Config{MaxSignatureValidity: &tolerance})

	if err := verifier.Validate(makeCtx(body, ""), validityHeader(privateKey, body, 60), publicKey, false); err != nil {
		t.Fatalf("a 60 s window should pass a 60 s bound, got: %v", err)
	}
	if err := verifier.Validate(makeCtx(body, ""), validityHeader(privateKey, body, 61), publicKey, false); err == nil {
		t.Fatal("a 61 s window should fail a 60 s bound")
	}
}

func TestValidityWindow_ZeroDisablesTheBound(t *testing.T) {
	// The documented escape hatch for operators who knowingly accept
	// long-lived signatures.
	privateKey, publicKey := generateTestKeyPair()
	body := []byte("payload")

	unlimited := time.Duration(0)
	verifier, _, _ := New(context.Background(), &Config{MaxSignatureValidity: &unlimited})
	if err := verifier.Validate(makeCtx(body, ""), validityHeader(privateKey, body, 86400*365), publicKey, false); err != nil {
		t.Fatalf("expected the bound to be disabled, got: %v", err)
	}
}

func TestValidityWindow_ExpiredStillReportsAsExpired(t *testing.T) {
	// Ordering guard: the width checks run after the liveness checks, so an
	// expired signature keeps its existing, more useful error message even
	// when its window would also breach the bound.
	privateKey, publicKey := generateTestKeyPair()
	body := []byte("payload")
	now := time.Now().Unix()
	created, expires := now-7200, now-3600
	header := fmt.Sprintf(
		`Signature algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		created, expires, signTestData(privateKey, body, created, expires),
	)

	verifier, _, _ := New(context.Background(), &Config{})
	err := verifier.Validate(makeCtx(body, ""), header, publicKey, false)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected an expiry error, got: %v", err)
	}
}

func TestValidityWindow_InvertedWindowRejected(t *testing.T) {
	// created inside the skew tolerance, expires a second out: this slips past
	// both liveness checks, so it needs its own rejection.
	privateKey, publicKey := generateTestKeyPair()
	body := []byte("payload")
	now := time.Now().Unix()
	created, expires := now+3, now+1
	header := fmt.Sprintf(
		`Signature algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		created, expires, signTestData(privateKey, body, created, expires),
	)

	verifier, _, _ := New(context.Background(), &Config{})
	err := verifier.Validate(makeCtx(body, ""), header, publicKey, false)
	if err == nil {
		t.Fatal("expected an inverted validity window to be rejected")
	}
	if !strings.Contains(err.Error(), "inverted validity window") {
		t.Errorf("expected an inverted-window error, got: %v", err)
	}
}

func TestValidityWindow_AppliesToValidateAck(t *testing.T) {
	// ValidateAck shares checkTimestampWindow, so the bound must hold there too.
	privateKey, publicKey := generateTestKeyPair()
	body := []byte("ack payload")
	const outbound = "outboundSig=="

	created := time.Now().Unix()
	expires := created + 86400
	header := fmt.Sprintf(
		`Signature keyId="bpp.example.com|key-1|ed25519",algorithm="ed25519",created="%d",expires="%d",signature="%s"`,
		created, expires, signAckTestData(privateKey, body, outbound, created, expires),
	)

	verifier, _, _ := New(context.Background(), &Config{})
	err := verifier.ValidateAck(makeCtx(body, ""), body, header, outbound, publicKey, false)
	if err == nil {
		t.Fatal("expected an overlong AckSignature window to be rejected")
	}
	if !strings.Contains(err.Error(), "validity window too long") {
		t.Errorf("expected a validity-window error, got: %v", err)
	}
}
