package definition

import (
	"context"
	"net/http"
)

type StepContext struct {
	context.Context
	Request           *http.Request
	Body              []byte
	Route             *Route
	SigningKey        string
	SignValidationKey string
}

type Step interface {
	Run(ctx *StepContext) error
}

type StepProvider interface {
	New(context.Context, map[string]string) (Step, func(), error)
}
