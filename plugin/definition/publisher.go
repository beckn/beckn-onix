package definition

import (
	"context"

	"google.golang.org/api/option"
)

type Publisher interface {
	// Publish sends a message (as a byte slice) using the underlying messaging system.
	Publish(context.Context, string, []byte) error
}

type PublisherProvider interface {
	// New initializes a new publisher instance with the given configuration
	New(ctx context.Context, config map[string]string, opts ...option.ClientOption) (Publisher, func(), error)
}
