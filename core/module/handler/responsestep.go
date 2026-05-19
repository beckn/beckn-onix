package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// ---------------------------------------------------------------------------
// ackSignerStep
// ---------------------------------------------------------------------------

// ackSignerStep signs the synchronous Ack response per NFH-004 §3.4 and sets
// the Signature response header. It is a ResponseStep — it runs after all
// inbound steps succeed, before the ACK body is written to the caller.
//
// For protocol versions < 2.0.0 the step is a no-op so pre-v2 flows are
// unaffected. For 2.0.0 and any later version AckSigning is applied.
type ackSignerStep struct {
	signer definition.Signer
	km     definition.KeyManager
}

// newAckSignerStep returns a new ackSignerStep after validating its dependencies.
func newAckSignerStep(signer definition.Signer, km definition.KeyManager) (definition.ResponseStep, error) {
	if signer == nil {
		return nil, fmt.Errorf("invalid config: Signer plugin not configured")
	}
	if km == nil {
		return nil, fmt.Errorf("invalid config: KeyManager plugin not configured")
	}
	return &ackSignerStep{signer: signer, km: km}, nil
}

// computeSigHeader signs body and returns the Signature header value string.
// It is the shared primitive used by both RunOnResponse and signBodyAndSetHeader.
func (a *ackSignerStep) computeSigHeader(ctx *model.StepContext, body []byte) (string, error) {
	keySet, err := a.km.Keyset(ctx, ctx.SubID)
	if err != nil {
		return "", fmt.Errorf("ackSigner: failed to get signing key: %w", err)
	}
	createdAt := time.Now().Unix()
	validTill := time.Now().Add(5 * time.Minute).Unix()
	sig, err := a.signer.SignAck(ctx, body, ctx.InboundAuthSignature, keySet.SigningPrivate, createdAt, validTill)
	if err != nil {
		return "", fmt.Errorf("ackSigner: failed to sign: %w", err)
	}
	return buildSignatureHeader(ctx.SubID, keySet.UniqueKeyID, createdAt, validTill, sig), nil
}

// signBodyAndSetHeader signs body and sets the Signature header on ctx.RespHeader.
// Used by the publisher/no-route path and by stdHandler.signNackResponse (pipeline NACKs).
//
// NOTE: Do NOT use this on the URL-routing path — there the Signature must go
// on resp.Header (forwarded by ReverseProxy), not on ctx.RespHeader.
func (a *ackSignerStep) signBodyAndSetHeader(ctx *model.StepContext, body []byte) error {
	sig, err := a.computeSigHeader(ctx, body)
	if err != nil {
		return err
	}
	ctx.RespHeader.Set("Signature", sig)
	return nil
}

// RunOnResponse signs the Ack response and sets the Signature header.
//
// resp is nil on the publisher path: ONIX controls the ACK body, so the digest
// is computed over the deterministic body that SendAck will write.
// resp is non-nil on the URL-routing path: the body comes from the upstream
// app via ReverseProxy, so the digest covers the actual bytes the caller
// receives. In both cases the Signature header value is identical in structure.
//
// This step signs ALL upstream responses including any status code — per
// NFH-007 CON-004-02 every synchronous response MUST carry a Signature header.
// ONIX-generated pipeline NACKs are signed separately via stdHandler.signNackResponse
// (called before sendNack) because the pipelineErr guard prevents
// response steps from running on pipeline failures.
func (a *ackSignerStep) RunOnResponse(ctx *model.StepContext, resp *http.Response) error {
	if !model.IsAtLeastV2(ctx.ProtocolVersion) {
		return nil
	}

	if len(ctx.SubID) == 0 {
		return model.NewBadReqErr(fmt.Errorf("subscriberID not set"))
	}

	var ackBody []byte
	var err error

	if resp != nil {
		// URL-routing path: sign the actual upstream response body so the
		// digest covers exactly what the caller will receive.
		ackBody, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("ackSigner: failed to read upstream response body: %w", err)
		}
		// Restore the body so the proxy can forward it unchanged.
		resp.Body = io.NopCloser(bytes.NewReader(ackBody))

		// Set Signature on the upstream response so ReverseProxy forwards it
		// to the caller. Do NOT set ctx.RespHeader on this path — that header
		// map belongs to the current handler's own response, not the proxied one.
		sigHeader, serr := a.computeSigHeader(ctx, ackBody)
		if serr != nil {
			return serr
		}
		resp.Header.Set("Signature", sigHeader)
		return nil
	}

	// Publisher / no-route path: ONIX writes the ACK — build the deterministic
	// body that SendAck will write so the digest matches.
	ackBody, err = buildAckBody(ctx.ProtocolVersion, ctx.MessageID)
	if err != nil {
		return fmt.Errorf("ackSigner: failed to build ack body: %w", err)
	}
	// signBodyAndSetHeader writes to ctx.RespHeader which IS the http.ResponseWriter
	// header map — the Signature header will be flushed when WriteHeader is called.
	return a.signBodyAndSetHeader(ctx, ackBody)
}

// buildAckBody constructs the deterministic JSON ACK body for the given protocol
// version and messageID — mirroring the v2 branch of sendAck.
func buildAckBody(protocolVersion, messageID string) ([]byte, error) {
	resp := &model.Response{
		Message: model.Message{
			Status:    model.StatusACK,
			MessageID: messageID,
		},
	}
	return json.Marshal(resp)
}

// buildSignatureHeader constructs the Signature response header value per NFH-004 §3.4.
func buildSignatureHeader(subID, keyID string, createdAt, validTill int64, signature string) string {
	return fmt.Sprintf(
		"Signature keyId=\"%s|%s|ed25519\",algorithm=\"ed25519\",created=\"%d\",expires=\"%d\",headers=\"(created) (expires) digest request-signature\",signature=\"%s\"",
		subID, keyID, createdAt, validTill, signature,
	)
}

// ---------------------------------------------------------------------------
// validateAckSignatureStep
// ---------------------------------------------------------------------------

// validateAckSignatureStep verifies the Signature response header on every
// synchronous ACK received by a Caller handler. It is the symmetric counterpart
// to ackSignerStep (Receiver side) and together they provide bilateral
// non-repudiation for the synchronous leg per NFH-004 §3.4.
//
// # Why PayloadStore is not needed here
//
// The outbound Authorization header is set on ctx.Request.Header by the sign
// step (step.go signStep.Run) before the proxy forwards the request.
// ModifyResponse captures the same stepCtx from ServeHTTP, so
// ctx.Request.Header.Get(model.AuthHeaderSubscriber) returns the exact value
// the BPP used as request-signature when signing the ACK.
//
// A service restart between sending the request and receiving the ACK is not
// a concern on this synchronous path: if ONIX restarts, the TCP connection to
// the BPP drops, the BPP never sends the ACK back, and ONIX never needs to
// verify it. There is no scenario where ONIX restarts and later receives the
// ACK on the same call.
//
// # This does NOT apply to async callback verification (#679)
//
// Callbacks (on_search, on_select, …) arrive independently of the original
// outbound request. A restart between sending the request and receiving the
// callback means the outbound Authorization is gone from memory. PayloadStore
// is mandatory for #679 — the outbound Authorization must be persisted before
// forwarding so it survives restarts and can be retrieved when the callback
// arrives on a fresh instance.
//
// No-ops:
//   - publisher path (resp == nil) — no Signature header exists on async ACKs
//   - protocol version < 2.0.0 — pre-v2 flows are unaffected
type validateAckSignatureStep struct {
	signValidator definition.SignValidator
	km            definition.KeyManager
}

// newValidateAckSignatureStep returns a new validateAckSignatureStep after
// validating its dependencies.
func newValidateAckSignatureStep(sv definition.SignValidator, km definition.KeyManager) (definition.ResponseStep, error) {
	if sv == nil {
		return nil, fmt.Errorf("invalid config: SignValidator plugin not configured")
	}
	if km == nil {
		return nil, fmt.Errorf("invalid config: KeyManager plugin not configured")
	}
	return &validateAckSignatureStep{signValidator: sv, km: km}, nil
}

// RunOnResponse verifies the Signature response header on the ACK.
//
// Per NFH-007 CON-004-02 every synchronous response (any status code) MUST
// carry a Signature header. Per NFH-007 conformance, a missing or invalid
// Signature SHOULD NOT invalidate the transaction — instead we degrade trust
// (log a warning and continue) rather than hard-rejecting.
//
// No-ops:
//   - publisher path (resp == nil) — async ACKs have no Signature header
//   - protocol version < 2.0.0 — pre-v2 flows are unaffected
func (v *validateAckSignatureStep) RunOnResponse(ctx *model.StepContext, resp *http.Response) error {
	if resp == nil {
		// Publisher path — the ACK is sent asynchronously; no Signature header.
		return nil
	}
	if !model.IsAtLeastV2(ctx.ProtocolVersion) {
		return nil
	}

	sigHeader := resp.Header.Get("Signature")
	if sigHeader == "" {
		// Missing Signature on any response (including error responses that the
		// peer's ONIX hasn't yet been upgraded to sign) → degrade, not reject.
		log.Warnf(ctx, "validateAckSign: missing Signature response header on v2 response (status=%d) — degraded trust", resp.StatusCode)
		return nil
	}

	// The outbound Authorization was set on ctx.Request.Header by the sign step
	// (step.go signStep.Run). In ModifyResponse, ctx is captured from ServeHTTP
	// so the header value is the one sent to the upstream BPP/BAP.
	outboundAuth := extractAuthSignature(ctx.Request.Header.Get(model.AuthHeaderSubscriber))

	// Read and restore the ACK body for digest computation.
	ackBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Warnf(ctx, "validateAckSign: failed to read response body: %v — degraded trust", err)
		return nil
	}
	resp.Body = io.NopCloser(bytes.NewReader(ackBody))

	// Parse the keyId from the Signature header to identify the signer.
	parsed, err := parseHeader(sigHeader)
	if err != nil {
		log.Warnf(ctx, "validateAckSign: failed to parse Signature header keyId: %v — degraded trust", err)
		return nil
	}

	// Look up the signer's public key from the registry via KeyManager.
	publicKey, _, err := v.km.LookupNPKeys(ctx, parsed.SubscriberID, parsed.UniqueID)
	if err != nil {
		log.Warnf(ctx, "validateAckSign: failed to look up public key for %s: %v — degraded trust", parsed.SubscriberID, err)
		return nil
	}

	if err := v.signValidator.ValidateAck(ctx, ackBody, sigHeader, outboundAuth, publicKey); err != nil {
		log.Warnf(ctx, "validateAckSign: Signature verification failed (status=%d): %v — degraded trust", resp.StatusCode, err)
		return nil
	}

	log.Debugf(ctx, "validateAckSign: Signature verified OK (status=%d)", resp.StatusCode)
	return nil
}
