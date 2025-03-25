package definition

import (
	"context"
	"net/url"

	"github.com/beckn/beckn-onix/pkg/model"
)

type Router interface {
	Route(ctx context.Context, url *url.URL, body []byte) (*model.Route, error)
}

type RouterProvider interface {
	New(ctx context.Context, cfg map[string]string) (Router, error)
}
