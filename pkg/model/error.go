package model

import (
	"fmt"
	"net/http"
	"strings"
)

// Error represents an error response.
type Error struct {
	Code    string `json:"code"`
	Paths   string `json:"paths,omitempty"`
	Message string `json:"message"`
}

// Error implements the error interface for the Error struct.
func (e *Error) Error() string {
	return fmt.Sprintf("Error: Code=%s, Path=%s, Message=%s", e.Code, e.Paths, e.Message)
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

func (e *SchemaValidationErr) BecknError() *Error {
	if len(e.Errors) == 0 {
		return &Error{
			Code:    http.StatusText(http.StatusBadRequest),
			Message: "Schema validation error.",
		}
	}

	// Collect all error paths and messages
	var paths []string
	var messages []string
	for _, err := range e.Errors {
		if err.Paths != "" {
			paths = append(paths, err.Paths)
		}
		messages = append(messages, err.Message)
	}

	return &Error{
		Code:    http.StatusText(http.StatusBadRequest),
		Paths:   strings.Join(paths, ";"),
		Message: strings.Join(messages, "; "),
	}
}

// SignalidationErr represents a collection of schema validation failures.
type SignValidationErr struct {
	error
}

func NewSignValidationErrf(format string, a ...any) *SignValidationErr {
	return &SignValidationErr{fmt.Errorf(format, a...)}
}

func NewSignValidationErr(e error) *SignValidationErr {
	return &SignValidationErr{e}
}

func (e *SignValidationErr) BecknError() *Error {
	return &Error{
		Code:    http.StatusText(http.StatusUnauthorized),
		Message: "Signature Validation Error: " + e.Error(),
	}
}

// SignalidationErr represents a collection of schema validation failures.
type BadReqErr struct {
	error
}

func NewBadReqErr(err error) *BadReqErr {
	return &BadReqErr{err}
}

func NewBadReqErrf(format string, a ...any) *BadReqErr {
	return &BadReqErr{fmt.Errorf(format, a...)}
}

func (e *BadReqErr) BecknError() *Error {
	return &Error{
		Code:    http.StatusText(http.StatusBadRequest),
		Message: "BAD Request: " + e.Error(),
	}
}

// SignalidationErr represents a collection of schema validation failures.
type NotFoundErr struct {
	error
}

func NewNotFoundErr(err error) *NotFoundErr {
	return &NotFoundErr{err}
}

func NewNotFoundErrf(format string, a ...any) *NotFoundErr {
	return &NotFoundErr{fmt.Errorf(format, a...)}
}

func (e *NotFoundErr) BecknError() *Error {
	return &Error{
		Code:    http.StatusText(http.StatusNotFound),
		Message: "Endpoint not found: " + e.Error(),
	}
}
