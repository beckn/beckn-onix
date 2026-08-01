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
	// cause is the underlying error this Error was derived from, if any. It is
	// unexported (never appears on the wire) and exists purely so
	// errors.Is/errors.As can keep reaching the original cause through Unwrap,
	// instead of a caller having to thread a separate cause value alongside
	// the *Error return.
	cause error
}

// NewCodedError constructs an Error carrying an explicit ErrorCode value and
// message, for callers that already know a specific code to report (e.g. a
// plugin classifying one of its own failure modes onto the Beckn v2.0.0
// ErrorCode taxonomy).
//
// The returned *Error is a plain value, not a step error: nackBecknError
// (core/module/handler/responsestep.go) only recognizes SchemaValidationErr,
// CodedErr, AckNoCallbackErr, and any type implementing BecknErrorer. Callers
// must wrap the result in one of those types (or implement BecknErrorer)
// before returning it from a Step — returning it bare falls through to a
// generic 500 Internal Server Error instead of the intended NACK code.
//
// NewCodedErr builds the step error itself, carrying a cause and an HTTP
// status alongside the code.
func NewCodedError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// NewCodedErrorWithCause is like NewCodedError but also records the JSONPath
// to the failing field (path, may be "") and the underlying cause, so callers
// don't have to flatten both into the message string to preserve them —
// Details.Path carries the path and Unwrap() keeps the cause reachable via
// errors.Is/errors.As.
func NewCodedErrorWithCause(code, message, path string, cause error) *Error {
	e := &Error{Code: code, Message: message, cause: cause}
	if path != "" {
		e.Details = &ErrorDetails{Path: path}
	}
	return e
}

// Unwrap exposes the wrapped cause (if any) so errors.Is/errors.As can reach
// it in addition to matching *Error itself.
func (e *Error) Unwrap() error {
	return e.cause
}

// BecknErrorer is implemented by any error type that can produce its own
// *Error NACK representation. nackBecknError (core/module/handler/responsestep.go)
// dispatches on a short list of concrete types first (SchemaValidationErr,
// CodedErr, AckNoCallbackErr) for their HTTP status codes, then falls back to
// this interface for any other type — so a new error type can be wired into
// NACK dispatch by implementing BecknError() *Error alone, without core
// importing the plugin package that defines it. The fallback always answers
// 400; a type needing another status should return a *CodedErr instead.
type BecknErrorer interface {
	error
	BecknError() *Error
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

// CodedErr wraps one cause with an optional taxonomy code and the HTTP status
// to NACK it with. It covers what BadReqErr, SignValidationErr and NotFoundErr
// used to carry separately: those stored the same fields and differed only in
// the status nackBecknError (core/module/handler/responsestep.go) picked for
// each by type switch, so the status is now set at construction instead.
//
// Use NewCodedErr, or NewBadReqErr, NewSignValidationErr and NewNotFoundErr
// for the 400, 401 and 404 cases and their message prefixes. Status, prefix
// and default code are unexported: a usable value comes from a constructor.
//
// SchemaValidationErr and AckNoCallbackErr stay separate. They differ in
// shape, not just status.
type CodedErr struct {
	// Code is the taxonomy value for this failure's specific cause, or ""
	// if unclassified — BecknError() then reports defaultCode.
	Code string

	// httpStatus is the status nackBecknError responds with.
	httpStatus int
	// prefix is prepended to the wrapped error's text by BecknError().
	prefix string
	// defaultCode is reported when Code is empty.
	defaultCode string

	error
}

// defaultUnclassifiedCode is reported when a NewCodedErr caller passes no
// code. Those callers are expected to know their code, so this only keeps an
// empty one off the wire.
const defaultUnclassifiedCode = "NET_INTERNAL_ERROR"

// NewCodedErr creates a CodedErr with an explicit HTTP status and taxonomy
// code, for callers of any failure flavor. Plugins that classify several
// kinds of failure — vcvalidator, for one — need only this constructor.
//
// The status is explicit rather than derived from the code's family prefix,
// which does not determine it: NET_* alone spans 404, 500, 502 and 503.
func NewCodedErr(httpStatus int, code string, err error) *CodedErr {
	return &CodedErr{Code: code, httpStatus: httpStatus, defaultCode: defaultUnclassifiedCode, error: err}
}

// HTTPStatus returns the status to NACK with, defaulting to 400 for a value
// built without a constructor — the status core uses for any other
// BecknErrorer.
func (e *CodedErr) HTTPStatus() int {
	if e.httpStatus == 0 {
		return http.StatusBadRequest
	}
	return e.httpStatus
}

// Unwrap exposes the wrapped cause so errors.Is/errors.As can reach it (e.g. a
// plugin-defined sentinel error) in addition to matching *CodedErr itself.
func (e *CodedErr) Unwrap() error {
	return e.error
}

// resolveCode returns Code if non-empty, else the constructor's default.
func (e *CodedErr) resolveCode() string {
	if e.Code != "" {
		return e.Code
	}
	return e.defaultCode
}

// BecknError builds the *Error NACK payload from the resolved code and the
// constructor's prefix prepended to the wrapped error's text.
func (e *CodedErr) BecknError() *Error {
	return &Error{
		Code:    e.resolveCode(),
		Message: e.prefix + e.Error(),
	}
}

// defaultSignValidationCode is used when a signature-validation failure
// carries no more specific classification — the closest generic bucket in the
// AUT_* taxonomy.
const defaultSignValidationCode = "AUT_SIGNATURE_INVALID"

// NewSignValidationErr creates a 401 CodedErr for a request whose
// authenticity could not be established. Pass code "" to leave the failure
// unclassified, reporting defaultSignValidationCode.
//
// The "Signature Validation Error: " message prefix was accurate for the
// original sole caller (signvalidator.go, exclusively real signature
// failures). vcvalidator (see #870/#884) also uses it for non-signature causes
// — expiry, revocation, DID-resolution failures, issuer mismatch — so the
// human-readable message can now read e.g. "Signature Validation Error:
// CREDENTIAL_EXPIRED: ...". The structured Code field is correct either way;
// only this message text is misleading for those causes. Deliberately left
// as-is: fixing it is a cross-cutting change affecting every caller, deferred
// rather than folded into #884's scope.
func NewSignValidationErr(code string, err error) *CodedErr {
	return &CodedErr{
		Code:        code,
		httpStatus:  http.StatusUnauthorized,
		prefix:      "Signature Validation Error: ",
		defaultCode: defaultSignValidationCode,
		error:       err,
	}
}

// defaultBadReqCode is used when a bad request carries no more specific
// classification — the closest generic bucket in the SCH_* taxonomy. Reused
// across many callers rather than a dedicated bucket, since this fallback is
// rarely hit once a caller passes an explicit code.
const defaultBadReqCode = "SCH_INVALID_FORMAT"

// NewBadReqErr creates a 400 CodedErr. Pass code "" to leave the failure
// unclassified, reporting defaultBadReqCode, or a specific taxonomy code when
// the caller knows one (e.g. a policy checker classifying a denial onto the
// Beckn v2.0.0 POL_* codes).
func NewBadReqErr(code string, err error) *CodedErr {
	return &CodedErr{
		Code:        code,
		httpStatus:  http.StatusBadRequest,
		prefix:      "BAD Request: ",
		defaultCode: defaultBadReqCode,
		error:       err,
	}
}

// defaultNotFoundCode is used when a not-found failure carries no more
// specific classification — the closest generic bucket in the NET_* taxonomy.
const defaultNotFoundCode = "NET_ENTITY_NOT_FOUND"

// NewNotFoundErr creates a 404 CodedErr for a requested endpoint or entity
// that does not exist. Pass code "" to leave the failure unclassified,
// reporting defaultNotFoundCode.
func NewNotFoundErr(code string, err error) *CodedErr {
	return &CodedErr{
		Code:        code,
		httpStatus:  http.StatusNotFound,
		prefix:      "Endpoint not found: ",
		defaultCode: defaultNotFoundCode,
		error:       err,
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
