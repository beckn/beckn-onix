package response

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"strings"

	"github.com/beckn/beckn-onix/pkg/model"
)

// Error represents a standardized error response used across the system.
type Error struct {
	// Code is a short, machine-readable error code.
	Code string `json:"code,omitempty"`

	// Message provides a human-readable description of the error.
	Message string `json:"message,omitempty"`

	// Paths indicates the specific field(s) or endpoint(s) related to the error.
	Paths string `json:"paths,omitempty"`
}

// SchemaValidationErr represents a collection of schema validation failures.
type SchemaValidationErr struct {
	Errors []Error
}

// Error implements the error interface for SchemaValidationErr.
func (e *SchemaValidationErr) Error() string {
	var errorMessages []string
	for _, err := range e.Errors {
		errorMessages = append(errorMessages, fmt.Sprintf("%s: %s", err.Paths, err.Message))
	}
	return strings.Join(errorMessages, "; ")
}

// Message represents a standard message structure with acknowledgment and error information.
type Message struct {
	// Ack contains the acknowledgment status of the response.
	Ack struct {
		Status string `json:"status,omitempty"`
	} `json:"ack,omitempty"`

	// Error holds error details if any occurred during processing.
	Error *Error `json:"error,omitempty"`
}

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
