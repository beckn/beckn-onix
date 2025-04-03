package model

import (
	"fmt"
	"net/http"
	"strings"
)

// Error represents a standard error response.
type Error struct {
	Code    string `json:"code"`
	Paths   string `json:"paths,omitempty"`
	Message string `json:"message"`
}

// This implements the error interface for the Error struct.
func (e *Error) Error() string {
	return fmt.Sprintf("Error: Code=%s, Path=%s, Message=%s", e.Code, e.Paths, e.Message)
}

// SchemaValidationErr occurs when schema validation errors are encountered.
type SchemaValidationErr struct {
	Errors []Error
}

// This implements the error interface for SchemaValidationErr.
func (e *SchemaValidationErr) Error() string {
	var errorMessages []string
	for _, err := range e.Errors {
		errorMessages = append(errorMessages, fmt.Sprintf("%s: %s", err.Paths, err.Message))
	}
	return strings.Join(errorMessages, "; ")
}

// BecknError converts the SchemaValidationErr to an instance of Error.
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

// SignValidationErr occurs when signature validation fails.
type SignValidationErr struct {
	error
}

// NewSignValidationErr creates a new instance of SignValidationErr from an error.
func NewSignValidationErr(e error) *SignValidationErr {
	return &SignValidationErr{e}
}

// BecknError converts the SignValidationErr to an instance of Error.
func (e *SignValidationErr) BecknError() *Error {
	return &Error{
		Code:    http.StatusText(http.StatusUnauthorized),
		Message: "Signature Validation Error: " + e.Error(),
	}
}

// BadReqErr occurs when a bad request is encountered.
type BadReqErr struct {
	error
}

// NewBadReqErr creates a new instance of BadReqErr from an error.
func NewBadReqErr(err error) *BadReqErr {
	return &BadReqErr{err}
}

// BecknError converts the BadReqErr to an instance of Error.
func (e *BadReqErr) BecknError() *Error {
	return &Error{
		Code:    http.StatusText(http.StatusBadRequest),
		Message: "BAD Request: " + e.Error(),
	}
}

// NotFoundErr occurs when a requested endpoint is not found.
type NotFoundErr struct {
	error
}

// NewNotFoundErr creates a new instance of NotFoundErr from an error.
func NewNotFoundErr(err error) *NotFoundErr {
	return &NotFoundErr{err}
}

// BecknError converts the NotFoundErr to an instance of Error.
func (e *NotFoundErr) BecknError() *Error {
	return &Error{
		Code:    http.StatusText(http.StatusNotFound),
		Message: "Endpoint not found: " + e.Error(),
	}
}
