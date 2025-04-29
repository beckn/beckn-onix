package main

import (
	"context"
	"fmt"

	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/plugin/implementation/publisher"
	"github.com/rabbitmq/amqp091-go"
)

// publisherProvider implements the PublisherProvider interface.
// It is responsible for creating a new Publisher instance.
type publisherProvider struct{}

// New creates a new Publisher instance based on the provided configuration map.
// It also returns a cleanup function to close resources and any potential errors encountered.
func (p *publisherProvider) New(ctx context.Context, config map[string]string) (definition.Publisher, func() error, error) {
	// Step 1: Map config
	cfg := &publisher.Config{
		Addr:       config["addr"],
		Exchange:   config["exchange"],
		RoutingKey: config["routing_key"],
		Durable:    config["durable"] == "true",
		UseTLS:     config["use_tls"] == "true",
	}
	log.Debugf(ctx, "Publisher config mapped: %+v", cfg)

	// Step 2: Validate
	if err := publisher.Validate(cfg); err != nil {
		log.Errorf(ctx, err, "Publisher config validation failed")
		return nil, nil, err
	}
	log.Infof(ctx, "Publisher config validated successfully")

	// Step 3:URL
	connURL, err := publisher.GetConnURL(cfg)
	if err != nil {
		log.Errorf(ctx, err, "Failed to build RabbitMQ connection URL")
		return nil, nil, fmt.Errorf("failed to build connection URL: %w", err)
	}
	log.Debugf(ctx, "RabbitMQ connection URL built: %s", connURL)
	// Step 4: Connect
	conn, err := amqp091.Dial(connURL)
	if err != nil {
		log.Errorf(ctx, err, "Failed to connect to RabbitMQ")
		return nil, nil, fmt.Errorf("%w: %v", publisher.ErrConnectionFailed, err)
	}
	log.Infof(ctx, "Connected to RabbitMQ")

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		log.Errorf(ctx, err, "Failed to open RabbitMQ channel")
		return nil, nil, fmt.Errorf("%w: %v", publisher.ErrChannelFailed, err)
	}
	log.Infof(ctx, "RabbitMQ channel opened successfully")

	// Step 5: Declare Exchange
	err = ch.ExchangeDeclare(
		cfg.Exchange,
		"topic",
		cfg.Durable,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		ch.Close()
		conn.Close()
		log.Errorf(ctx, err, "Failed to declare exchange: %s", cfg.Exchange)
		return nil, nil, fmt.Errorf("%w: %v", publisher.ErrExchangeDeclare, err)
	}
	log.Infof(ctx, "RabbitMQ exchange declared successfully: %s", cfg.Exchange)

	// Step 6: Create publisher instance
	publisher := &publisher.Publisher{
		Conn:    conn,
		Channel: ch,
		Config:  cfg,
	}

	cleanup := func() error {
		log.Infof(ctx, "Cleaning up RabbitMQ resources")
		_ = ch.Close()
		return conn.Close()
	}

	log.Infof(ctx, "Publisher instance created successfully")
	return publisher, cleanup, nil
}

// Provider is the instance of publisherProvider that implements the PublisherProvider interface.
var Provider = publisherProvider{}
