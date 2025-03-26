package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
)

func TestError_Error(t *testing.T) {
	err := &Error{
		Code:    "404",
		Paths:   "/api/v1/user",
		Message: "User not found",
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
			{Paths: "/user", Message: "Field required"},
			{Paths: "/email", Message: "Invalid format"},
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
			{Paths: "/user", Message: "Field required"},
		},
	}

	beErr := schemaErr.BecknError()
	expected := "Bad Request"
	if beErr.Code != expected {
		t.Errorf("err.Error() = %s, want %s",
			beErr.Code, expected)
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
		t.Errorf("expected %s, got %s", expectedMsg, beErr.Message)
	}
}

func TestNewBadReqErrf(t *testing.T) {
	badReqErr := NewBadReqErrf("invalid field %s", "name")
	expected := "invalid field name"
	if badReqErr.Error() != expected {
		t.Errorf("expected %s, got %s", expected, badReqErr.Error())
	}
}

func TestNewBadReqErr(t *testing.T) {
	err := errors.New("bad request")
	badReqErr := NewBadReqErr(err)

	if badReqErr.Error() != err.Error() {
		t.Errorf("expected %s, got %s", err.Error(), badReqErr.Error())
	}
}

func TestNotFoundErr_BecknError(t *testing.T) {
	notFoundErr := NewNotFoundErr(errors.New("resource not found"))
	beErr := notFoundErr.BecknError()

	expectedMsg := "Endpoint not found: resource not found"
	if beErr.Message != expectedMsg {
		t.Errorf("expected %s, got %s", expectedMsg, beErr.Message)
	}
}

func TestNewNotFoundErrf(t *testing.T) {
	notFoundErr := NewNotFoundErrf("resource %s not found", "user")
	expected := "resource user not found"
	if notFoundErr.Error() != expected {
		t.Errorf("expected %s, got %s", expected, notFoundErr.Error())
	}
}

func TestNewNotFoundErr(t *testing.T) {
	err := errors.New("not found")
	notFoundErr := NewNotFoundErr(err)

	if notFoundErr.Error() != err.Error() {
		t.Errorf("expected %s, got %s", err.Error(), notFoundErr.Error())
	}
}

func TestRole_UnmarshalYAML_ValidRole(t *testing.T) {
	var role Role
	yamlData := []byte("bap")

	err := yaml.Unmarshal(yamlData, &role)
	assert.NoError(t, err)
	assert.Equal(t, RoleBAP, role)
}

func TestRole_UnmarshalYAML_InvalidRole(t *testing.T) {
	var role Role
	yamlData := []byte("invalid")

	err := yaml.Unmarshal(yamlData, &role)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Role")
}
