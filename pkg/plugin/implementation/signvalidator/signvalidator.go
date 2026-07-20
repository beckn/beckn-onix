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

const (
	defaultClockSkewTolerance = 5 * time.Second
	maxClockSkewTolerance     = 10 * time.Second
)

// AUT_* codes reachable from this plugin's failure modes. AUT_RATE_LIMITED,
// AUT_DOMAIN_NOT_ALLOWED, and AUT_REPLAY_DETECTED have no corresponding checks
// in signvalidator today and are intentionally absent from this list.
const (
	// codeSignatureMissing covers an absent signature value in the Authorization header.
	codeSignatureMissing = "AUT_SIGNATURE_MISSING"
	// codeSignatureInvalid is the generic bucket for malformed headers, undecodable
	// signature/key material, expired/not-yet-valid timestamp windows, and failed
	// cryptographic verification — none of these have a more specific taxonomy value.
	codeSignatureInvalid = "AUT_SIGNATURE_INVALID"
	// codeUnauthorizedAction covers a cryptographically valid signature whose signer
	// identity does not match the identity declared in the request context.
	codeUnauthorizedAction = "AUT_UNAUTHORIZED_ACTION"
)

// Config struct for Verifier.
type Config struct {
	// ClockSkewTolerance is the maximum future drift allowed for the `created`
	// field. nil means use the spec default (5 s). Set to a zero-value pointer
	// (&0) to enforce strict same-second validation. NFOs may override this
	// per-subnet via the plugin config key "clockSkewToleranceSeconds".
	// The `expires` field always uses zero tolerance regardless of this value.
	ClockSkewTolerance *time.Duration
}

// validator implements the validator interface.
type validator struct {
	clockSkewTolerance time.Duration // resolved at construction; never changes
}

// New creates a new Verifier instance.
// The caller's Config is never mutated.
func New(ctx context.Context, config *Config) (*validator, func() error, error) {
	tolerance := defaultClockSkewTolerance
	if config.ClockSkewTolerance != nil {
		tolerance = *config.ClockSkewTolerance
	}
	if tolerance > maxClockSkewTolerance {
		log.Warnf(ctx, "signvalidator: clockSkewToleranceSeconds=%ds exceeds recommended maximum of %ds; large tolerances widen the replay window",
			int(tolerance.Seconds()), int(maxClockSkewTolerance.Seconds()))
	}
	return &validator{clockSkewTolerance: tolerance}, nil, nil
}

// Validate verifies the 3-line signing string for inbound requests.
func (v *validator) Validate(ctx *model.StepContext, header string, publicKeyBase64 string, checkIdentity bool) error {
	createdTimestamp, expiredTimestamp, signature, subscriberID, err := parseAuthHeader(header)
	if err != nil {
		// parseAuthHeader always returns an already-classified *model.SignValidationErr;
		// wrap with plain fmt.Errorf (not model.NewSignValidationErr) so errors.As still
		// finds that inner classification instead of shadowing it with a new, less
		// specific wrapper.
		return fmt.Errorf("error parsing header: %w", err)
	}

	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return model.NewCodedSignValidationErr(codeSignatureInvalid, fmt.Errorf("error decoding signature: %w", err))
	}

	if err := checkTimestampWindow("signature", createdTimestamp, expiredTimestamp, v.clockSkewTolerance); err != nil {
		return err
	}

	createdTime := time.Unix(createdTimestamp, 0)
	expiredTime := time.Unix(expiredTimestamp, 0)

	signingString := hash(ctx.Body, createdTime.Unix(), expiredTime.Unix())

	decodedPublicKey, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return model.NewCodedSignValidationErr(codeSignatureInvalid, fmt.Errorf("error decoding public key: %w", err))
	}

	if !ed25519.Verify(ed25519.PublicKey(decodedPublicKey), []byte(signingString), signatureBytes) {
		return model.NewCodedSignValidationErr(codeSignatureInvalid, fmt.Errorf("signature verification failed"))
	}

	if checkIdentity {
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
		return 0, 0, "", "", model.NewCodedSignValidationErr(codeSignatureInvalid, fmt.Errorf("unsupported algorithm %q: only ed25519 is permitted", signatureMap["algorithm"]))
	}

	createdTimestamp, err := strconv.ParseInt(signatureMap["created"], 10, 64)
	if err != nil {
		return 0, 0, "", "", model.NewCodedSignValidationErr(codeSignatureInvalid, fmt.Errorf("invalid created timestamp: %w", err))
	}

	expiredTimestamp, err := strconv.ParseInt(signatureMap["expires"], 10, 64)
	if err != nil {
		return 0, 0, "", "", model.NewCodedSignValidationErr(codeSignatureInvalid, fmt.Errorf("invalid expires timestamp: %w", err))
	}

	signature := signatureMap["signature"]
	if signature == "" {
		return 0, 0, "", "", model.NewCodedSignValidationErr(codeSignatureMissing, fmt.Errorf("signature missing in header"))
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
func (v *validator) ValidateAck(ctx *model.StepContext, body []byte, signatureHeader, outboundAuthSignature, publicKeyBase64 string, checkIdentity bool) error {
	createdTimestamp, expiredTimestamp, signature, subscriberID, err := parseAuthHeader(signatureHeader)
	if err != nil {
		// parseAuthHeader always returns an already-classified *model.SignValidationErr;
		// wrap with plain fmt.Errorf (not model.NewSignValidationErr) so errors.As still
		// finds that inner classification instead of shadowing it with a new, less
		// specific wrapper.
		return fmt.Errorf("error parsing Signature header: %w", err)
	}

	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return model.NewCodedSignValidationErr(codeSignatureInvalid, fmt.Errorf("error decoding signature: %w", err))
	}

	if err := checkTimestampWindow("AckSignature", createdTimestamp, expiredTimestamp, v.clockSkewTolerance); err != nil {
		return err
	}

	signingString := hashAck(body, createdTimestamp, expiredTimestamp, outboundAuthSignature)

	decodedPublicKey, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return model.NewCodedSignValidationErr(codeSignatureInvalid, fmt.Errorf("error decoding public key: %w", err))
	}

	if !ed25519.Verify(ed25519.PublicKey(decodedPublicKey), []byte(signingString), signatureBytes) {
		return model.NewCodedSignValidationErr(codeSignatureInvalid, fmt.Errorf("AckSignature verification failed"))
	}

	if checkIdentity {
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
		return model.NewCodedSignValidationErr(codeUnauthorizedAction, fmt.Errorf("subscriber identity mismatch: signing subscriber %q does not match declared context identity %q",
			signerID, expected))
	}
	return nil
}

// checkTimestampWindow validates the created/expires timestamp pair.
// clockSkewTolerance is applied to `created` only — the spec permits a
// configurable forward drift window to accommodate NTP skew between NPs.
// `expires` is always checked with zero tolerance per the spec.
func checkTimestampWindow(prefix string, createdTimestamp, expiredTimestamp int64, clockSkewTolerance time.Duration) error {
	now := time.Now().UTC()
	// Accept created values up to clockSkewTolerance in the future.
	deadline := now.Add(clockSkewTolerance)
	if time.Unix(createdTimestamp, 0).UTC().After(deadline) {
		return model.NewCodedSignValidationErr(codeSignatureInvalid, fmt.Errorf("%s not yet valid: created=%s, server_time=%s, tolerance=%ds, overshoot=%ds",
			prefix,
			time.Unix(createdTimestamp, 0).UTC().Format(time.RFC3339),
			now.Format(time.RFC3339),
			int(clockSkewTolerance.Seconds()),
			createdTimestamp-deadline.Unix(),
		))
	}
	// expires: zero tolerance — reject without exception.
	if now.Unix() > expiredTimestamp {
		return model.NewCodedSignValidationErr(codeSignatureInvalid, fmt.Errorf("%s expired: expires=%s, server_time=%s, expired_by=%ds",
			prefix,
			time.Unix(expiredTimestamp, 0).UTC().Format(time.RFC3339),
			now.Format(time.RFC3339),
			now.Unix()-expiredTimestamp,
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
