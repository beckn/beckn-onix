package definition

import (
	"context"
	"net/url"
)

type SchemaValidator interface {
	Validate(ctx context.Context, url *url.URL, b []byte) error
}

type SchemaValidatorProvider interface {
	New(ctx context.Context, config map[string]string) (SchemaValidator, error)
}
