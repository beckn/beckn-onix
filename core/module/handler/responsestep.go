package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// ---------------------------------------------------------------------------
// Response helpers — ACK / NACK body construction and writing
// ---------------------------------------------------------------------------

// preV2Ack is the pre-v2 acknowledgment wrapper used for context.version < "2.0.0".
// Wire format: {"ack":{"status":"ACK"}}
type preV2Ack struct {
	Status model.Status `json:"status"`
}

// preV2Message is the pre-v2 message envelope.
// Wire format: {"message":{"ack":{"status":"ACK"},"error":{...}}}
type preV2Message struct {
	Ack   preV2Ack     `json:"ack"`
	Error *model.Error `json:"error,omitempty"`
}

// preV2Response is the pre-v2 top-level response.
type preV2Response struct {
	Message preV2Message `json:"message"`
}

// sendAck sends a synchronous ACK response to the client.
// For context.version "2.0.0" and later the response uses the v2 envelope:
//
//	{"message":{"status":"ACK","messageId":"<uuid>"}}
//
// All other versions use the pre-v2 envelope:
//
//	{"message":{"ack":{"status":"ACK"}}}
func sendAck(ctx context.Context, w http.ResponseWriter) []byte {
	var data []byte
	if isAtLeastV2(ctx) {
		resp := &model.Response{
			Message: model.Message{
				Status:    model.StatusACK,
				MessageID: msgID(ctx),
			},
		}
		data, _ = json.Marshal(resp)
	} else {
		resp := &preV2Response{
			Message: preV2Message{
				Ack: preV2Ack{Status: model.StatusACK},
			},
		}
		data, _ = json.Marshal(resp)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		http.Error(w, "failed to write response", http.StatusInternalServerError)
	}
	return data
}

// nackBecknError maps an error to its Beckn model.Error representation, HTTP
// status code, and body-level status string. The body status is StatusNACK for
// all error types except AckNoCallbackErr on v2+ requests, where it reflects the
// caller-supplied Status (ACK or NACK).
func nackBecknError(ctx context.Context, err error) (*model.Error, int, model.Status) {
	var schemaErr *model.SchemaValidationErr
	var signErr *model.SignValidationErr
	var badReqErr *model.BadReqErr
	var notFoundErr *model.NotFoundErr
	var ackNoCallbackErr *model.AckNoCallbackErr

	switch {
	case errors.As(err, &schemaErr):
		return schemaErr.BecknError(), http.StatusBadRequest, model.StatusNACK
	case errors.As(err, &signErr):
		return signErr.BecknError(), http.StatusUnauthorized, model.StatusNACK
	case errors.As(err, &badReqErr):
		return badReqErr.BecknError(), http.StatusBadRequest, model.StatusNACK
	case errors.As(err, &notFoundErr):
		return notFoundErr.BecknError(), http.StatusNotFound, model.StatusNACK
	case errors.As(err, &ackNoCallbackErr):
		if !isAtLeastV2(ctx) {
			return internalServerError(ctx), http.StatusInternalServerError, model.StatusNACK
		}
		return ackNoCallbackErr.BecknError(), http.StatusAccepted, ackNoCallbackErr.Status
	default:
		return internalServerError(ctx), http.StatusInternalServerError, model.StatusNACK
	}
}

// nackBodyBytes returns the serialised response body for the given Beckn error
// and body-level status. The output is identical to the bytes that nack() writes
// to the wire.
func nackBodyBytes(ctx context.Context, becknErr *model.Error, bodyStatus model.Status) []byte {
	var data []byte
	if isAtLeastV2(ctx) {
		resp := &model.Response{
			Message: model.Message{
				Status:    bodyStatus,
				MessageID: msgID(ctx),
				Error:     becknErr,
			},
		}
		data, _ = json.Marshal(resp)
	} else {
		resp := &preV2Response{
			Message: preV2Message{
				Ack:   preV2Ack{Status: bodyStatus},
				Error: becknErr,
			},
		}
		data, _ = json.Marshal(resp)
	}
	return data
}

// nackBytes returns the response body that sendNack would write for the given
// error, without actually writing it. Use this to sign the body before it is
// flushed to the wire (see stdHandler.signNackResponse).
func nackBytes(ctx context.Context, err error) []byte {
	becknErr, _, bodyStatus := nackBecknError(ctx, err)
	return nackBodyBytes(ctx, becknErr, bodyStatus)
}

// nack writes an error response body with the given HTTP status code.
// For context.version "2.0.0":
//
//	{"message":{"status":"<bodyStatus>","messageId":"<uuid>","error":{...}}}
//
// All other versions:
//
//	{"message":{"ack":{"status":"<bodyStatus>"},"error":{...}}}
func nack(ctx context.Context, w http.ResponseWriter, becknErr *model.Error, httpStatus int, bodyStatus model.Status) []byte {
	data := nackBodyBytes(ctx, becknErr, bodyStatus)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	if _, er := w.Write(data); er != nil {
		log.Debugf(ctx, "Error writing response: %v, MessageID: %s", er, ctx.Value(model.ContextKeyMsgID))
		http.Error(w, fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.ContextKeyMsgID)), http.StatusInternalServerError)
	}
	return data
}

// sendNack maps err to its Beckn error type and sends the appropriate response.
func sendNack(ctx context.Context, w http.ResponseWriter, err error) []byte {
	becknErr, httpStatus, bodyStatus := nackBecknError(ctx, err)
	return nack(ctx, w, becknErr, httpStatus, bodyStatus)
}

// isAtLeastV2 reports whether the request uses Beckn protocol v2.0.0 or later.
func isAtLeastV2(ctx context.Context) bool {
	v, _ := ctx.Value(model.ContextKeyProtocolVersion).(string)
	return model.IsAtLeastV2(v)
}

// msgID returns the message ID stored in the context, or empty string if absent.
func msgID(ctx context.Context) string {
	v, _ := ctx.Value(model.ContextKeyMsgID).(string)
	return v
}

// internalServerError generates a Beckn internal-server-error payload.
func internalServerError(ctx context.Context) *model.Error {
	return &model.Error{
		Code:    "NET_INTERNAL_ERROR",
		Message: fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.ContextKeyMsgID)),
	}
}

// ---------------------------------------------------------------------------
// ackSignerStep — signs synchronous responses on the Receiver side
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
// rctx is nil on the publisher path: ONIX controls the ACK body, so the digest
// is computed over the deterministic body that sendAck will write.
// rctx is non-nil on the URL-routing path: the body was pre-read by the handler
// and is available in rctx.Body. rctx.Header is a shared reference to
// resp.Header, so setting Signature here is forwarded by ReverseProxy without
// any explicit write-back.
//
// This step signs ALL upstream responses including any status code — per
// NFH-007 CON-004-02 every synchronous response MUST carry a Signature header.
// ONIX-generated pipeline NACKs are signed separately via stdHandler.signNackResponse
// (called before sendNack) because the pipelineErr guard prevents
// response steps from running on pipeline failures.
func (a *ackSignerStep) RunOnResponse(ctx *model.StepContext, rctx *model.ResponseStepContext) error {
	if !model.IsAtLeastV2(ctx.ProtocolVersion) {
		return nil
	}
	if len(ctx.SubID) == 0 {
		return model.NewBadReqErr(fmt.Errorf("subscriberID not set"))
	}

	if rctx != nil {
		// URL-routing path: body is pre-read; rctx.Header is a shared reference
		// to resp.Header so the Signature reaches ReverseProxy automatically.
		sigHeader, err := a.computeSigHeader(ctx, rctx.Body)
		if err != nil {
			return err
		}
		rctx.Header.Set("Signature", sigHeader)
		return nil
	}

	// Publisher / no-route path: ONIX writes the ACK — build the deterministic
	// body that sendAck will write so the digest matches.
	ackBody, err := buildAckBody(ctx.ProtocolVersion, ctx.MessageID)
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
// validateAckSignatureStep — verifies synchronous response signatures on the Caller side
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
//   - publisher path (rctx == nil) — async ACKs have no Signature header
//   - protocol version < 2.0.0 — pre-v2 flows are unaffected
func (v *validateAckSignatureStep) RunOnResponse(ctx *model.StepContext, rctx *model.ResponseStepContext) error {
	if rctx == nil {
		return nil
	}
	if !model.IsAtLeastV2(ctx.ProtocolVersion) {
		return nil
	}

	sigHeader := rctx.Header.Get("Signature")
	if sigHeader == "" {
		log.Warnf(ctx, "validateAckSign: missing Signature response header on v2 response (status=%d) — degraded trust", rctx.StatusCode)
		return nil
	}

	// The outbound Authorization was set on ctx.Request.Header by the sign step
	// (step.go signStep.Run). In ModifyResponse, ctx is captured from ServeHTTP
	// so the header value is the one sent to the upstream BPP/BAP.
	outboundAuth := extractAuthSignature(ctx.Request.Header.Get(model.AuthHeaderSubscriber))

	parsed, err := parseHeader(sigHeader)
	if err != nil {
		log.Warnf(ctx, "validateAckSign: failed to parse Signature header keyId: %v — degraded trust", err)
		return nil
	}

	publicKey, _, err := v.km.LookupNPKeys(ctx, parsed.SubscriberID, parsed.UniqueID)
	if err != nil {
		log.Warnf(ctx, "validateAckSign: failed to look up public key for %s: %v — degraded trust", parsed.SubscriberID, err)
		return nil
	}

	if err := v.signValidator.ValidateAck(ctx, rctx.Body, sigHeader, outboundAuth, publicKey, false); err != nil {
		log.Warnf(ctx, "validateAckSign: Signature verification failed (status=%d): %v — degraded trust", rctx.StatusCode, err)
		return nil
	}

	log.Debugf(ctx, "validateAckSign: Signature verified OK (status=%d)", rctx.StatusCode)
	return nil
}
