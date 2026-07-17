package model

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
)

// NewSignValidationErrf creates a new SignValidationErr with a formatted error message.
func NewSignValidationErrf(format string, a ...any) *SignValidationErr {
	return &SignValidationErr{fmt.Errorf(format, a...)}
}

// NewNotFoundErrf creates a new NotFoundErr with a formatted error message.
func NewNotFoundErrf(format string, a ...any) *NotFoundErr {
	return &NotFoundErr{fmt.Errorf(format, a...)}
}

// NewBadReqErrf creates a new BadReqErr with a formatted error message.
func NewBadReqErrf(format string, a ...any) *BadReqErr {
	return &BadReqErr{fmt.Errorf(format, a...)}
}

func TestError_Error(t *testing.T) {
	err := &Error{
		Code:    "404",
		Message: "User not found",
		Details: &ErrorDetails{Path: "/api/v1/user"},
	}

	expected := "Error: Code=404, Path=/api/v1/user, Message=User not found"
	actual := err.Error()

	if actual != expected {
		t.Errorf("err.Error() = %s, want %s",
			actual, expected)
	}

}

func TestSchemaValidationErr_Error(t *testing.T) {
	schemaErr := &SchemaValidationErr{
		Errors: []Error{
			{Details: &ErrorDetails{Path: "/user"}, Message: "Field required"},
			{Details: &ErrorDetails{Path: "/email"}, Message: "Invalid format"},
		},
	}

	expected := "/user: Field required; /email: Invalid format"
	actual := schemaErr.Error()

	if actual != expected {
		t.Errorf("err.Error() = %s, want %s",
			actual, expected)
	}
}

func TestSchemaValidationErr_BecknError(t *testing.T) {
	schemaErr := &SchemaValidationErr{
		Errors: []Error{
			{Details: &ErrorDetails{Path: "/user"}, Message: "Field required"},
		},
	}

	beErr := schemaErr.BecknError()
	expected := "Bad Request"
	if beErr.Code != expected {
		t.Errorf("err.Error() = %s, want %s",
			beErr.Code, expected)
	}
	if beErr.Details == nil || beErr.Details.Path != "/user" {
		t.Errorf("beErr.Details = %+v, want Path=/user", beErr.Details)
	}
}

func TestSchemaValidationErr_BecknError_NoPaths(t *testing.T) {
	schemaErr := &SchemaValidationErr{
		Errors: []Error{
			{Message: "generic error one"},
			{Message: "generic error two"},
		},
	}

	beErr := schemaErr.BecknError()
	if beErr.Details != nil {
		t.Errorf("beErr.Details = %+v, want nil when no entry has a path", beErr.Details)
	}
	expectedMsg := "generic error one; generic error two"
	if beErr.Message != expectedMsg {
		t.Errorf("beErr.Message = %s, want %s", beErr.Message, expectedMsg)
	}
}

func TestSchemaValidationErr_BecknError_MixedPaths(t *testing.T) {
	schemaErr := &SchemaValidationErr{
		Errors: []Error{
			{Details: &ErrorDetails{Path: "/a"}, Message: "m1"},
			{Message: "m2"},
			{Details: &ErrorDetails{Path: "/c"}, Message: "m3"},
		},
	}

	beErr := schemaErr.BecknError()
	if beErr.Details == nil {
		t.Fatal("beErr.Details = nil, want non-nil since at least one entry has a path")
	}
	expectedPath := "/a;;/c"
	if beErr.Details.Path != expectedPath {
		t.Errorf("beErr.Details.Path = %s, want %s (positionally aligned with Message)", beErr.Details.Path, expectedPath)
	}
	expectedMsg := "m1; m2; m3"
	if beErr.Message != expectedMsg {
		t.Errorf("beErr.Message = %s, want %s", beErr.Message, expectedMsg)
	}
}

func TestSignValidationErr_BecknError(t *testing.T) {
	signErr := NewSignValidationErr(errors.New("signature failed"))
	beErr := signErr.BecknError()

	expectedMsg := "Signature Validation Error: signature failed"
	if beErr.Message != expectedMsg {
		t.Errorf("err.Error() = %s, want %s",
			beErr.Message, expectedMsg)
	}

}

func TestNewSignValidationErrf(t *testing.T) {
	signErr := NewSignValidationErrf("error %s", "signature failed")
	expected := "error signature failed"
	if signErr.Error() != expected {
		t.Errorf("err.Error() = %s, want %s",
			signErr.Error(), expected)
	}
}

func TestNewSignValidationErr(t *testing.T) {
	err := errors.New("signature error")
	signErr := NewSignValidationErr(err)

	if signErr.Error() != err.Error() {
		t.Errorf("err.Error() = %s, want %s", err.Error(),
			signErr.Error())
	}
}

func TestBadReqErr_BecknError(t *testing.T) {
	badReqErr := NewBadReqErr(errors.New("invalid input"))
	beErr := badReqErr.BecknError()

	expectedMsg := "BAD Request: invalid input"
	if beErr.Message != expectedMsg {
		t.Errorf("err.Error() = %s, want %s",
			beErr.Message, expectedMsg)
	}
}

func TestNewBadReqErrf(t *testing.T) {
	badReqErr := NewBadReqErrf("invalid field %s", "name")
	expected := "invalid field name"
	if badReqErr.Error() != expected {
		t.Errorf("err.Error() = %s, want %s",
			badReqErr, expected)
	}
}

func TestNewBadReqErr(t *testing.T) {
	err := errors.New("bad request")
	badReqErr := NewBadReqErr(err)

	if badReqErr.Error() != err.Error() {
		t.Errorf("err.Error() = %s, want %s",
			badReqErr.Error(), err.Error())
	}

}

func TestNotFoundErr_BecknError(t *testing.T) {
	notFoundErr := NewNotFoundErr(errors.New("resource not found"))
	beErr := notFoundErr.BecknError()

	expectedMsg := "Endpoint not found: resource not found"
	if beErr.Message != expectedMsg {
		t.Errorf("err.Error() = %s, want %s",
			beErr.Message, expectedMsg)
	}
}

func TestNewNotFoundErrf(t *testing.T) {
	notFoundErr := NewNotFoundErrf("resource %s not found", "user")
	expected := "resource user not found"
	if notFoundErr.Error() != expected {
		t.Errorf("err.Error() = %s, want %s",
			notFoundErr.Error(), expected)
	}
}

func TestNewNotFoundErr(t *testing.T) {
	err := errors.New("not found")
	notFoundErr := NewNotFoundErr(err)

	if notFoundErr.Error() != err.Error() {
		t.Errorf("err.Error() = %s, want %s",
			notFoundErr.Error(), err.Error())
	}
}

func TestRole_UnmarshalYAML_ValidRole(t *testing.T) {
	var role Role
	yamlData := []byte("bap")

	err := yaml.Unmarshal(yamlData, &role)
	assert.NoError(t, err) //TODO: should replace assert here
	assert.Equal(t, RoleBAP, role)
}

func TestRole_UnmarshalYAML_InvalidRole(t *testing.T) {
	var role Role
	yamlData := []byte("invalid")

	err := yaml.Unmarshal(yamlData, &role)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Role")
}

func TestSchemaValidationErr_BecknError_NoErrors(t *testing.T) {
	schemaValidationErr := &SchemaValidationErr{Errors: nil}
	beErr := schemaValidationErr.BecknError()

	expectedMsg := "Schema validation error."
	expectedCode := http.StatusText(http.StatusBadRequest)

	if beErr.Message != expectedMsg {
		t.Errorf("beErr.Message = %s, want %s", beErr.Message, expectedMsg)
	}
	if beErr.Code != expectedCode {
		t.Errorf("beErr.Code = %s, want %s", beErr.Code, expectedCode)
	}
}

func TestNewAckNoCallbackErr(t *testing.T) {
	tests := []struct {
		name           string
		status         Status
		becknErr       *Error
		wantErrContain string
	}{
		{
			name:           "ACK status",
			status:         StatusACK,
			becknErr:       &Error{Code: "NO_CATALOG", Message: "no matching catalog"},
			wantErrContain: "AckNoCallback(status=ACK)",
		},
		{
			name:           "NACK status",
			status:         StatusNACK,
			becknErr:       &Error{Code: "PROVIDER_CLOSED", Message: "provider closed"},
			wantErrContain: "AckNoCallback(status=NACK)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewAckNoCallbackErr(tt.status, tt.becknErr)

			if e.Status != tt.status {
				t.Errorf("Status = %s, want %s", e.Status, tt.status)
			}
			if e.BecknError() != tt.becknErr {
				t.Error("BecknError() did not return the wrapped error")
			}
			if got := e.Error(); !strings.Contains(got, tt.wantErrContain) {
				t.Errorf("Error() = %q, want it to contain %q", got, tt.wantErrContain)
			}
		})
	}
}

func TestNewAckNoCallbackErr_NilErr_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil Err, got none")
		}
	}()
	NewAckNoCallbackErr(StatusACK, nil)
}

func TestParseContextKey_ValidKeys(t *testing.T) {
	tests := []struct {
		input    string
		expected ContextKey
	}{
		{"transaction_id", ContextKeyTxnID},
		{"message_id", ContextKeyMsgID},
		{"subscriber_id", ContextKeySubscriberID},
		{"module_id", ContextKeyModuleID},
		{"parent_id", ContextKeyParentID},
	}

	for _, tt := range tests {
		key, err := ParseContextKey(tt.input)
		if err != nil {
			t.Errorf("unexpected error for input %s: %v", tt.input, err)
		}
		if key != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, key)
		}
	}
}

func TestParseContextKey_InvalidKey(t *testing.T) {
	_, err := ParseContextKey("invalid_key")
	if err == nil {
		t.Error("expected error for invalid context key, got nil")
	}
}

func TestContextKey_UnmarshalYAML_Valid(t *testing.T) {
	yamlData := []byte("message_id")
	var key ContextKey

	err := yaml.Unmarshal(yamlData, &key)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if key != ContextKeyMsgID {
		t.Errorf("expected %s, got %s", ContextKeyMsgID, key)
	}
}

func TestContextKey_UnmarshalYAML_Invalid(t *testing.T) {
	yamlData := []byte("invalid_key")
	var key ContextKey

	err := yaml.Unmarshal(yamlData, &key)
	if err == nil {
		t.Error("expected error for invalid context key, got nil")
	}
}
