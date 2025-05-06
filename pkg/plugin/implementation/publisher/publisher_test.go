package publisher

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/rabbitmq/amqp091-go"
)

func TestGetConnURLSuccess(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
	}{
		{
			name: "Valid config with credentials",
			config: &Config{
				Addr:   "localhost:5672",
				UseTLS: false,
			},
		},
		{
			name: "Valid config with vhost",
			config: &Config{
				Addr:   "localhost:5672/myvhost",
				UseTLS: false,
			},
		},
	}

	// Set valid credentials
	os.Setenv("RABBITMQ_USERNAME", "guest")
	os.Setenv("RABBITMQ_PASSWORD", "guest")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := GetConnURL(tt.config)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if url == "" {
				t.Error("expected non-empty URL, got empty string")
			}
		})
	}
}

func TestGetConnURLFailure(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		config   *Config
		wantErr  bool
	}{
		{
			name:     "Missing credentials",
			username: "",
			password: "",
			config:   &Config{Addr: "localhost:5672"},
			wantErr:  true,
		},
		{
			name:     "Missing config address",
			username: "guest",
			password: "guest",
			config:   &Config{}, // this won't error unless Validate() is called separately
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.username == "" {
				os.Unsetenv("RABBITMQ_USERNAME")
			} else {
				os.Setenv("RABBITMQ_USERNAME", tt.username)
			}

			if tt.password == "" {
				os.Unsetenv("RABBITMQ_PASSWORD")
			} else {
				os.Setenv("RABBITMQ_PASSWORD", tt.password)
			}

			url, err := GetConnURL(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("unexpected error. gotErr = %v, wantErr = %v", err != nil, tt.wantErr)
			}

			if err == nil && url == "" {
				t.Errorf("expected non-empty URL, got empty string")
			}
		})
	}
}

func TestValidateSuccess(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
	}{
		{
			name: "Valid config with Addr and Exchange",
			config: &Config{
				Addr:     "localhost:5672",
				Exchange: "ex",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.config); err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestValidateFailure(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
	}{
		{
			name:   "Nil config",
			config: nil,
		},
		{
			name:   "Missing Addr",
			config: &Config{Exchange: "ex"},
		},
		{
			name:   "Missing Exchange",
			config: &Config{Addr: "localhost:5672"},
		},
		{
			name:   "Empty Addr and Exchange",
			config: &Config{Addr: " ", Exchange: " "},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.config)
			if err == nil {
				t.Errorf("expected error for invalid config, got nil")
			}
		})
	}
}

type mockChannel struct {
	published bool
	args      amqp091.Publishing
	exchange  string
	key       string
	fail      bool
}

func (m *mockChannel) PublishWithContext(
	_ context.Context,
	exchange, key string,
	mandatory, immediate bool,
	msg amqp091.Publishing,
) error {
	if m.fail {
		return errors.New("mock publish failure")
	}
	m.published = true
	m.args = msg
	m.exchange = exchange
	m.key = key
	return nil
}

func TestPublishSuccess(t *testing.T) {
	mockCh := &mockChannel{}

	p := &Publisher{
		Channel: mockCh,
		Config: &Config{
			Exchange:   "mock.exchange",
			RoutingKey: "mock.key",
		},
	}

	err := p.Publish(context.Background(), "", []byte(`{"test": true}`))
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	if !mockCh.published {
		t.Error("expected message to be published, but it wasn't")
	}

	if mockCh.exchange != "mock.exchange" || mockCh.key != "mock.key" {
		t.Errorf("unexpected exchange or key. got (%s, %s)", mockCh.exchange, mockCh.key)
	}
}

func TestPublishFailure(t *testing.T) {
	mockCh := &mockChannel{fail: true}

	p := &Publisher{
		Channel: mockCh,
		Config: &Config{
			Exchange:   "mock.exchange",
			RoutingKey: "mock.key",
		},
	}

	err := p.Publish(context.Background(), "", []byte(`{"test": true}`))
	if err == nil {
		t.Error("expected error from failed publish, got nil")
	}
}
