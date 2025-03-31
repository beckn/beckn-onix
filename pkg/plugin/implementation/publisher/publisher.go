package publisher

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cloud.google.com/go/pubsub"
	"github.com/beckn/beckn-onix/pkg/log"
	"google.golang.org/api/option"
)

// Config holds the Pub/Sub configuration.
type Config struct {
	ProjectID string
	TopicID   string
}

// Publisher is a concrete implementation of a Google Cloud Pub/Sub publisher.
type Publisher struct {
	client *pubsub.Client
	topic  *pubsub.Topic
	config *Config
}

var (
	ErrProjectMissing = errors.New("missing required field 'Project'")
	ErrTopicMissing   = errors.New("missing required field 'Topic'")
	ErrEmptyConfig    = errors.New("empty config")
)

func validate(cfg *Config) error {
	if cfg == nil {
		return ErrEmptyConfig
	}
	if strings.TrimSpace(cfg.ProjectID) == "" {
		return ErrProjectMissing
	}
	if strings.TrimSpace(cfg.TopicID) == "" {
		return ErrTopicMissing
	}
	return nil
}

// New initializes a new Publisher instance.
// It creates a real pubsub.Client and then calls NewWithClient.
func New(ctx context.Context, cfg *Config, opts ...option.ClientOption) (*Publisher, func() error, error) {
	if err := validate(cfg); err != nil {
		return nil, nil, err
	}
	client, err := pubsub.NewClient(ctx, cfg.ProjectID, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create pubsub client: %w", err)
	}

	topic := client.Topic(cfg.TopicID)
	exists, err := topic.Exists(ctx)
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("failed to check topic existence: %w", err)
	}
	if !exists {
		_ = client.Close()
		return nil, nil, fmt.Errorf("topic %s does not exist", cfg.TopicID)
	}
	return &Publisher{
		client: client,
		topic:  topic,
		config: cfg,
	}, client.Close, nil
}

// Publisher Methods.

// Publish sends a message to Google Cloud Pub/Sub.
func (p *Publisher) Publish(ctx context.Context, topic string, msg []byte) error {
	pubsubMsg := &pubsub.Message{
		Data: msg,
	}

	result := p.topic.Publish(ctx, pubsubMsg)
	id, err := result.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	log.Infof(ctx, "Published message with ID: %s\n", id)
	return nil
}
