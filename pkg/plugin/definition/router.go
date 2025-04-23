package definition

import (
	"context"
	"net/url"

	"github.com/beckn/beckn-onix/pkg/model"
)

// RouterProvider initializes the a new Router instance with the given config.
type RouterProvider interface {
	New(ctx context.Context, config map[string]string) (Router, func() error, error)
}

// Router defines the interface for routing requests.
type Router interface {
	// Route determines the routing destination based on the request context.
	Route(ctx context.Context, url *url.URL, body []byte) (*model.Route, error)
}
