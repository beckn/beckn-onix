package signvalidator

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"golang.org/x/crypto/blake2b"
)

// Config struct for Verifier.
type Config struct {
}

// validator implements the validator interface.
type validator struct {
	config *Config
}

// New creates a new Verifier instance.
func New(ctx context.Context, config *Config) (*validator, func() error, error) {
	v := &validator{config: config}

	return v, nil, nil
}

// Validate verifies the 3-line signing string for inbound requests.
func (v *validator) Validate(ctx *model.StepContext, header string, publicKeyBase64 string) error {
	createdTimestamp, expiredTimestamp, signature, subscriberID, err := parseAuthHeader(header)
	if err != nil {
		return model.NewSignValidationErr(fmt.Errorf("error parsing header: %w", err))
	}

	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("error decoding signature: %w", err)
	}

	if err := checkTimestampWindow("signature", createdTimestamp, expiredTimestamp); err != nil {
		return err
	}

	createdTime := time.Unix(createdTimestamp, 0)
	expiredTime := time.Unix(expiredTimestamp, 0)

	signingString := hash(ctx.Body, createdTime.Unix(), expiredTime.Unix())

	decodedPublicKey, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return model.NewSignValidationErr(fmt.Errorf("error decoding public key: %w", err))
	}

	if !ed25519.Verify(ed25519.PublicKey(decodedPublicKey), []byte(signingString), signatureBytes) {
		return model.NewSignValidationErr(fmt.Errorf("signature verification failed"))
	}

	if ctx.Request.Header.Get(model.AuthHeaderSubscriber) == header {
		if err := checkSubscriberIdentity(ctx, ctx.Body, subscriberID); err != nil {
			return err
		}
	}
	return nil
}

// parseAuthHeader extracts signature values from the Authorization header.
// subscriberID is the first |-delimited component of keyId="..." (empty if keyId absent).
func parseAuthHeader(header string) (int64, int64, string, string, error) {
	header = strings.TrimPrefix(header, "Signature ")

	parts := strings.Split(header, ",")
	signatureMap := make(map[string]string)

	for _, part := range parts {
		keyValue := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(keyValue) == 2 {
			key := strings.TrimSpace(keyValue[0])
			value := strings.Trim(keyValue[1], "\"")
			signatureMap[key] = value
		}
	}

	if signatureMap["algorithm"] != "ed25519" {
		return 0, 0, "", "", model.NewSignValidationErr(fmt.Errorf("unsupported algorithm %q: only ed25519 is permitted", signatureMap["algorithm"]))
	}

	createdTimestamp, err := strconv.ParseInt(signatureMap["created"], 10, 64)
	if err != nil {
		// TODO: Return appropriate error code when Error Code Handling Module is ready
		return 0, 0, "", "", fmt.Errorf("invalid created timestamp: %w", err)
	}

	expiredTimestamp, err := strconv.ParseInt(signatureMap["expires"], 10, 64)
	if err != nil {
		return 0, 0, "", "", model.NewSignValidationErr(fmt.Errorf("invalid expires timestamp: %w", err))
	}

	signature := signatureMap["signature"]
	if signature == "" {
		// TODO: Return appropriate error code when Error Code Handling Module is ready
		return 0, 0, "", "", model.NewSignValidationErr(fmt.Errorf("signature missing in header"))
	}

	var subscriberID string
	if keyID := signatureMap["keyId"]; keyID != "" {
		if p := strings.SplitN(keyID, "|", 2); len(p) >= 2 {
			subscriberID = strings.TrimSpace(p[0])
		}
	}

	return createdTimestamp, expiredTimestamp, signature, subscriberID, nil
}

// ValidateAck verifies the 4-line signing string per NFH-004 §3.4.
// body is passed explicitly because the two call sites hash different bodies:
// the on_search request body (step.go) vs the ACK response body (responsestep.go).
func (v *validator) ValidateAck(ctx *model.StepContext, body []byte, signatureHeader, outboundAuthSignature, publicKeyBase64 string) error {
	createdTimestamp, expiredTimestamp, signature, subscriberID, err := parseAuthHeader(signatureHeader)
	if err != nil {
		return model.NewSignValidationErr(fmt.Errorf("error parsing Signature header: %w", err))
	}

	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("error decoding signature: %w", err)
	}

	if err := checkTimestampWindow("AckSignature", createdTimestamp, expiredTimestamp); err != nil {
		return err
	}

	signingString := hashAck(body, createdTimestamp, expiredTimestamp, outboundAuthSignature)

	decodedPublicKey, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return model.NewSignValidationErr(fmt.Errorf("error decoding public key: %w", err))
	}

	if !ed25519.Verify(ed25519.PublicKey(decodedPublicKey), []byte(signingString), signatureBytes) {
		return model.NewSignValidationErr(fmt.Errorf("AckSignature verification failed"))
	}

	if ctx.Request.Header.Get(model.AuthHeaderSubscriber) == signatureHeader {
		if err := checkSubscriberIdentity(ctx, body, subscriberID); err != nil {
			return err
		}
	}
	return nil
}

// checkSubscriberIdentity asserts that the subscriber who signed the request
// (signerID from keyId in the parsed header) matches the caller identity declared
// in the request body context. body is the body that was actually validated so
// callers with different bodies (Validate vs ValidateAck) each pass the right one.
func checkSubscriberIdentity(ctx *model.StepContext, body []byte, signerID string) error {
	expected, _ := ctx.Value(model.ContextKeyRemoteID).(string)

	if expected == "" {
		var payload struct {
			Context map[string]interface{} `json:"context"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			log.Debugf(ctx, "checkSubscriberIdentity: failed to parse body: %v; skipping check", err)
		} else if payload.Context != nil {
			expected = model.ResolveCallerID(payload.Context, ctx.Role)
		}
	}

	if expected == "" {
		log.Debugf(ctx, "checkSubscriberIdentity: no caller ID available for role %v; skipping check", ctx.Role)
		return nil
	}

	if signerID != expected {
		return model.NewSignValidationErr(fmt.Errorf("subscriber identity mismatch: signing subscriber %q does not match declared context identity %q",
			signerID, expected))
	}
	return nil
}

// checkTimestampWindow validates that the current server time falls within
// [created, expires]. prefix ("signature" or "AckSignature") is used in the
// error message to distinguish the two callers in logs.
func checkTimestampWindow(prefix string, createdTimestamp, expiredTimestamp int64) error {
	now := time.Now().UTC()
	current := now.Unix()
	if createdTimestamp > current {
		return model.NewSignValidationErr(fmt.Errorf("%s not yet valid: created=%s, server_time=%s, delta=%ds",
			prefix,
			time.Unix(createdTimestamp, 0).UTC().Format(time.RFC3339),
			now.Format(time.RFC3339),
			createdTimestamp-current,
		))
	}
	if current > expiredTimestamp {
		return model.NewSignValidationErr(fmt.Errorf("%s expired: expires=%s, server_time=%s, expired_by=%ds",
			prefix,
			time.Unix(expiredTimestamp, 0).UTC().Format(time.RFC3339),
			now.Format(time.RFC3339),
			current-expiredTimestamp,
		))
	}
	return nil
}

// hash constructs a signing string for regular request verification.
func hash(payload []byte, createdTimestamp, expiredTimestamp int64) string {
	hasher, _ := blake2b.New512(nil)
	hasher.Write(payload)
	hashSum := hasher.Sum(nil)
	digestB64 := base64.StdEncoding.EncodeToString(hashSum)

	return fmt.Sprintf("(created): %d\n(expires): %d\ndigest: BLAKE-512=%s", createdTimestamp, expiredTimestamp, digestB64)
}

// hashAck constructs the NFH-004 §3.4 four-line signing string for AckSignature
// verification. Mirrors ackSigner.SignAck signing-string construction exactly.
func hashAck(payload []byte, createdTimestamp, expiredTimestamp int64, requestSignature string) string {
	s := hash(payload, createdTimestamp, expiredTimestamp)
	if requestSignature != "" {
		s += "\nrequest-signature: " + requestSignature
	}
	return s
}
