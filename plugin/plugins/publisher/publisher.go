package main

import (
	logger "beckn-onix/shared/utils"
	"errors"
	"strings"
	"time"
)

// PublisherPlugin implements the Plugin interface for Google Cloud Pub/Sub
type PublisherPlugin struct {
	project string
	topic   string
	region  string
}

var (
	ErrProjectMissing = errors.New("missing required field 'project'")
	ErrTopicMissing   = errors.New("missing required field 'topic'")
	ErrRegionMissing  = errors.New("missing required field 'region'")
)

// Handle processes the incoming message
func (p *PublisherPlugin) Handle(message string) {
	timestamp := time.Now().Format(time.RFC3339)

	// In a real implementation, we would use Google Cloud Pub/Sub client
	// For now, we'll simulate the publishing
	// ctx := context.Background()
	messageID := time.Now().UnixNano()

	logger.Log.Info("[","%s","]","Publishing message to Google Cloud Pub/Sub", timestamp)
	logger.Log.Info("  Project: ", p.project)
	logger.Log.Info("  Topic: ", p.topic)
	logger.Log.Info("  Region: ", p.region)
	logger.Log.Info("  Message: ", message)
	logger.Log.Info("  Message ID:  (simulated)", messageID)

	// In a real implementation, we would use:
	// client, err := pubsub.NewClient(ctx, p.project)
	// if err != nil { logger.Log.Info("Failed to create client: %v", err); return }
	// defer client.Close()
	//
	// topic := client.Topic(p.topic)
	// result := topic.Publish(ctx, &pubsub.Message{Data: []byte(message)})
	// id, err := result.Get(ctx)
	// if err != nil { logger.Log.Info("Failed to publish: %v", err); return }
	// logger.Log.Info("Published message with ID: %s", id)
}

// Configure sets up the plugin with the provided configuration
func (p *PublisherPlugin) Configure(config map[string]interface{}) error {
	// Validate required fields
	if project, ok := config["project"]; ok {
		p.project = project.(string)
	} else {
		return ErrProjectMissing
	}

	if topic, ok := config["topic"]; ok {
		p.topic = topic.(string)
	} else {
		return ErrTopicMissing
	}

	if region, ok := config["region"]; ok {
		p.region = region.(string)
	} else {
		return ErrRegionMissing
	}

	// Validate non-empty values
	if strings.TrimSpace(p.project) == "" {
		return ErrProjectMissing
	}
	if strings.TrimSpace(p.topic) == "" {
		return ErrTopicMissing
	}
	if strings.TrimSpace(p.region) == "" {
		return ErrRegionMissing
	}

	logger.Log.Info("Publisher plugin configured with project=",p.project,"topic=", p.topic, "region=",p.region)

	return nil
}

// Export the plugin
var Plugin PublisherPlugin
