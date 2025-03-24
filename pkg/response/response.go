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

const (
	SchemaValidationErrorType ErrorType = "SCHEMA_VALIDATION_ERROR"
	InvalidRequestErrorType   ErrorType = "INVALID_REQUEST"
)

// type BecknRequest struct {
// 	Context map[string]interface{} `json:"context,omitempty"`
// }

func SendAck(w http.ResponseWriter) {
	// Create the response object
	resp := &model.Response{
		Message: model.Message{
			Ack: model.Ack{
				Status: model.StatusACK,
			},
		},
	}

	// Marshal to JSON
	data, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	// Set headers and write response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// nack sends a negative acknowledgment (NACK) response with an error message.
func nack(w http.ResponseWriter, err *model.Error, status int) {
	// Create the NACK response object
	resp := &model.Response{
		Message: model.Message{
			Ack: model.Ack{
				Status: model.StatusNACK,
			},
			Error: err,
		},
	}

	// Marshal the response to JSON
	data, jsonErr := json.Marshal(resp)
	if jsonErr != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	// Set headers and write response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status) // Assuming NACK means a bad request
	w.Write(data)
}

func internalServerError(ctx context.Context) *model.Error {
	return &model.Error{
		Code:    http.StatusText(http.StatusInternalServerError), 
		Message: fmt.Sprintf("Internal server error, MessageID: %s", ctx.Value(model.MsgIDKey)),
	}
}

// SendNack sends a negative acknowledgment (NACK) response with an error message.
func SendNack(ctx context.Context, w http.ResponseWriter, err error) {
	var schemaErr *model.SchemaValidationErr
	var signErr *model.SignValidationErr
	var badReqErr *model.BadReqErr
	var notFoundErr *model.NotFoundErr

	switch {
	case errors.As(err, &schemaErr):
		nack(w, schemaErr.BecknError(), http.StatusBadRequest)
		return
	case errors.As(err, &signErr):
		nack(w, signErr.BecknError(), http.StatusUnauthorized)
		return
	case errors.As(err, &badReqErr):
		nack(w, badReqErr.BecknError(), http.StatusBadRequest)
		return
	case errors.As(err, &notFoundErr):
		nack(w, notFoundErr.BecknError(), http.StatusNotFound)
		return
	default:
		nack(w, internalServerError(ctx), http.StatusInternalServerError)
		return
	}
}
