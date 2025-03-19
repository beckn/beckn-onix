package definition

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// SchemaValError represents a single schema validation failure.
type SchemaValError struct {
	Path    string
	Message string
}

// SchemaValidationErr represents a collection of schema validation failures.
type SchemaValidationErr struct {
	Errors []SchemaValError
}

// Validator interface for schema validation.
type SchemaValidator interface {
	Validate(ctx context.Context, url *url.URL, payload []byte) error
}

// ValidatorProvider interface for creating validators.
type SchemaValidatorProvider interface {
	New(ctx context.Context, config map[string]string) (SchemaValidator, func() error, error)
}

// Error implements the error interface for SchemaValidationErr.
func (e *SchemaValidationErr) Error() string {
	var errorMessages []string
	for _, err := range e.Errors {
		errorMessages = append(errorMessages, fmt.Sprintf("%s: %s", err.Path, err.Message))
	}
	return strings.Join(errorMessages, "; ")
}
