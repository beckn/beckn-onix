package main

import (
	"context"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/publisher"
)

// config converts the map[string]string to the publisher.Config struct.
func config(config map[string]string) *publisher.Config {
	return &publisher.Config{
		ProjectID: config["project"],
		TopicID:   config["topic"],
	}
}

// provider implements the PublisherProvider interface.
type provider struct{}

// New creates a new Publisher instance.
func (p provider) New(ctx context.Context, c map[string]string) (definition.Publisher, func(), error) {
	return publisher.New(ctx, config(c))
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider = provider{}
