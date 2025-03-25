package definition

import (
	"context"
	"net/url"
)

// SchemaValidator interface for schema validation.
type SchemaValidator interface {
	Validate(ctx context.Context, url *url.URL, reqBody []byte) error
}

// SchemaValidatorProvider interface for creating validators.
type SchemaValidatorProvider interface {
	New(ctx context.Context, config map[string]string) (SchemaValidator, func() error, error)
}
