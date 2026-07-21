package model

import (
	"fmt"
	"strings"
)

// Error represents a standard error response.
type Error struct {
	Code    string        `json:"code"`
	Message string        `json:"message"`
	Details *ErrorDetails `json:"details,omitempty"`
}

// NewCodedError constructs an Error carrying an explicit ErrorCode value and
// message, for callers that already know a specific code to report (e.g. a
// plugin classifying one of its own failure modes onto the Beckn v2.0.0
// ErrorCode taxonomy).
//
// The returned *Error is a plain value, not a step error: nackBecknError
// (core/module/handler/responsestep.go) only recognizes SchemaValidationErr,
// SignValidationErr, BadReqErr, NotFoundErr, and AckNoCallbackErr. Callers
// must wrap the result in one of those types before returning it from a
// Step — returning it bare falls through to a generic 500 Internal Server
// Error instead of the intended NACK code.
func NewCodedError(code, message string) *Error {
	return &Error{Code: code, Message: message}
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

// defaultSchemaValidationCode is used when a SchemaValidationErr (or one of
// its underlying Errors) carries no more specific classification — the
// closest generic bucket in the SCH_* taxonomy. Shared by both schemavalidator
// (legacy, retiring) and schemav2validator, since both construct this type.
const defaultSchemaValidationCode = "SCH_INVALID_FORMAT"

// BecknError converts the SchemaValidationErr to an instance of Error.
func (e *SchemaValidationErr) BecknError() *Error {
	if len(e.Errors) == 0 {
		return &Error{
			Code:    defaultSchemaValidationCode,
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
		Code:    FirstNonEmptyCode(e.Errors, defaultSchemaValidationCode),
		Details: details,
		Message: strings.Join(messages, "; "),
	}
}

// FirstNonEmptyCode returns the first non-empty Code among errs, in order, or
// defaultCode if none is set. Used when multiple causes must be reduced to
// one representative Code for the wire — the other causes' text is still
// carried in full elsewhere (e.g. a joined Message), only their Code is
// dropped, since a single Error can only carry one code.
func FirstNonEmptyCode(errs []Error, defaultCode string) string {
	for _, e := range errs {
		if e.Code != "" {
			return e.Code
		}
	}
	return defaultCode
}

// codedErr is the common shape shared by wrapper error types that carry one
// opaque cause plus an optional explicit taxonomy code, falling back to a
// type-specific default when Code is empty. Embed it in a named type (e.g.
// SignValidationErr, BadReqErr) to get Code storage and Unwrap() without
// duplicating them — the embedding type still defines its own constructors
// and BecknError(), since message-prefix formatting and the default code
// genuinely differ per type.
type codedErr struct {
	// Code is the taxonomy value for this failure's specific cause, or ""
	// if unclassified — the embedding type's BecknError() should apply its
	// own default via resolveCode in that case.
	Code string
	error
}

// Unwrap exposes the wrapped cause so errors.Is/errors.As can reach it (e.g. a
// plugin-defined sentinel error) in addition to matching the embedding type.
func (e *codedErr) Unwrap() error {
	return e.error
}

// resolveCode returns Code if non-empty, else defaultCode. Called by each
// embedding type's BecknError() to apply its own default fallback.
func (e *codedErr) resolveCode(defaultCode string) string {
	if e.Code != "" {
		return e.Code
	}
	return defaultCode
}

// defaultSignValidationCode is used when a SignValidationErr carries no more
// specific classification — the closest generic bucket in the AUT_* taxonomy.
const defaultSignValidationCode = "AUT_SIGNATURE_INVALID"

// SignValidationErr occurs when signature validation fails.
type SignValidationErr struct {
	codedErr
}

// NewSignValidationErr creates a new instance of SignValidationErr from an
// error. Code is left unset, so BecknError() falls back to the generic
// AUT_SIGNATURE_INVALID bucket — mirrors NewBadReqErr's lazy-default
// convention. Use NewCodedSignValidationErr when the caller knows a more
// specific AUT_* cause.
func NewSignValidationErr(e error) *SignValidationErr {
	return &SignValidationErr{codedErr{error: e}}
}

// NewCodedSignValidationErr creates a SignValidationErr classified with an
// explicit AUT_* code, for callers that already know the specific cause.
func NewCodedSignValidationErr(code string, e error) *SignValidationErr {
	return &SignValidationErr{codedErr{Code: code, error: e}}
}

// BecknError converts the SignValidationErr to an instance of Error.
//
// The "Signature Validation Error: " message prefix was accurate for this
// type's original sole caller (signvalidator.go, exclusively real signature
// failures). vcvalidator (see #870/#884) also constructs SignValidationErr
// for non-signature causes — expiry, revocation, DID-resolution failures,
// issuer mismatch — via NewCodedSignValidationErr, so the human-readable
// message can now read e.g. "Signature Validation Error: CREDENTIAL_EXPIRED:
// ...". The structured Code field is correct either way; only this message
// text is misleading for those causes. Deliberately left as-is: fixing it is
// a cross-cutting change affecting every caller of this type, deferred
// rather than folded into #884's scope.
func (e *SignValidationErr) BecknError() *Error {
	return &Error{
		Code:    e.resolveCode(defaultSignValidationCode),
		Message: "Signature Validation Error: " + e.Error(),
	}
}

// BadReqErr occurs when a bad request is encountered.
type BadReqErr struct {
	codedErr
}

// defaultBadReqCode is used when a BadReqErr carries no more specific
// classification — the closest generic bucket in the SCH_* taxonomy. Reused
// across many callers rather than a dedicated bucket, since this fallback is
// rarely hit once a caller adopts NewCodedBadReqErr.
const defaultBadReqCode = "SCH_INVALID_FORMAT"

// NewBadReqErr creates a new instance of BadReqErr from an error. Code is left
// unset, so BecknError() falls back to defaultBadReqCode — the many existing
// callers of this constructor across the codebase keep that behavior
// unchanged. Use NewCodedBadReqErr when the caller knows a more specific
// taxonomy code.
func NewBadReqErr(err error) *BadReqErr {
	return &BadReqErr{codedErr{error: err}}
}

// NewCodedBadReqErr creates a BadReqErr classified with an explicit taxonomy
// code, for callers that already know the specific cause (e.g. a policy
// checker classifying a denial onto the Beckn v2.0.0 POL_* codes).
func NewCodedBadReqErr(code string, err error) *BadReqErr {
	return &BadReqErr{codedErr{Code: code, error: err}}
}

// BecknError converts the BadReqErr to an instance of Error.
func (e *BadReqErr) BecknError() *Error {
	return &Error{
		Code:    e.resolveCode(defaultBadReqCode),
		Message: "BAD Request: " + e.Error(),
	}
}

// defaultNotFoundCode is used when a NotFoundErr carries no more specific
// classification — the closest generic bucket in the NET_* taxonomy.
const defaultNotFoundCode = "NET_ENTITY_NOT_FOUND"

// NotFoundErr occurs when a requested endpoint is not found.
type NotFoundErr struct {
	codedErr
}

// NewNotFoundErr creates a new instance of NotFoundErr from an error. Code is
// left unset, so BecknError() falls back to defaultNotFoundCode. Use
// NewCodedNotFoundErr when the caller knows a more specific taxonomy code.
func NewNotFoundErr(err error) *NotFoundErr {
	return &NotFoundErr{codedErr{error: err}}
}

// NewCodedNotFoundErr creates a NotFoundErr classified with an explicit
// taxonomy code, for callers that already know the specific cause.
func NewCodedNotFoundErr(code string, err error) *NotFoundErr {
	return &NotFoundErr{codedErr{Code: code, error: err}}
}

// BecknError converts the NotFoundErr to an instance of Error.
func (e *NotFoundErr) BecknError() *Error {
	return &Error{
		Code:    e.resolveCode(defaultNotFoundCode),
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
