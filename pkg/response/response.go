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

// SendAck sends an acknowledgment response (ACK) to the client.
// counterSign is the v2.0.0 LTS counter-signature string; pass an empty string
// for legacy protocol versions and the field will be omitted from the response.
func SendAck(w http.ResponseWriter, counterSign string) {
	resp := &model.Response{
		Message: model.Message{
			Ack: model.Ack{
				Status:      model.StatusACK,
				CounterSign: counterSign,
			},
		},
	}

	data, _ := json.Marshal(resp) //should not fail here

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(data)
	if err != nil {
		http.Error(w, "failed to write response", http.StatusInternalServerError)
		return
	}
}

// nack sends a negative acknowledgment (NACK) response with an error message.
// When the request carries Beckn protocol version "2.0.0" (LTS), the error moves
// to the top-level response field and field names change to errorCode/errorMessage.
func nack(ctx context.Context, w http.ResponseWriter, err *model.Error, status int) {
	resp := &model.Response{
		Message: model.Message{Ack: model.Ack{Status: model.StatusNACK}},
	}
	if version, _ := ctx.Value(model.ContextKeyProtocolVersion).(string); version == model.ProtocolVersionLTS {
		resp.Error = err.ToV2Error() // v2: top-level error, renamed fields
	} else {
		resp.Message.Error = err // legacy: error nested inside message
	}
	data, _ := json.Marshal(resp) // should not fail here

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, er := w.Write(data)
	if er != nil {
		log.Debugf(ctx, "Error writing response: %v, MessageID: %s", er, ctx.Value(model.ContextKeyMsgID))
		http.Error(w, fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.ContextKeyMsgID)), http.StatusInternalServerError)
		return
	}
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
		return
	case errors.As(err, &signErr):
		nack(ctx, w, signErr.BecknError(), http.StatusUnauthorized)
		return
	case errors.As(err, &badReqErr):
		nack(ctx, w, badReqErr.BecknError(), http.StatusBadRequest)
		return
	case errors.As(err, &notFoundErr):
		nack(ctx, w, notFoundErr.BecknError(), http.StatusNotFound)
		return
	default:
		nack(ctx, w, internalServerError(ctx), http.StatusInternalServerError)
		return
	}
}
