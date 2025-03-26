package response

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/beckn/beckn-onix/pkg/model"
)

type ErrorType string

type errorResponseWriter struct{}

func (e *errorResponseWriter) Header() http.Header {
	return http.Header{}
}

func (e *errorResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("write error")
}

func (e *errorResponseWriter) WriteHeader(statusCode int) {}

func SendAck(w http.ResponseWriter) {
	resp := &model.Response{
		Message: model.Message{
			Ack: model.Ack{
				Status: model.StatusACK,
			},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(data)
	if err != nil {
		http.Error(w, "failed to write response", http.StatusInternalServerError)
		return
	}
}

func nack(w http.ResponseWriter, err *model.Error, status int, ctx context.Context) {
	resp := &model.Response{
		Message: model.Message{
			Ack: model.Ack{
				Status: model.StatusNACK,
			},
			Error: err,
		},
	}
	data, jsonErr := json.Marshal(resp)
	if jsonErr != nil {
		fmt.Printf("Error marshaling response: %v, MessageID: %s", jsonErr, ctx.Value(model.MsgIDKey))
		http.Error(w, fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.MsgIDKey)), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, er := w.Write(data)
	if er != nil {
		fmt.Printf("Error writing response: %v, MessageID: %s", er, ctx.Value(model.MsgIDKey))
		http.Error(w, fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.MsgIDKey)), http.StatusInternalServerError)
		return
	}
}

func internalServerError(ctx context.Context) *model.Error {
	return &model.Error{
		Code:    http.StatusText(http.StatusInternalServerError),
		Message: fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.MsgIDKey)),
	}
}

func SendNack(ctx context.Context, w http.ResponseWriter, err error) {
	var schemaErr *model.SchemaValidationErr
	var signErr *model.SignValidationErr
	var badReqErr *model.BadReqErr
	var notFoundErr *model.NotFoundErr

	switch {
	case errors.As(err, &schemaErr):
		nack(w, schemaErr.BecknError(), http.StatusBadRequest, ctx)
		return
	case errors.As(err, &signErr):
		nack(w, signErr.BecknError(), http.StatusUnauthorized, ctx)
		return
	case errors.As(err, &badReqErr):
		nack(w, badReqErr.BecknError(), http.StatusBadRequest, ctx)
		return
	case errors.As(err, &notFoundErr):
		nack(w, notFoundErr.BecknError(), http.StatusNotFound, ctx)
		return
	default:
		nack(w, internalServerError(ctx), http.StatusInternalServerError, ctx)
		return
	}
}
