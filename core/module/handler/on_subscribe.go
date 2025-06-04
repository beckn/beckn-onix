package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
)

type onSubscribeHandler struct {
	km definition.KeyManager
	dp definition.Decrypter
}

func (s *onSubscribeHandler) Run(ctx *model.StepContext) error {
	var req model.OnSubscribeRequest
	if err := json.Unmarshal(ctx.Body, &req); err != nil {
		return model.NewBadReqErr(fmt.Errorf("invalid request body: %w", err))
	}

	// Validate required fields
	if req.MessageID == "" || req.Challenge == "" {
		return model.NewBadReqErr(fmt.Errorf("message_id and challenge are required"))
	}

	// Fetch key set from Key Manager using message ID
	keySet, _, err := s.km.SigningPrivateKey(ctx, req.MessageID)
	if err != nil {
		return fmt.Errorf("failed to get keys for message_id %s: %w", req.MessageID, err)
	}

	// // Decode and decrypt the challenge
	encBytes, err := base64.StdEncoding.DecodeString(req.Challenge)
	if err != nil {
		return fmt.Errorf("failed to decode challenge: %w", err)
	}

	// Decrypt using the Decrypter interface
	plainText, err := s.dp.Decrypt(ctx, string(encBytes), keySet, "")
	if err != nil {
		return fmt.Errorf("failed to decrypt challenge: %w", err)
	}

	// Prepare the response
	resp := model.OnSubscribeResponse{
		Answer:    plainText,
		MessageID: req.MessageID,
	}

	respJSON, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	ctx.Body = respJSON
	return nil
}

// HandleOnSubscribe handles the 'on_subscribe' request by initializing and executing the onSubscribeHandler logic.
func HandleOnSubscribe(ctx *model.StepContext, km definition.KeyManager, dp definition.Decrypter) error {
	handler := &onSubscribeHandler{
		km: km,
		dp: dp,
	}
	return handler.Run(ctx)
}
