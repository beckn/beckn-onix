package definition

import (
	"context"
	"net/url"
)

// Error struct for validation errors
type Error struct {
	Path    string
	Message string
}

// Validator interface for schema validation
type Validator interface {
	Validate(ctx context.Context, url *url.URL, payload []byte) (bool, Error)
}

// ValidatorProvider interface for creating validators
type ValidatorProvider interface {
	New(ctx context.Context, config map[string]string) (map[string]Validator, Error)
}
