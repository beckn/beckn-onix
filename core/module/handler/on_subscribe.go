package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
)

// NewOnSubscribeHandler initializes and returns the HTTP handler
func NewOnSubscribeHandler(ctx context.Context, km definition.KeyManager, dp definition.Decrypter) (http.Handler, error) {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req model.OnSubscribeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("failed to decode request: %v", err), http.StatusBadRequest)
			return
		}

		// Validate required fields
		if req.MessageID == "" || req.Challenge == "" {
			http.Error(w, "message_id and challenge are required", http.StatusBadRequest)
			return
		}

		// Fetch keys from Key Manager
		keySet, _, err := km.SigningPrivateKey(ctx, req.MessageID)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get keys for message_id %s: %v", req.MessageID, err), http.StatusInternalServerError)
			return
		}

		// Decode and decrypt the challenge string
		encBytes, err := base64.StdEncoding.DecodeString(req.Challenge)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to decode challenge: %v", err), http.StatusInternalServerError)
			return
		}

		plainText, err := dp.Decrypt(ctx, string(encBytes), keySet, "")
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to decrypt challenge: %v", err), http.StatusInternalServerError)
			return
		}

		// Prepare response
		resp := model.OnSubscribeResponse{
			Answer:    plainText,
			MessageID: req.MessageID,
		}

		respJSON, err := json.Marshal(resp)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to marshal response: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(respJSON)
	}), nil
}
