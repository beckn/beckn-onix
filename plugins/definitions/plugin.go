package definitions

import "context"

// Validator interface for schema validation
type Validator interface {
	Validate(ctx context.Context, b []byte) error
}

// ValidatorProvider interface for creating validators
type ValidatorProvider interface {
	//Get(p string) (Validator, error)
	Initialize(schemaDir string) (map[string]Validator, error)
}
