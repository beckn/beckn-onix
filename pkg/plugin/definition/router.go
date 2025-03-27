package definition

import (
	"context"
	"net/url"
)

// Route defines the structure for the Route returned.
type Route struct {
	TargetType  string // "url" or "msgq" or "bap" or "bpp"
	PublisherID string // For message queues
	URL         string // For API calls
}

// RouterProvider initializes the a new Router instance with the given config.
type RouterProvider interface {
	New(ctx context.Context, config map[string]string) (Router, func() error, error)
}

// Router defines the interface for routing requests.
type Router interface {
	// Route determines the routing destination based on the request context.
	Route(ctx context.Context, url *url.URL, body []byte) (*Route, error)
}
