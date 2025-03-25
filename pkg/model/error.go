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

// BecknError converts SchemaValidationErr into a Beckn-compliant Error response.
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

// SignValidationErr represents an error that occurs during signature validation.
type SignValidationErr struct {
	error
}

// NewSignValidationErrf creates a new SignValidationErr with a formatted message.
func NewSignValidationErrf(format string, a ...any) *SignValidationErr {
	return &SignValidationErr{fmt.Errorf(format, a...)}
}

// NewSignValidationErr creates a new SignValidationErr from an existing error.
func NewSignValidationErr(e error) *SignValidationErr {
	return &SignValidationErr{e}
}

// BecknError converts SignValidationErr into a Beckn-compliant Error response.
func (e *SignValidationErr) BecknError() *Error {
	return &Error{
		Code:    http.StatusText(http.StatusUnauthorized),
		Message: "Signature Validation Error: " + e.Error(),
	}
}

// BadReqErr represents an error related to a bad request.
type BadReqErr struct {
	error
}

// NewBadReqErr creates a new BadReqErr from an existing error.
func NewBadReqErr(err error) *BadReqErr {
	return &BadReqErr{err}
}

// NewBadReqErrf creates a new BadReqErr with a formatted message.
func NewBadReqErrf(format string, a ...any) *BadReqErr {
	return &BadReqErr{fmt.Errorf(format, a...)}
}

// BecknError converts BadReqErr into a Beckn-compliant Error response.
func (e *BadReqErr) BecknError() *Error {
	return &Error{
		Code:    http.StatusText(http.StatusBadRequest),
		Message: "BAD Request: " + e.Error(),
	}
}

// NotFoundErr represents an error for a missing resource or endpoint.
type NotFoundErr struct {
	error
}

// NewNotFoundErr creates a new NotFoundErr from an existing error.
func NewNotFoundErr(err error) *NotFoundErr {
	return &NotFoundErr{err}
}

// NewNotFoundErrf creates a new NotFoundErr with a formatted message.
func NewNotFoundErrf(format string, a ...any) *NotFoundErr {
	return &NotFoundErr{fmt.Errorf(format, a...)}
}

// BecknError converts NotFoundErr into a Beckn-compliant Error response.
func (e *NotFoundErr) BecknError() *Error {
	return &Error{
		Code:    http.StatusText(http.StatusNotFound),
		Message: "Endpoint not found: " + e.Error(),
	}
}
