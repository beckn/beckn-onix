package main

import (
	"context"
	"errors"

	definition "github.com/beckn/beckn-onix/pkg/plugin/definition"
	validator "github.com/beckn/beckn-onix/pkg/plugin/implementation/schemaValidator"
)

// ValidatorProvider provides instance of Validator.
type schemaValidatorProvider struct{}

// New initializes a new Verifier instance.
func (vp schemaValidatorProvider) New(ctx context.Context, config map[string]string) (definition.SchemaValidator, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	// Extract schema_dir from the config map
	schemaDir, ok := config["schema_dir"]
	if !ok || schemaDir == "" {
		return nil, nil, errors.New("config must contain 'schema_dir'")
	}

	// Create a new Validator instance with the provided configuration
	return validator.New(ctx, &validator.Config{
		SchemaDir: schemaDir, // Pass the schemaDir to the validator.Config
	})
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.SchemaValidatorProvider = &schemaValidatorProvider{}
