package response

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type ErrorType string

const (
	SchemaValidationErrorType ErrorType = "SCHEMA_VALIDATION_ERROR"
	InvalidRequestErrorType   ErrorType = "INVALID_REQUEST"
)

type BecknRequest struct {
	Context map[string]interface{} `json:"context,omitempty"`
}
type Error struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Paths   string `json:"paths,omitempty"`
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

type Message struct {
	Ack struct {
		Status string `json:"status,omitempty"`
	} `json:"ack,omitempty"`
	Error *Error `json:"error,omitempty"`
}
type BecknResponse struct {
	Context map[string]interface{} `json:"context,omitempty"`
	Message Message                `json:"message,omitempty"`
}
type ClientFailureBecknResponse struct {
	Context map[string]interface{} `json:"context,omitempty"`
	Error   *Error                 `json:"error,omitempty"`
}

var errorMap = map[ErrorType]Error{
	SchemaValidationErrorType: {
		Code:    "400",
		Message: "Schema validation failed",
	},
	InvalidRequestErrorType: {
		Code:    "401",
		Message: "Invalid request format",
	},
}
var DefaultError = Error{
	Code:    "500",
	Message: "Internal server error",
}

func Nack(ctx context.Context, tp ErrorType, paths string, body []byte) ([]byte, error) {
	var req BecknRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to parse request: %w", err)
	}
	errorObj, ok := errorMap[tp]
	if paths != "" {
		errorObj.Paths = paths
	}
	var response BecknResponse
	if !ok {
		response = BecknResponse{
			Context: req.Context,
			Message: Message{
				Ack: struct {
					Status string `json:"status,omitempty"`
				}{
					Status: "NACK",
				},
				Error: &DefaultError,
			},
		}
	} else {
		response = BecknResponse{
			Context: req.Context,
			Message: Message{
				Ack: struct {
					Status string `json:"status,omitempty"`
				}{
					Status: "NACK",
				},
				Error: &errorObj,
			},
		}
	}
	return json.Marshal(response)
}

// Ack processes the incoming Beckn request, unmarshals the JSON body into a BecknRequest struct,
// and returns a JSON-encoded acknowledgment response with a status of "ACK".
// If the request body cannot be parsed, it returns an error.
func Ack(ctx context.Context, body []byte) ([]byte, error) {
	var req BecknRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to parse request: %w", err)
	}
	response := BecknResponse{
		Context: req.Context,
		Message: Message{
			Ack: struct {
				Status string `json:"status,omitempty"`
			}{
				Status: "ACK",
			},
		},
	}
	return json.Marshal(response)
}

// HandleClientFailure processes a client failure scenario by unmarshaling the provided
// request body, determining the appropriate error response based on the given ErrorType,
// and returning the serialized response. If the ErrorType is not found in the error map,
// a default error is used.
func HandleClientFailure(ctx context.Context, tp ErrorType, body []byte) ([]byte, error) {
	var req BecknRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to parse request: %w", err)
	}
	errorObj, ok := errorMap[tp]
	var response ClientFailureBecknResponse
	if !ok {
		response = ClientFailureBecknResponse{
			Context: req.Context,
			Error:   &DefaultError,
		}
	} else {
		response = ClientFailureBecknResponse{
			Context: req.Context,
			Error:   &errorObj,
		}
	}
	return json.Marshal(response)
}
