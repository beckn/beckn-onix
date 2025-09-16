package main

import (
	"context"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/publisher"
)

// publisherProvider implements the PublisherProvider interface.
// It is responsible for creating a new Publisher instance.
type publisherProvider struct{}

// New creates a new Publisher instance based on the provided configuration.
func (p *publisherProvider) New(ctx context.Context, config map[string]string) (definition.Publisher, func() error, error) {
	cfg := &publisher.Config{
		Addr:       config["addr"],
		Exchange:   config["exchange"],
		RoutingKey: config["routing_key"],
		Durable:    config["durable"] == "true",
		UseTLS:     config["use_tls"] == "true",
	}
	log.Debugf(ctx, "Publisher config mapped: %+v", cfg)

	pub, cleanup, err := publisher.New(cfg)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create publisher instance")
		return nil, nil, err
	}

	log.Infof(ctx, "Publisher instance created successfully")
	return pub, cleanup, nil
}

// Provider is the instance of publisherProvider that implements the PublisherProvider interface.
var Provider = publisherProvider{}
