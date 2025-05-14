package publisher

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/rabbitmq/amqp091-go"
)

func TestGetConnURLSuccess(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
	}{
		{
			name: "Valid config with connection address",
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
		{
			name: "Addr with leading and trailing spaces",
			config: &Config{
				Addr:   "  localhost:5672/myvhost  ",
				UseTLS: false,
			},
		},
	}

	// Set valid credentials
	t.Setenv("RABBITMQ_USERNAME", "guest")
	t.Setenv("RABBITMQ_PASSWORD", "guest")

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
			if tt.username != "" {
				t.Setenv("RABBITMQ_USERNAME", tt.username)
			}

			if tt.password != "" {
				t.Setenv("RABBITMQ_PASSWORD", tt.password)
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
		name         string
		config       *Config
		expectedErrr string
	}{
		{
			name:         "Nil config",
			config:       nil,
			expectedErrr: "config is nil",
		},
		{
			name:         "Missing Addr",
			config:       &Config{Exchange: "ex"},
			expectedErrr: "missing config.Addr",
		},
		{
			name:         "Missing Exchange",
			config:       &Config{Addr: "localhost:5672"},
			expectedErrr: "missing config.Exchange",
		},
		{
			name:         "Empty Addr and Exchange",
			config:       &Config{Addr: " ", Exchange: " "},
			expectedErrr: "missing config.Addr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.config)
			if err == nil {
				t.Errorf("expected error for invalid config, got nil")
				return
			}
			if !strings.Contains(err.Error(), tt.expectedErrr) {
				t.Errorf("expected error to contain %q, got: %v", tt.expectedErrr, err)
			}
		})
	}
}

type mockChannelForPublish struct {
	published bool
	exchange  string
	key       string
	body      []byte
	fail      bool
}

func (m *mockChannelForPublish) PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp091.Publishing) error {
	if m.fail {
		return fmt.Errorf("simulated publish failure")
	}
	m.published = true
	m.exchange = exchange
	m.key = key
	m.body = msg.Body
	return nil
}

func (m *mockChannelForPublish) ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp091.Table) error {
	return nil
}

func (m *mockChannelForPublish) Close() error {
	return nil
}

func TestPublishSuccess(t *testing.T) {
	mockCh := &mockChannelForPublish{}

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
	mockCh := &mockChannelForPublish{fail: true}

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

func TestNewPublisherSucess(t *testing.T) {
	originalDialFunc := DialFunc
	originalChannelFunc := ChannelFunc
	defer func() {
		DialFunc = originalDialFunc
		ChannelFunc = originalChannelFunc
	}()

	// mockedConn := &mockConnection{}

	DialFunc = func(url string) (*amqp091.Connection, error) {
		return nil, nil
	}

	ChannelFunc = func(conn *amqp091.Connection) (Channel, error) {
		return &mockChannel{}, nil
	}

	cfg := &Config{
		Addr:       "localhost",
		Exchange:   "test-ex",
		Durable:    true,
		RoutingKey: "test.key",
	}

	t.Setenv("RABBITMQ_USERNAME", "user")
	t.Setenv("RABBITMQ_PASSWORD", "pass")

	pub, cleanup, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if pub == nil {
		t.Fatal("Publisher should not be nil")
	}
	if cleanup == nil {
		t.Fatal("Cleanup should not be nil")
	}
	if err := cleanup(); err != nil {
		t.Errorf("Cleanup failed: %v", err)
	}
}

func TestNewPublisherFailures(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		dialFunc      func(url string) (*amqp091.Connection, error) // Mocked dial function
		envVars       map[string]string
		expectedError string
	}{
		{
			name:          "ValidateFailure",
			cfg:           &Config{}, // invalid config
			expectedError: "missing config.Addr",
		},
		{
			name: "GetConnURLFailure",
			cfg: &Config{
				Addr:       "localhost",
				Exchange:   "test-ex",
				Durable:    true,
				RoutingKey: "test.key",
			},
			envVars: map[string]string{
				"RABBITMQ_USERNAME": "",
				"RABBITMQ_PASSWORD": "",
			},
			expectedError: "missing RabbitMQ credentials in environment",
		},
		{
			name: "ConnectionFailure",
			cfg: &Config{
				Addr:       "localhost",
				Exchange:   "test-ex",
				Durable:    true,
				RoutingKey: "test.key",
			},
			dialFunc: func(url string) (*amqp091.Connection, error) {
				return nil, fmt.Errorf("simulated connection failure")
			},
			envVars: map[string]string{
				"RABBITMQ_USERNAME": "user",
				"RABBITMQ_PASSWORD": "pass",
			},
			expectedError: "failed to connect to RabbitMQ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			// Mock dialFunc if needed
			originalDialFunc := DialFunc
			if tt.dialFunc != nil {
				DialFunc = tt.dialFunc
				defer func() {
					DialFunc = originalDialFunc
				}()
			}

			_, _, err := New(tt.cfg)

			if err == nil || (tt.expectedError != "" && !strings.Contains(err.Error(), tt.expectedError)) {
				t.Errorf("Test %s failed: expected error containing %v, got: %v", tt.name, tt.expectedError, err)
			}
		})
	}
}
