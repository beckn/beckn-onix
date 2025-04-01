package response

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/beckn/beckn-onix/pkg/model"
)

// SendAck sends an acknowledgment response (ACK) to the client.
func SendAck(w http.ResponseWriter) {
	resp := &model.Response{
		Message: model.Message{
			Ack: model.Ack{
				Status: model.StatusACK,
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
func nack(ctx context.Context, w http.ResponseWriter, err *model.Error, status int) {
	resp := &model.Response{
		Message: model.Message{
			Ack: model.Ack{
				Status: model.StatusNACK,
			},
			Error: err,
		},
	}
	data, _ := json.Marshal(resp) //should not fail here

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, er := w.Write(data)
	if er != nil {
		fmt.Printf("Error writing response: %v, MessageID: %s", er, ctx.Value(model.MsgIDKey))
		http.Error(w, fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.MsgIDKey)), http.StatusInternalServerError)
		return
	}
}

// internalServerError generates an internal server error response.
func internalServerError(ctx context.Context) *model.Error {
	return &model.Error{
		Code:    http.StatusText(http.StatusInternalServerError),
		Message: fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.MsgIDKey)),
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
