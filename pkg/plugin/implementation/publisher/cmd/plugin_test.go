package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/publisher"
	"github.com/rabbitmq/amqp091-go"
)

type mockChannel struct{}

func (m *mockChannel) PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp091.Publishing) error {
	return nil
}
func (m *mockChannel) ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp091.Table) error {
	return nil
}
func (m *mockChannel) Close() error {
	return nil
}

func TestPublisherProvider_New_Success(t *testing.T) {
	// Save original dialFunc and channelFunc
	originalDialFunc := publisher.DialFunc
	originalChannelFunc := publisher.ChannelFunc
	defer func() {
		publisher.DialFunc = originalDialFunc
		publisher.ChannelFunc = originalChannelFunc
	}()

	// Override mocks
	publisher.DialFunc = func(url string) (*amqp091.Connection, error) {
		return nil, nil
	}
	publisher.ChannelFunc = func(conn *amqp091.Connection) (publisher.Channel, error) {
		return &mockChannel{}, nil
	}

	t.Setenv("RABBITMQ_USERNAME", "guest")
	t.Setenv("RABBITMQ_PASSWORD", "guest")

	config := map[string]string{
		"addr":        "localhost",
		"exchange":    "test-exchange",
		"routing_key": "test.key",
		"durable":     "true",
		"use_tls":     "false",
	}

	ctx := context.Background()
	pub, cleanup, err := Provider.New(ctx, config)

	if err != nil {
		t.Fatalf("Provider.New returned error: %v", err)
	}
	if pub == nil {
		t.Fatal("Expected non-nil publisher")
	}
	if cleanup == nil {
		t.Fatal("Expected non-nil cleanup function")
	}

	if err := cleanup(); err != nil {
		t.Errorf("Cleanup returned error: %v", err)
	}
}

func TestPublisherProvider_New_Failure(t *testing.T) {
	// Save and restore dialFunc
	originalDialFunc := publisher.DialFunc
	defer func() { publisher.DialFunc = originalDialFunc }()

	// Simulate dial failure
	publisher.DialFunc = func(url string) (*amqp091.Connection, error) {
		return nil, errors.New("dial failed")
	}

	t.Setenv("RABBITMQ_USERNAME", "guest")
	t.Setenv("RABBITMQ_PASSWORD", "guest")

	config := map[string]string{
		"addr":        "localhost",
		"exchange":    "test-exchange",
		"routing_key": "test.key",
		"durable":     "true",
	}

	ctx := context.Background()
	pub, cleanup, err := Provider.New(ctx, config)

	if err == nil {
		t.Fatal("Expected error from Provider.New but got nil")
	}
	if !strings.Contains(err.Error(), "dial failed") {
		t.Errorf("Expected 'dial failed' error, got: %v", err)
	}
	if pub != nil {
		t.Errorf("Expected nil publisher, got: %v", pub)
	}
	if cleanup != nil {
		t.Error("Expected nil cleanup, got non-nil")
	}
}
