package response

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
)

// legacyAck is the pre-LTS acknowledgment wrapper used for context.version != "2.0.0".
// Wire format: {"ack":{"status":"ACK"}}
type legacyAck struct {
	Status model.Status `json:"status"`
}

// legacyMessage is the pre-LTS message envelope.
// Wire format: {"message":{"ack":{"status":"ACK"},"error":{...}}}
type legacyMessage struct {
	Ack   legacyAck    `json:"ack"`
	Error *model.Error `json:"error,omitempty"`
}

// legacyResponse is the pre-LTS top-level response.
type legacyResponse struct {
	Message legacyMessage `json:"message"`
}

// SendAck sends a synchronous ACK response to the client.
// For context.version "2.0.0" the response uses the LTS envelope:
//
//	{"message":{"status":"ACK","messageId":"<uuid>"}}
//
// All other versions use the legacy envelope:
//
//	{"message":{"ack":{"status":"ACK"}}}
func SendAck(ctx context.Context, w http.ResponseWriter) {
	var data []byte

	if isLTS(ctx) {
		resp := &model.Response{
			Message: model.Message{
				Status:    model.StatusACK,
				MessageID: msgID(ctx),
			},
		}
		data, _ = json.Marshal(resp)
	} else {
		resp := &legacyResponse{
			Message: legacyMessage{
				Ack: legacyAck{Status: model.StatusACK},
			},
		}
		data, _ = json.Marshal(resp)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		http.Error(w, "failed to write response", http.StatusInternalServerError)
	}
}

// nack sends a NACK response with an error payload.
// For context.version "2.0.0":
//
//	{"message":{"status":"NACK","messageId":"<uuid>","error":{...}}}
//
// All other versions:
//
//	{"message":{"ack":{"status":"NACK"},"error":{...}}}
func nack(ctx context.Context, w http.ResponseWriter, err *model.Error, status int) {
	var data []byte

	if isLTS(ctx) {
		resp := &model.Response{
			Message: model.Message{
				Status:    model.StatusNACK,
				MessageID: msgID(ctx),
				Error:     err,
			},
		}
		data, _ = json.Marshal(resp)
	} else {
		resp := &legacyResponse{
			Message: legacyMessage{
				Ack:   legacyAck{Status: model.StatusNACK},
				Error: err,
			},
		}
		data, _ = json.Marshal(resp)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, er := w.Write(data); er != nil {
		log.Debugf(ctx, "Error writing response: %v, MessageID: %s", er, ctx.Value(model.ContextKeyMsgID))
		http.Error(w, fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.ContextKeyMsgID)), http.StatusInternalServerError)
	}
}

// isLTS reports whether the request is a Beckn v2.0.0 LTS request.
func isLTS(ctx context.Context) bool {
	v, _ := ctx.Value(model.ContextKeyProtocolVersion).(string)
	return v == model.ProtocolVersionLTS
}

// msgID returns the message ID stored in the context, or empty string if absent.
func msgID(ctx context.Context) string {
	v, _ := ctx.Value(model.ContextKeyMsgID).(string)
	return v
}

// internalServerError generates an internal server error response.
func internalServerError(ctx context.Context) *model.Error {
	return &model.Error{
		Code:    http.StatusText(http.StatusInternalServerError),
		Message: fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.ContextKeyMsgID)),
	}
}

// SendNack processes different types of errors and sends an appropriate NACK response.
func SendNack(ctx context.Context, w http.ResponseWriter, err error) {
	var schemaErr *model.SchemaValidationErr
	var signErr *model.SignValidationErr
	var badReqErr *model.BadReqErr
	var notFoundErr *model.NotFoundErr

	switch {
	case errors.As(err, &schemaErr):
		nack(ctx, w, schemaErr.BecknError(), http.StatusBadRequest)
	case errors.As(err, &signErr):
		nack(ctx, w, signErr.BecknError(), http.StatusUnauthorized)
	case errors.As(err, &badReqErr):
		nack(ctx, w, badReqErr.BecknError(), http.StatusBadRequest)
	case errors.As(err, &notFoundErr):
		nack(ctx, w, notFoundErr.BecknError(), http.StatusNotFound)
	default:
		nack(ctx, w, internalServerError(ctx), http.StatusInternalServerError)
	}
}
