package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
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
	provider := &tekuriValidatorProvider{}
	schemaDir := "../schema/ondc_trv10_2.0.0/"
	_, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestInitializeInValidDirectory(t *testing.T) {
	provider := &tekuriValidatorProvider{}
	schemaDir := "../schemas/ondc_trv10_2.0.0/"
	_, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("failed to read schema directory: %v", err)
	}
}

func TestInvalidCompileFile(t *testing.T) {
	schemaDir := "../invalid_schemas/"
	if _, err := os.Stat(schemaDir); os.IsNotExist(err) {
		t.Fatalf("Schema directory does not exist: %v", schemaDir)
	}
	provider := &tekuriValidatorProvider{}
	compiledSchema, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("failed to compile JSON schema : %v", err)
	}
	if compiledSchema == nil {
		t.Fatalf("compiled schema is nil : ")

	}
}

func TestInvalidCompileSchema(t *testing.T) {
	schemaDir := "../invalid_schemas/invalid_compile_schema/"
	if _, err := os.Stat(schemaDir); os.IsNotExist(err) {
		t.Fatalf("Schema directory does not exist: %v", schemaDir)
	}
	provider := &tekuriValidatorProvider{}
	compiledSchema, _ := provider.Initialize(schemaDir)
	fmt.Println(compiledSchema)
	if compiledSchema == nil {
		t.Fatalf("compiled schema is nil : ")

	}
}

func TestValidateData(t *testing.T) {
	schemaDir := "../schema/ondc_trv10_2.0.0/"
	if _, err := os.Stat(schemaDir); os.IsNotExist(err) {
		t.Fatalf("Schema directory does not exist: %v", schemaDir)
	}
	provider := &tekuriValidatorProvider{}
	validators, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("Failed to initialize schema provider: %v", err)
	}
	var validator *tekuriValidator
	for _, v := range validators {
		var ok bool
		validator, ok = v.(*tekuriValidator)
		if ok {
			break
		}
	}
	if validator == nil {
		t.Fatalf("No validators found in the map")
	}

	payloadFilePath := "../test/payload.json"
	payloadData, err := ioutil.ReadFile(payloadFilePath)
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
	schemaDir := "../schema/ondc_trv10_2.0.0/"

	if _, err := os.Stat(schemaDir); os.IsNotExist(err) {
		t.Fatalf("Schema directory does not exist: %v", schemaDir)
	}

	provider := &tekuriValidatorProvider{}
	validators, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("Failed to initialize schema provider: %v", err)
	}
	var validator *tekuriValidator
	for _, v := range validators {
		var ok bool
		validator, ok = v.(*tekuriValidator)
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
	schemaDir := "../schema/ondc_trv10_2.0.0/"

	if _, err := os.Stat(schemaDir); os.IsNotExist(err) {
		t.Fatalf("Schema directory does not exist: %v", schemaDir)
	}

	provider := &tekuriValidatorProvider{}
	validators, err := provider.Initialize(schemaDir)
	if err != nil {
		t.Fatalf("Failed to initialize schema provider: %v", err)
	}
	var validator *tekuriValidator
	for _, v := range validators {
		var ok bool
		validator, ok = v.(*tekuriValidator)
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
