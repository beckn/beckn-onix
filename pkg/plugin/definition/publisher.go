package definition

import "context"

// Publisher defines the general publisher interface for messaging plugins.
type Publisher interface {
	// Publish sends a message (as a byte slice) using the underlying messaging system.
	Publish(context.Context, string, []byte) error
}

// PublisherProvider is the interface for creating new Publisher instances.
type PublisherProvider interface {
	// New initializes a new publisher instance with the given configuration.
	New(ctx context.Context, config map[string]string) (Publisher, func() error, error)
}
