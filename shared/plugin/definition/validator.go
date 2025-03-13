package definition

import (
	"context"
	"net/url"
)

// Validator interface for schema validation
type Validator interface {
	Validate(ctx context.Context, url url.URL, payload []byte) (bool, error)
}

// ValidatorProvider interface for creating validators
type ValidatorProvider interface {
	New(ctx context.Context, config map[string]string) (map[string]Validator, error)
}
