package definition

import (
	"context"
	"net/url"
)

type Router interface {
	Route(ctx context.Context, url *url.URL, body []byte) (*Route, error)
}

type RouterProvider interface {
	New(ctx context.Context, cfg map[string]string) (Router, error)
}

type Route struct {
	Type      string
	URL       *url.URL
	Publisher string
}
