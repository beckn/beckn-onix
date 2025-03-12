package main

import (
	"strings"
	"testing"
)

// TestPublishSuccess tests successful publish scenarios
func TestPublishSuccess(t *testing.T) {
	tests := []struct {
		name    string
		message string
		config  map[string]interface{}
	}{
		{
			name: "Valid JSON message",
			message: `{
                "data": "test message",
                "metadata": {"key": "value"}
            }`,
			config: map[string]interface{}{
				"project": "test-project",
				"topic":   "test-topic",
			},
		},
		{
			name: "Valid message with all fields",
			message: `{
                "data": "complete message",
                "metadata": {
                    "key1": "value1",
                    "key2": "value2"
                },
                "timestamp": "2024-03-12T00:00:00Z"
            }`,
			config: map[string]interface{}{
				"project": "test-project",
				"topic":   "test-topic",
			},
		},
		{
			name:    "Valid message with minimum fields",
			message: `{"data": "minimal"}`,
			config: map[string]interface{}{
				"project": "test-project",
				"topic":   "test-topic",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PublisherPlugin{}
			if err := p.Configure(tt.config); err != nil {
				t.Fatalf("Configure() error = %v", err)
			}

			if err := p.Publish(tt.message); err != nil {
				t.Errorf("Publish() error = %v", err)
			}

			// Verify message was stored
			messages := p.GetMessages()
			if len(messages) == 0 {
				t.Error("No messages stored")
			}

			// Verify last message
			lastMsg := messages[len(messages)-1]
			if lastMsg.Payload != tt.message {
				t.Errorf("Message payload = %v, want %v", lastMsg.Payload, tt.message)
			}
		})
	}
}

// TestPublishFailure tests failure scenarios for publish
func TestPublishFailure(t *testing.T) {
	tests := []struct {
		name      string
		message   string
		config    map[string]interface{}
		setupFunc func(*PublisherPlugin)
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "Empty message",
			message: "",
			config: map[string]interface{}{
				"project": "test-project",
				"topic":   "test-topic",
			},
			wantErr:   true,
			errSubstr: "empty message",
		},
		{
			name:    "Invalid JSON",
			message: `{"invalid": json}`,
			config: map[string]interface{}{
				"project": "test-project",
				"topic":   "test-topic",
			},
			wantErr:   true,
			errSubstr: "invalid JSON",
		},
		{
			name:      "Unconfigured publisher",
			message:   `{"data": "test"}`,
			config:    nil,
			wantErr:   true,
			errSubstr: "not properly configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PublisherPlugin{}
			if tt.config != nil {
				if err := p.Configure(tt.config); err != nil {
					t.Fatalf("Configure() error = %v", err)
				}
			}

			if tt.setupFunc != nil {
				tt.setupFunc(p)
			}

			err := p.Publish(tt.message)
			if (err != nil) != tt.wantErr {
				t.Errorf("Publish() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("Publish() error = %v, want error containing %q", err, tt.errSubstr)
			}
		})
	}
}

// TestHandleSuccess tests successful message handling
func TestHandleSuccess(t *testing.T) {
	tests := []struct {
		name    string
		message string
		config  map[string]interface{}
	}{
		{
			name:    "Handle valid message",
			message: `{"data": "test message"}`,
			config: map[string]interface{}{
				"project": "test-project",
				"topic":   "test-topic",
			},
		},
		{
			name:    "Handle message with different topic",
			message: `{"data": "different topic"}`,
			config: map[string]interface{}{
				"project": "test-project",
				"topic":   "different-topic",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PublisherPlugin{}
			if err := p.Configure(tt.config); err != nil {
				t.Fatalf("Configure() error = %v", err)
			}

			if err := p.Handle(tt.message); err != nil {
				t.Errorf("Handle() error = %v", err)
			}

			// Verify message was handled
			messages := p.GetMessages()
			if len(messages) == 0 {
				t.Error("No messages handled")
			}
		})
	}
}

// TestConfigureSuccess tests successful configuration scenarios
func TestConfigureSuccess(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
	}{
		{
			name: "Valid minimal config",
			config: map[string]interface{}{
				"project": "test-project",
				"topic":   "test-topic",
			},
		},
		{
			name: "Valid config with optional fields",
			config: map[string]interface{}{
				"project":    "test-project",
				"topic":      "test-topic",
				"region":     "us-west",
				"batch_size": 100,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PublisherPlugin{}
			if err := p.Configure(tt.config); err != nil {
				t.Errorf("Configure() error = %v", err)
			}

			if p.project != tt.config["project"] {
				t.Errorf("project = %v, want %v", p.project, tt.config["project"])
			}
			if p.topic != tt.config["topic"] {
				t.Errorf("topic = %v, want %v", p.topic, tt.config["topic"])
			}
		})
	}
}

// TestConfigureFailure tests configuration failure scenarios
func TestConfigureFailure(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]interface{}
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "Missing project",
			config:    map[string]interface{}{"topic": "test-topic"},
			wantErr:   true,
			errSubstr: "project",
		},
		{
			name:      "Missing topic",
			config:    map[string]interface{}{"project": "test-project"},
			wantErr:   true,
			errSubstr: "topic",
		},
		{
			name: "Empty project",
			config: map[string]interface{}{
				"project": "",
				"topic":   "test-topic",
			},
			wantErr:   true,
			errSubstr: "project",
		},
		{
			name: "Empty topic",
			config: map[string]interface{}{
				"project": "test-project",
				"topic":   "",
			},
			wantErr:   true,
			errSubstr: "topic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PublisherPlugin{}
			err := p.Configure(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("Configure() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("Configure() error = %v, want error containing %q", err, tt.errSubstr)
			}
		})
	}
}

// TestMessageCreation tests message creation and validation
func TestMessageCreation(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    PublisherMessage
	}{
		{
			name:    "Create message with payload",
			payload: `{"data": "test"}`,
			want: PublisherMessage{
				Payload: `{"data": "test"}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PublisherPlugin{}
			p.Configure(map[string]interface{}{
				"project": "test-project",
				"topic":   "test-topic",
			})

			err := p.Handle(tt.payload)
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}

			messages := p.GetMessages()
			if len(messages) == 0 {
				t.Fatal("No messages created")
			}

			got := messages[len(messages)-1]
			if got.Payload != tt.want.Payload {
				t.Errorf("Message payload = %v, want %v", got.Payload, tt.want.Payload)
			}
			if got.Topic != p.topic {
				t.Errorf("Message topic = %v, want %v", got.Topic, p.topic)
			}
			if got.Project != p.project {
				t.Errorf("Message project = %v, want %v", got.Project, p.project)
			}
		})
	}
}
