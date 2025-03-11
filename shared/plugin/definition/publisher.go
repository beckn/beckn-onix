package definition

import (
	logger "beckn-onix/shared/log"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Publisher defines the interface for publishing messages
type Publisher interface {
	Handle(message string) error  // Use Handle method instead of embedding Plugin
	Publish(message string) error // Add specific publisher method
}

type PublisherPlugin struct {
	project   string
	topic     string
	region    string
	validator *ValidatorPlugin
}

var (
	ErrProjectMissing = errors.New("missing required field 'project'")
	ErrTopicMissing   = errors.New("missing required field 'topic'")
	ErrRegionMissing  = errors.New("missing required field 'region'")
)

// Handle processes the incoming message
func (p *PublisherPlugin) Handle(message string) error {
	// Validate plugin is properly configured
	if p.project == "" || p.topic == "" || p.region == "" {
		return errors.New("publisher not properly configured")
	}

	// First validate the message if validator is configured
	if p.validator != nil {
		if err := p.validator.Handle(message); err != nil {
			logger.Log.Error("Validation failed: ", err)
			return fmt.Errorf("validation failed: %w", err)
		}
	}

	timestamp := time.Now().Format(time.RFC3339)
	messageID := time.Now().UnixNano()

	logger.Log.Info(timestamp, "Publishing message to Google Cloud Pub/Sub")
	logger.Log.Info("  Project: ", p.project)
	logger.Log.Info("  Topic: ", p.topic)
	logger.Log.Info("  Region: ", p.region)
	logger.Log.Info("  Message: ", message)
	logger.Log.Info("  Message ID: ", messageID, " (simulated)")

	return nil
}

// Publish implements the Publisher interface
func (p *PublisherPlugin) Publish(message string) error {
	if message == "" {
		return errors.New("empty message")
	}
	return p.Handle(message)
}

// Configure sets up the plugin with the provided configuration
func (p *PublisherPlugin) Configure(config map[string]interface{}) error {
	// Configure validator if present in config
	if validatorConfig, ok := config["validator"].(map[string]interface{}); ok {
		p.validator = &ValidatorPlugin{}
		if err := p.validator.Configure(validatorConfig); err != nil {
			return err
		}
		logger.Log.Info("Validator configured for publisher")
	}

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

	logger.Log.Info("Publisher plugin configured with project=", p.project, "topic=", p.topic, "region=", p.region)

	return nil
}

// Export the plugin
var Plugin PublisherPlugin
