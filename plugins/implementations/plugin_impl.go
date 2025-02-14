package main

import (
	"context"
	"encoding/json"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// tekuriValidator implements the Validator interface using the santhosh-tekuri/jsonschema package.
type tekuriValidator struct {
	schema *jsonschema.Schema
}

// Validate validates the given data against the schema.
func (v *tekuriValidator) Validate(ctx context.Context, data []byte) error {
	var jsonData interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return err
	}
	return v.schema.Validate(jsonData)
}

type tekuriValidatorProvider struct{}

func (vp tekuriValidatorProvider) New(schemaPath string) (*tekuriValidator, error) {
	compiler := jsonschema.NewCompiler()
	schema, err := compiler.Compile(schemaPath)
	if err != nil {
		return nil, err
	}
	return &tekuriValidator{schema: schema}, nil
}

var Provider = tekuriValidatorProvider{}
