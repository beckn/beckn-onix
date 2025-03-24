package model

import (
	"errors"
	"net/http"
	"testing"
)

func TestError_Error(t *testing.T) {
	err := &Error{
		Code:    "400",
		Paths:   "/path/to/field",
		Message: "Invalid value",
	}

	expected := "Error: Code=400, Path=/path/to/field, Message=Invalid value"
	if err.Error() != expected {
		t.Errorf("Expected %s, got %s", expected, err.Error())
	}
}

func TestSchemaValidationErr_Error(t *testing.T) {
	errs := SchemaValidationErr{
		Errors: []Error{
			{Paths: "/field1", Message: "Field is required"},
			{Paths: "/field2", Message: "Invalid format"},
		},
	}

	expected := "/field1: Field is required; /field2: Invalid format"
	if errs.Error() != expected {
		t.Errorf("Expected %s, got %s", expected, errs.Error())
	}
}

func TestSchemaValidationErr_BecknError(t *testing.T) {
	errs := SchemaValidationErr{
		Errors: []Error{
			{Paths: "/field1", Message: "Field is required"},
			{Paths: "/field2", Message: "Invalid format"},
		},
	}

	result := errs.BecknError()
	if result.Code != http.StatusText(http.StatusBadRequest) {
		t.Errorf("Expected %s, got %s", http.StatusText(http.StatusBadRequest), result.Code)
	}

	expectedPaths := "/field1;/field2"
	expectedMessage := "Field is required; Invalid format"
	if result.Paths != expectedPaths {
		t.Errorf("Expected paths %s, got %s", expectedPaths, result.Paths)
	}
	if result.Message != expectedMessage {
		t.Errorf("Expected message %s, got %s", expectedMessage, result.Message)
	}
}

func TestNewSignValidationErrf(t *testing.T) {
	err := NewSignValidationErrf("signature %s", "invalid")
	expected := "signature invalid"
	if err.Error() != expected {
		t.Errorf("Expected %s, got %s", expected, err.Error())
	}
}

func TestNewSignValidationErr(t *testing.T) {
	baseErr := errors.New("invalid signature")
	err := NewSignValidationErr(baseErr)
	if err.Error() != "invalid signature" {
		t.Errorf("Expected %s, got %s", "invalid signature", err.Error())
	}
}

func TestSignValidationErr_BecknError(t *testing.T) {
	err := NewSignValidationErr(errors.New("invalid signature"))
	result := err.BecknError()

	expected := "Signature Validation Error: invalid signature"
	if result.Code != http.StatusText(http.StatusUnauthorized) {
		t.Errorf("Expected %s, got %s", http.StatusText(http.StatusUnauthorized), result.Code)
	}
	if result.Message != expected {
		t.Errorf("Expected %s, got %s", expected, result.Message)
	}
}

func TestNewBadReqErr(t *testing.T) {
	baseErr := errors.New("bad request error")
	err := NewBadReqErr(baseErr)
	if err.Error() != "bad request error" {
		t.Errorf("Expected %s, got %s", "bad request error", err.Error())
	}
}

func TestNewBadReqErrf(t *testing.T) {
	err := NewBadReqErrf("missing %s", "field")
	expected := "missing field"
	if err.Error() != expected {
		t.Errorf("Expected %s, got %s", expected, err.Error())
	}
}

func TestBadReqErr_BecknError(t *testing.T) {
	err := NewBadReqErr(errors.New("invalid payload"))
	result := err.BecknError()

	expected := "BAD Request: invalid payload"
	if result.Code != http.StatusText(http.StatusBadRequest) {
		t.Errorf("Expected %s, got %s", http.StatusText(http.StatusBadRequest), result.Code)
	}
	if result.Message != expected {
		t.Errorf("Expected %s, got %s", expected, result.Message)
	}
}

func TestNewNotFoundErr(t *testing.T) {
	baseErr := errors.New("resource not found")
	err := NewNotFoundErr(baseErr)
	if err.Error() != "resource not found" {
		t.Errorf("Expected %s, got %s", "resource not found", err.Error())
	}
}

func TestNewNotFoundErrf(t *testing.T) {
	err := NewNotFoundErrf("route %s not found", "/api/data")
	expected := "route /api/data not found"
	if err.Error() != expected {
		t.Errorf("Expected %s, got %s", expected, err.Error())
	}
}

func TestNotFoundErr_BecknError(t *testing.T) {
	err := NewNotFoundErr(errors.New("endpoint not available"))
	result := err.BecknError()

	expected := "Endpoint not found: endpoint not available"
	if result.Code != http.StatusText(http.StatusNotFound) {
		t.Errorf("Expected %s, got %s", http.StatusText(http.StatusNotFound), result.Code)
	}
	if result.Message != expected {
		t.Errorf("Expected %s, got %s", expected, result.Message)
	}
}
