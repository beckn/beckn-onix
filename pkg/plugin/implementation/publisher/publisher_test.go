package publisher

import (
	"os"
	"testing"
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
