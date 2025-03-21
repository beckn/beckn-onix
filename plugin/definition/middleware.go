package definition

import (
	"context"
	"net/http"
)

type MiddlewareProvider interface {
	New(ctx context.Context, cfg map[string]string) (func(http.Handler) http.Handler, error)
}
