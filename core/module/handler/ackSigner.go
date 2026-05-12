package handler

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// ackSignerStep signs the synchronous Ack response per NFH-004 §3.4 and sets
// the Signature response header. It is a ResponseStep — it runs after all
// inbound steps succeed, before the ACK body is written to the caller.
//
// For protocol versions < 2.0.0 the step is a no-op so legacy flows are
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

// RunOnResponse signs the Ack response and sets the Signature header.
func (a *ackSignerStep) RunOnResponse(ctx *model.StepContext) error {
	if !model.IsAtLeastV2(ctx.ProtocolVersion) {
		return nil
	}

	if len(ctx.SubID) == 0 {
		return model.NewBadReqErr(fmt.Errorf("subscriberID not set"))
	}

	// Build the deterministic ACK body for the digest — same shape that SendAck
	// will write, so the signature covers the exact bytes the caller receives.
	ackBody, err := buildAckBody(ctx.ProtocolVersion, ctx.MessageID)
	if err != nil {
		return fmt.Errorf("ackSigner: failed to build ack body: %w", err)
	}

	keySet, err := a.km.Keyset(ctx, ctx.SubID)
	if err != nil {
		return fmt.Errorf("ackSigner: failed to get signing key: %w", err)
	}

	createdAt := time.Now().Unix()
	validTill := time.Now().Add(5 * time.Minute).Unix()

	sig, err := a.signer.SignAck(ctx, ackBody, ctx.InboundAuthSignature, keySet.SigningPrivate, createdAt, validTill)
	if err != nil {
		return fmt.Errorf("ackSigner: failed to sign ack: %w", err)
	}

	ctx.RespHeader.Set("Signature", buildSignatureHeader(ctx.SubID, keySet.UniqueKeyID, createdAt, validTill, sig))
	return nil
}

// buildAckBody constructs the deterministic JSON ACK body for the given protocol
// version and messageID — mirroring the LTS branch of response.SendAck.
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
