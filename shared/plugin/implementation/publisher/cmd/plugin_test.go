package main

import (
	"context"
	"testing"

	"github.com/beckn/beckn-onix/shared/plugin/definition"
)

// MockPublisher is a mock implementation of the definition.Publisher interface for testing.
type MockPublisher struct{}

func (m *MockPublisher) Publish(ctx context.Context, msg []byte) error {
	return nil
}

func (m *MockPublisher) Close() error {
	return nil
}

// TestValidateConfig tests the validateConfig function.
func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]string
		wantErr bool
	}{
		{"Valid config", map[string]string{"project": "test-project", "topic": "test-topic"}, false},
		{"Nil config", nil, true},
		{"Missing project", map[string]string{"topic": "test-topic"}, true},
		{"Missing topic", map[string]string{"project": "test-project"}, true},
		{"Empty project", map[string]string{"project": "", "topic": "test-topic"}, true},
		{"Empty topic", map[string]string{"project": "test-project", "topic": ""}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestPublisherProviderNew tests the New method of PublisherProvider.
func TestPublisherProviderNew(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		config  map[string]string
		wantErr bool
	}{
		{"Nil context", nil, map[string]string{"project": "test-project", "topic": "test-topic"}, true},
		{"Invalid config", context.Background(), map[string]string{"project": "", "topic": "test-topic"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := PublisherProvider{}
			_, err := provider.New(tt.ctx, tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("PublisherProvider.New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestProviderImplementation verifies that the Provider is correctly initialized.
func TestProviderImplementation(t *testing.T) {
	if _, ok := interface{}(Provider).(definition.PublisherProvider); !ok {
		t.Errorf("Provider does not implement definition.PublisherProvider")
	}
}
