package model

import (
	"fmt"
	"net/http"
	"strings"
)

// Error represents a standard error response.
type Error struct {
	Code    string        `json:"code"`
	Message string        `json:"message"`
	Details *ErrorDetails `json:"details,omitempty"`
}

// ErrorDetails carries optional structured context for an Error: a JSONPath to
// the failing field, and/or a chained root-cause Error from a downstream layer.
type ErrorDetails struct {
	Path  string `json:"path,omitempty"`
	Cause *Error `json:"cause,omitempty"`
}

// path returns the details path, or "" if Details is unset.
func (e *Error) path() string {
	if e.Details == nil {
		return ""
	}
	return e.Details.Path
}

// This implements the error interface for the Error struct.
func (e *Error) Error() string {
	return fmt.Sprintf("Error: Code=%s, Path=%s, Message=%s", e.Code, e.path(), e.Message)
}

// SchemaValidationErr occurs when schema validation errors are encountered.
type SchemaValidationErr struct {
	Errors []Error
}

// This implements the error interface for SchemaValidationErr.
func (e *SchemaValidationErr) Error() string {
	var errorMessages []string
	for _, err := range e.Errors {
		errorMessages = append(errorMessages, fmt.Sprintf("%s: %s", err.path(), err.Message))
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

	// Collect all error paths, one entry per cause (an entry with no path
	// contributes an empty string), so Details.Path preserves per-cause
	// structure when split on ";" — path segments don't contain literal
	// semicolons in practice. Message is a separate, human-readable
	// concatenation only; it may itself contain either delimiter, so it
	// is not safe to split back into per-cause text.
	var paths []string
	var messages []string
	hasPath := false
	for _, err := range e.Errors {
		p := err.path()
		if p != "" {
			hasPath = true
		}
		paths = append(paths, p)
		messages = append(messages, err.Message)
	}

	var details *ErrorDetails
	if hasPath {
		details = &ErrorDetails{Path: strings.Join(paths, ";")}
	}

	return &Error{
		Code:    http.StatusText(http.StatusBadRequest),
		Details: details,
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

// AckNoCallbackErr is returned by a step when the receiver has authenticated and
// accepted the request but will not send an async callback — for example, no
// matching catalog, inventory unavailable, or provider closed. ONIX maps this to
// HTTP 202 Accepted using the v2 flat response shape. For protocol versions prior
// to 2.0.0 this error falls through to a 500 Internal Server Error.
type AckNoCallbackErr struct {
	// Status is ACK when the request was accepted but no callback will follow,
	// or NACK when the request was outright rejected.
	Status Status
	// Err explains why no callback will be sent. Required by the spec.
	Err *Error
}

// NewAckNoCallbackErr constructs an AckNoCallbackErr.
// Use StatusACK for "accepted but no callback" and StatusNACK for outright rejection.
// Panics if err is nil — the spec requires an error explanation on every AckNoCallback (202) response.
func NewAckNoCallbackErr(status Status, err *Error) *AckNoCallbackErr {
	if err == nil {
		panic("AckNoCallbackErr: Err is required")
	}
	return &AckNoCallbackErr{Status: status, Err: err}
}

// Error implements the error interface.
func (e *AckNoCallbackErr) Error() string {
	return fmt.Sprintf("AckNoCallback(status=%s): %s", e.Status, e.Err.Error())
}

// BecknError returns the wrapped *Error payload.
func (e *AckNoCallbackErr) BecknError() *Error {
	return e.Err
}
