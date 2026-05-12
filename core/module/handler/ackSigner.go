package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
//
// resp is nil on the publisher path: ONIX controls the ACK body, so the digest
// is computed over the deterministic body that SendAck will write.
// resp is non-nil on the URL-routing path: the body comes from the upstream
// app via ReverseProxy, so the digest covers the actual bytes the caller
// receives. In both cases the Signature header value is identical in structure.
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
	} else {
		// Publisher path: ONIX writes the ACK — build the deterministic body
		// that SendAck will write so the digest matches.
		ackBody, err = buildAckBody(ctx.ProtocolVersion, ctx.MessageID)
		if err != nil {
			return fmt.Errorf("ackSigner: failed to build ack body: %w", err)
		}
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

	sigHeader := buildSignatureHeader(ctx.SubID, keySet.UniqueKeyID, createdAt, validTill, sig)
	if resp != nil {
		// URL-routing path: set on the upstream response so ReverseProxy
		// forwards it to the caller.
		resp.Header.Set("Signature", sigHeader)
	} else {
		// Publisher path: set on the response writer headers.
		ctx.RespHeader.Set("Signature", sigHeader)
	}
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
