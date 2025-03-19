package definition

import "context"

// Publisher defines the general publisher interface for messaging plugins.
type Publisher interface {
	// Publish sends a message (as a byte slice) using the underlying messaging system.
	Publish(ctx context.Context, msg []byte) error

	Close() error // Important for releasing resources
}

type PublisherProvider interface {
	// New initializes a new publisher instance with the given configuration
	New(ctx context.Context, config map[string]string) (Publisher, error)
}
