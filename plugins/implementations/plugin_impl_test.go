package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type Payload struct {
	Context Context `json:"context"`
	Message Message `json:"message"`
}

type Context struct{}
type Message struct{}

func TestValidDirectory(t *testing.T) {
	provider := &TekuriValidatorProvider{}
	schemaDir := "../testData/schema_valid/"
	_, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidPayload(t *testing.T) {
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

	payloadFilePath := "../testData/payloads/search.json"
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

func TestInValidDirectory(t *testing.T) {
	provider := &TekuriValidatorProvider{}
	schemaDir := "../testData/schema/ondc_trv10/"
	_, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("failed to read schema directory: %v", err)
	}
}

func TestInvalidCompileFile(t *testing.T) {
	schemaDir := "../testData/invalid_compile_schema/"
	// if _, err := os.Stat(schemaDir); os.IsNotExist(err) {
	// 	t.Fatalf("Schema directory does not exist: %v", schemaDir)
	// }
	provider := &TekuriValidatorProvider{}
	_, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("failed to compile JSON schema : %v", err)
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

func TestPayloadWithExtraFields(t *testing.T) {
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
	payloadFilePath := "../testData/payloads/search_extraField.json"
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
		t.Errorf("Validation failed due to extra fields: %v", err)
	} else {
		fmt.Println("Validation succeeded.")
	}

}

func TestPayloadWithMissingFields(t *testing.T) {
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
	payloadFilePath := "../testData/payloads/search_missingField.json"
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
		t.Errorf("Validation failed with missing fields: %v", err)
	} else {
		fmt.Println("Validation succeeded.")
	}

}

func TestInValidPayload(t *testing.T) {
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
		t.Fatalf("Validation failed: %v", err)
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

func TestInitialize_SchemaPathNotDirectory(t *testing.T) {
	vp := &TekuriValidatorProvider{}
	filePath := "../testdata/directory.json"

	_, err := vp.Initialize(filePath)

	if err == nil || !strings.Contains(err.Error(), "provided schema path is not a directory") {
		t.Errorf("Expected error about non-directory schema path, got: %v", err)
	}
}

func TestInitialize_InvalidSchemaFileStructure(t *testing.T) {
	schemaDir := "../testData/invalid_structure"
	provider := &TekuriValidatorProvider{}

	_, err := provider.Initialize(schemaDir)
	if err == nil || !strings.Contains(err.Error(), "invalid schema file structure") {
		t.Fatalf("Expected error for invalid schema file structure, got: %v", err)
	}

}

func TestInitialize_FailedToGetRelativePath(t *testing.T) {
	schemaDir := "../testData/valid_schemas"
	provider := &TekuriValidatorProvider{}

	// Use an absolute path for schemaDir and a relative path for the file to simulate the error
	absSchemaDir, err := filepath.Abs(schemaDir)
	if err != nil {
		t.Fatalf("Failed to get absolute path for schema directory: %v", err)
	}

	// Temporarily change the working directory to simulate different volumes
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %v", err)
	}
	defer os.Chdir(originalWd) // Restore the original working directory after the test

	// Change to a different directory
	os.Chdir("/tmp")

	_, err = provider.Initialize(absSchemaDir)
	if err == nil || !strings.Contains(err.Error(), "failed to get relative path for file") {
		t.Fatalf("Expected error for failing to get relative path, got: %v", err)
	}

}
