package response

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"
)

// ErrorType represents different types of errors in the Beckn protocol.
type ErrorType string

const (
	// SchemaValidationErrorType represents an error due to schema validation failure.
	SchemaValidationErrorType ErrorType = "SCHEMA_VALIDATION_ERROR"

	// InvalidRequestErrorType represents an error due to an invalid request.
	InvalidRequestErrorType ErrorType = "INVALID_REQUEST"
)

// BecknRequest represents a generic Beckn request with an optional context.
type BecknRequest struct {
	Context map[string]interface{} `json:"context,omitempty"`
}

// SendAck sends an acknowledgment (ACK) response indicating a successful request processing.
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
	if _, err := w.Write(data); err != nil {
		log.Error(context.Background(), err, "failed to write ack response")
	}

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
	if _, err := w.Write(data); err != nil {
		log.Error(context.Background(), err, "failed to write nack response")
	}

}

func internalServerError(ctx context.Context) *model.Error {
	return &model.Error{
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
	case errors.As(err, &schemaErr): // Custom application error
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

// BecknError generates a standardized Beckn error response.
func BecknError(ctx context.Context, err error, status int) *model.Error {
	msg := err.Error()
	msgID := ctx.Value(model.MsgIDKey)
	if status == http.StatusInternalServerError {

		msg = "Internal server error"
	}
	return &model.Error{
		Message: fmt.Sprintf("%s. MessageID: %s.", msg, msgID),
		Code:    strconv.Itoa(status),
	}
}
