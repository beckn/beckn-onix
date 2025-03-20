package publisher

import (
	"context"
	"errors"
	"testing"
)

// TestValidate tests the validate function.
func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr error
	}{
		{
			name:    "Valid config",
			config:  &Config{ProjectID: "test-project", TopicID: "test-topic"},
			wantErr: nil,
		},
		{
			name:    "Nil config",
			config:  nil,
			wantErr: ErrEmptyConfig,
		},
		{
			name:    "Empty project ID",
			config:  &Config{ProjectID: "", TopicID: "test-topic"},
			wantErr: ErrProjectMissing,
		},
		{
			name:    "Whitespace project ID",
			config:  &Config{ProjectID: "   ", TopicID: "test-topic"},
			wantErr: ErrProjectMissing,
		},
		{
			name:    "Empty topic ID",
			config:  &Config{ProjectID: "test-project", TopicID: ""},
			wantErr: ErrTopicMissing,
		},
		{
			name:    "Whitespace topic ID",
			config:  &Config{ProjectID: "test-project", TopicID: "  "},
			wantErr: ErrTopicMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.config)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestNew tests the New function with validation errors only.
// We can't easily test the pubsub client creation parts without complex mocks.
func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		config  *Config
		wantErr bool
	}{
		{
			// Should fail validation
			name:    "Empty project ID",
			ctx:     context.Background(),
			config:  &Config{ProjectID: "", TopicID: "test-topic"},
			wantErr: true,
		},
		{
			// Should fail validation
			name:    "Empty topic ID",
			ctx:     context.Background(),
			config:  &Config{ProjectID: "test-project", TopicID: ""},
			wantErr: true,
		},
		{
			// Should fail due to nil context
			name:    "Nil context",
			ctx:     nil,
			config:  &Config{ProjectID: "test-project", TopicID: "test-topic"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.ctx, tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
