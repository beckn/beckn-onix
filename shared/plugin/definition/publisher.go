package main

import (
	logger "beckn-onix/shared/log"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Message represents the structure of a message to be published
type Message struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Payload   string `json:"payload"`
	Topic     string `json:"topic"`
	Project   string `json:"project"`
}

// Publisher interface extends Plugin
type PublisherInterface interface {
	PluginInterface
	Publish(message string) error
}

// Plugin interface that all plugins must implement
type PluginInterface interface {
	Handle(message string) error
	Configure(config map[string]interface{}) error
}

// PublisherPlugin implements the Publisher interface
type PublisherPlugin struct {
	project  string
	topic    string
	messages []Message // Store messages for demonstration
}

var (
	ErrProjectMissing = errors.New("missing required field 'project'")
	ErrTopicMissing   = errors.New("missing required field 'topic'")
)

// Handle processes the incoming message
func (p *PublisherPlugin) Handle(message string) error {
	if p.project == "" || p.topic == ""  {
		return errors.New("publisher not properly configured")
	}

	// Create a new message
	msg := Message{
		ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		Timestamp: time.Now().Format(time.RFC3339),
		Payload:   message,
		Topic:     p.topic,
		Project:   p.project,
	}

	// Convert message to JSON for logging
	jsonMsg, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		logger.Log.Error("Failed to marshal message:", err)
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Log the formatted message
	logger.Log.Info("Publishing message:")
	logger.Log.Info(string(jsonMsg))

	// Store message (simulating publish)
	p.messages = append(p.messages, msg)

	return nil
}

// Publish implements the Publisher interface
func (p *PublisherPlugin) Publish(message string) error {
	if message == "" {
		return errors.New("empty message")
	}

	// Validate message format (assuming JSON)
	var js json.RawMessage
	if err := json.Unmarshal([]byte(message), &js); err != nil {
		logger.Log.Error("Invalid JSON message:", err)
		return fmt.Errorf("invalid JSON message: %w", err)
	}

	return p.Handle(message)
}

// Configure sets up the plugin with the provided configuration
func (p *PublisherPlugin) Configure(config map[string]interface{}) error {
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



	if strings.TrimSpace(p.project) == "" {
		return ErrProjectMissing
	}
	if strings.TrimSpace(p.topic) == "" {
		return ErrTopicMissing
	}
	

	logger.Log.Info("Publisher plugin configured with project=", p.project, "topic=", p.topic)

	return nil
}

// GetMessages returns all published messages (for demonstration)
func (p *PublisherPlugin) GetMessages() []Message {
	return p.messages
}

// Export the plugin
var Plugin PublisherPlugin
