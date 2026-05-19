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

// SendAck sends a synchronous ACK response to the client.
// For context.version "2.0.0" and later the response uses the v2 envelope:
//
//	{"message":{"status":"ACK","messageId":"<uuid>"}}
//
// All other versions use the pre-v2 envelope:
//
//	{"message":{"ack":{"status":"ACK"}}}
func SendAck(ctx context.Context, w http.ResponseWriter) {
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
}

// nackBecknError maps an error to its Beckn model.Error representation and HTTP
// status code — the same mapping used by SendNack.
func nackBecknError(ctx context.Context, err error) (*model.Error, int) {
	var schemaErr *model.SchemaValidationErr
	var signErr *model.SignValidationErr
	var badReqErr *model.BadReqErr
	var notFoundErr *model.NotFoundErr

	switch {
	case errors.As(err, &schemaErr):
		return schemaErr.BecknError(), http.StatusBadRequest
	case errors.As(err, &signErr):
		return signErr.BecknError(), http.StatusUnauthorized
	case errors.As(err, &badReqErr):
		return badReqErr.BecknError(), http.StatusBadRequest
	case errors.As(err, &notFoundErr):
		return notFoundErr.BecknError(), http.StatusNotFound
	default:
		return internalServerError(ctx), http.StatusInternalServerError
	}
}

// nackBodyBytes returns the serialised NACK response body for the given Beckn
// error. The output is identical to the bytes that nack() writes to the wire.
func nackBodyBytes(ctx context.Context, becknErr *model.Error) []byte {
	var data []byte
	if isAtLeastV2(ctx) {
		resp := &model.Response{
			Message: model.Message{
				Status:    model.StatusNACK,
				MessageID: msgID(ctx),
				Error:     becknErr,
			},
		}
		data, _ = json.Marshal(resp)
	} else {
		resp := &preV2Response{
			Message: preV2Message{
				Ack:   preV2Ack{Status: model.StatusNACK},
				Error: becknErr,
			},
		}
		data, _ = json.Marshal(resp)
	}
	return data
}

// NackBytes returns the NACK response body that SendNack would write for the
// given error, without actually writing it.
//
// Use this when the caller needs to sign (or otherwise process) the response
// body before it is written to the wire. The bytes returned here are guaranteed
// to be identical to what SendNack writes.
func NackBytes(ctx context.Context, err error) []byte {
	becknErr, _ := nackBecknError(ctx, err)
	return nackBodyBytes(ctx, becknErr)
}

// nack sends a NACK response with an error payload.
// For context.version "2.0.0":
//
//	{"message":{"status":"NACK","messageId":"<uuid>","error":{...}}}
//
// All other versions:
//
//	{"message":{"ack":{"status":"NACK"},"error":{...}}}
func nack(ctx context.Context, w http.ResponseWriter, becknErr *model.Error, status int) {
	data := nackBodyBytes(ctx, becknErr)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, er := w.Write(data); er != nil {
		log.Debugf(ctx, "Error writing response: %v, MessageID: %s", er, ctx.Value(model.ContextKeyMsgID))
		http.Error(w, fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.ContextKeyMsgID)), http.StatusInternalServerError)
	}
}

// isAtLeastV2 reports whether the request uses Beckn protocol v2.0.0 or later.
// Uses a major-version check so future versions (2.1.0, 3.0.0, …) also get
// the v2 response envelope automatically.
func isAtLeastV2(ctx context.Context) bool {
	v, _ := ctx.Value(model.ContextKeyProtocolVersion).(string)
	return model.IsAtLeastV2(v)
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
	becknErr, status := nackBecknError(ctx, err)
	nack(ctx, w, becknErr, status)
}
