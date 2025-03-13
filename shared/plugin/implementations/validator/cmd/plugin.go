package main

import (
	"context"

	"beckn-onix/shared/plugin/definition"
	"beckn-onix/shared/plugin/implementations/validator"
)

// ValidatorProvider provides instances of Validator.
type ValidatorProvider struct{}

// New initializes a new Verifier instance.
func (vp ValidatorProvider) New(ctx context.Context, config map[string]string) (map[string]definition.Validator, error) {
	// Create a new Validator instance with the provided configuration
	validators, err := validator.New(ctx, config)
	if err != nil {
		return nil, err
	}

	// Convert the map to the expected type
	result := make(map[string]definition.Validator)
	for key, val := range validators {
		result[key] = val
	}

	return result, nil
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.ValidatorProvider = ValidatorProvider{}
