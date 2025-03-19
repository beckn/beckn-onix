package main

import (
	"context"
	"fmt"

	"github.com/beckn/beckn-onix/shared/plugin/definition"
	"github.com/beckn/beckn-onix/shared/plugin/implementation/publisher"
)

// Returns error if required fields are missing.
func validateConfig(config map[string]string) (*publisher.Config, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	project, ok := config["project"]
	if !ok || project == "" {
		return nil, fmt.Errorf("project ID is required")
	}

	topic, ok := config["topic"]
	if !ok || topic == "" {
		return nil, fmt.Errorf("topic ID is required")
	}

	return &publisher.Config{
		ProjectID: project,
		TopicID:   topic,
	}, nil
}

// PublisherProvider implements the definition.PublisherProvider interface.
type PublisherProvider struct{}

// New creates a new Publisher instance.
func (p PublisherProvider) New(ctx context.Context, config map[string]string) (definition.Publisher, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context cannot be nil")
	}

	cfg, err := validateConfig(config)
	if err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return publisher.New(ctx, cfg)
}

// Provider is the exported symbol that the plugin manager will look for.
var Provider definition.PublisherProvider = PublisherProvider{}
