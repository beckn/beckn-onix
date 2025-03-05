package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"testing"
)

type Payload struct {
	Context Context `json:"context"`
	Message Message `json:"message"`
}

type Context struct{}

type Message struct{}

func TestInitializeValidDirectory(t *testing.T) {
	provider := &TekuriValidatorProvider{}
	schemaDir := "../testData/schema_valid/"
	_, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestInitializeInValidDirectory(t *testing.T) {
	provider := &TekuriValidatorProvider{}
	schemaDir := "../testData/schema/ondc_trv10/"
	_, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("failed to read schema directory: %v", err)
	}
}

func TestInvalidCompileFile(t *testing.T) {
	schemaDir := "../testData/invalid_compile_schema/"
	if _, err := os.Stat(schemaDir); os.IsNotExist(err) {
		t.Fatalf("Schema directory does not exist: %v", schemaDir)
	}
	provider := &TekuriValidatorProvider{}
	compiledSchema, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("failed to compile JSON schema : %v", err)
	}
	if compiledSchema == nil {
		t.Fatalf("compiled schema is nil : ")

	}
}

func TestInvalidCompileSchema(t *testing.T) {
	schemaDir := "../testData/invalid_schemas/"
	if _, err := os.Stat(schemaDir); os.IsNotExist(err) {
		t.Fatalf("Schema directory does not exist: %v", schemaDir)
	}
	provider := &TekuriValidatorProvider{}
	compiledSchema, _ := provider.Initialize(schemaDir)
	fmt.Println(compiledSchema)
	if compiledSchema == nil {
		t.Fatalf("compiled schema is nil : ")

	}
}

func TestValidateData(t *testing.T) {
	schemaDir := "../testData/schema_valid/"
	if _, err := os.Stat(schemaDir); os.IsNotExist(err) {
		t.Fatalf("Schema directory does not exist: %v", schemaDir)
	}
	provider := &TekuriValidatorProvider{}
	validators, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("Failed to initialize schema provider: %v", err)
	}
	var validator *TekuriValidator
	for _, v := range validators {
		var ok bool
		validator, ok = v.(*TekuriValidator)
		if ok {
			break
		}
	}
	if validator == nil {
		t.Fatalf("No validators found in the map")
	}

	payloadFilePath := "../testData/cancel.json"
	payloadData, err := os.ReadFile(payloadFilePath)
	if err != nil {
		t.Fatalf("Failed to read payload data: %v", err)
	}
	var payload Payload
	err = json.Unmarshal(payloadData, &payload)
	if err != nil {
		log.Fatalf("Failed to unmarshal payload data: %v", err)
	}
	err = validator.Validate(context.Background(), payloadData)
	if err != nil {
		t.Errorf("Validation failed: %v", err)
	} else {
		fmt.Println("Validation succeeded.")
	}
}

func TestInValidateData(t *testing.T) {
	schemaDir := "../testData/schema_valid/"

	if _, err := os.Stat(schemaDir); os.IsNotExist(err) {
		t.Fatalf("Schema directory does not exist: %v", schemaDir)
	}

	provider := &TekuriValidatorProvider{}
	validators, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("Failed to initialize schema provider: %v", err)
	}
	var validator *TekuriValidator
	for _, v := range validators {
		var ok bool
		validator, ok = v.(*TekuriValidator)
		if ok {
			break
		}
	}
	if validator == nil {
		t.Fatalf("No validators found in the map")
	}
	invalidPayloadData := []byte(`"invalid": "data"}`)
	err = validator.Validate(context.Background(), invalidPayloadData)
	if err != nil {
		t.Errorf("Validation failed: %v", err)
	}
}

func TestInValidateUnmarshalData(t *testing.T) {
	schemaDir := "../testdata/schema_valid/"

	if _, err := os.Stat(schemaDir); os.IsNotExist(err) {
		t.Fatalf("Schema directory does not exist: %v", schemaDir)
	}

	provider := &TekuriValidatorProvider{}
	validators, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("Failed to initialize schema provider: %v", err)
	}
	var validator *TekuriValidator
	for _, v := range validators {
		var ok bool
		validator, ok = v.(*TekuriValidator)
		if ok {
			break
		}
	}
	if validator == nil {
		t.Fatalf("No validators found in the map")
	}
	invalidPayloadData := []byte(`{"invalid": "data`)
	err = validator.Validate(context.Background(), invalidPayloadData)
	if err != nil {
		t.Errorf("Error while unmarshaling the data: %v", err)
	}
}

func TestGetProvider(t *testing.T) {
	expected := providerInstance
	actual := GetProvider()

	if actual != expected {
		t.Fatalf("expected %v, got %v", expected, actual)
	} else {
		t.Logf("GetProvider returned the expected providerInstance")
	}
}
