package router

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestConfig creates a temporary directory and writes a sample routing rules file.
func setupTestConfig(t *testing.T) string {
	t.Helper()

	// Create a temporary directory for the routing rules
	configDir, err := os.MkdirTemp("", "routing_rules")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	// Define sample routing rules
	rulesContent := `
routing_rules:
  - domain: "ONDC:TRV11"
    version: "2.0.0"
    routing_type: "url"
    target:
      url: "https://services-backend/trv/v1"
    endpoints:
      - select
      - init
      - confirm
      - status

  - domain: "ONDC:TRV11"
    version: "2.0.0"
    routing_type: "msgq"
    target:
      topic_id: "trv_topic_id1"
    endpoints:
      - search
`

	// Write the routing rules to a file
	rulesFilePath := filepath.Join(configDir, "routing_rules.yaml")
	if err := os.WriteFile(rulesFilePath, []byte(rulesContent), 0644); err != nil {
		t.Fatalf("Failed to write routing rules file: %v", err)
	}

	return rulesFilePath
}

// TestNew tests the New function.
func TestNew(t *testing.T) {
	ctx := context.Background()
	rulesFilePath := setupTestConfig(t)
	defer os.RemoveAll(filepath.Dir(rulesFilePath))

	// Define test cases
	tests := []struct {
		name          string
		config        *Config
		expectedError string
	}{
		{
			name: "Valid configuration",
			config: &Config{
				RoutingConfig: rulesFilePath,
			},
			expectedError: "",
		},
		{
			name:          "Empty config",
			config:        nil,
			expectedError: "config cannot be nil",
		},
		{
			name: "Empty routing config path",
			config: &Config{
				RoutingConfig: "",
			},
			expectedError: "routing_config path is empty",
		},
		{
			name: "Routing config file does not exist",
			config: &Config{
				RoutingConfig: "/nonexistent/path/to/rules.yaml",
			},
			expectedError: "error reading config file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, closeFunc, err := New(ctx, tt.config)

			// Check for expected error
			if tt.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error %q, got %v", tt.expectedError, err)
				}
				return
			}

			// Ensure no error occurred
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Ensure the router and close function are not nil
			if router == nil {
				t.Error("expected a non-nil Router instance, got nil")
			}
			if closeFunc == nil {
				t.Error("expected a non-nil close function, got nil")
			}

			// Test the close function
			if err := closeFunc(); err != nil {
				t.Errorf("close function returned an error: %v", err)
			}
		})
	}
}

// TestValidateRules_Success tests the validate function for success cases.
func TestValidateRules_Success(t *testing.T) {
	tests := []struct {
		name  string
		rules []routingRule
	}{
		{
			name: "Valid rules with url routing",
			rules: []routingRule{
				{
					Domain:      "example.com",
					Version:     "1.0.0",
					RoutingType: "url",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"search", "select"},
				},
			},
		},
		{
			name: "Valid rules with msgq routing",
			rules: []routingRule{
				{
					Domain:      "example.com",
					Version:     "1.0.0",
					RoutingType: "msgq",
					Target: target{
						TopicID: "example_topic",
					},
					Endpoints: []string{"search", "select"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRules(tt.rules)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestValidateRules_Failure tests the validate function for failure cases.
func TestValidateRules_Failure(t *testing.T) {
	tests := []struct {
		name        string
		rules       []routingRule
		expectedErr string
	}{
		{
			name: "Missing domain",
			rules: []routingRule{
				{
					Version:     "1.0.0",
					RoutingType: "url",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"search", "select"},
				},
			},
			expectedErr: "invalid rule: domain, version, and routing_type are required",
		},
		{
			name: "Missing version",
			rules: []routingRule{
				{
					Domain:      "example.com",
					RoutingType: "url",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"search", "select"},
				},
			},
			expectedErr: "invalid rule: domain, version, and routing_type are required",
		},
		{
			name: "Missing routing_type",
			rules: []routingRule{
				{
					Domain:  "example.com",
					Version: "1.0.0",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"search", "select"},
				},
			},
			expectedErr: "invalid rule: domain, version, and routing_type are required",
		},
		{
			name: "Invalid routing_type",
			rules: []routingRule{
				{
					Domain:      "example.com",
					Version:     "1.0.0",
					RoutingType: "invalid_type",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"search", "select"},
				},
			},
			expectedErr: "invalid rule: unknown routing_type 'invalid_type'",
		},
		{
			name: "Missing url for routing_type: url",
			rules: []routingRule{
				{
					Domain:      "example.com",
					Version:     "1.0.0",
					RoutingType: "url",
					Target:      target{
						// URL is missing
					},
					Endpoints: []string{"search", "select"},
				},
			},
			expectedErr: "invalid rule: url is required for routing_type 'url'",
		},
		{
			name: "Missing topic_id for routing_type: msgq",
			rules: []routingRule{
				{
					Domain:      "example.com",
					Version:     "1.0.0",
					RoutingType: "msgq",
					Target:      target{
						// TopicID is missing
					},
					Endpoints: []string{"search", "select"},
				},
			},
			expectedErr: "invalid rule: topic_id is required for routing_type 'msgq'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRules(tt.rules)
			if err == nil {
				t.Errorf("expected error: %v, got nil", tt.expectedErr)
			} else if err.Error() != tt.expectedErr {
				t.Errorf("expected error: %v, got: %v", tt.expectedErr, err)
			}
		})
	}
}

// TestRoute tests the Route function.
func TestRoute(t *testing.T) {
	ctx := context.Background()
	rulesFilePath := setupTestConfig(t)
	defer os.RemoveAll(filepath.Dir(rulesFilePath))

	config := &Config{
		RoutingConfig: rulesFilePath,
	}

	router, closeFunc, err := New(ctx, config)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer func() {
		if err := closeFunc(); err != nil {
			t.Errorf("closeFunc failed: %v", err)
		}
	}()

	// Define test cases
	tests := []struct {
		name          string
		url           string
		body          string
		expectedError string
	}{
		{
			name: "Valid domain, version, and endpoint",
			url:  "https://example.com/v1/ondc/select",
			body: `{"context": {"domain": "ONDC:TRV11", "version": "2.0.0"}}`,
		},
		{
			name:          "Unsupported endpoint",
			url:           "https://example.com/v1/ondc/unsupported",
			body:          `{"context": {"domain": "ONDC:TRV11", "version": "2.0.0"}}`,
			expectedError: "endpoint 'unsupported' is not supported for domain ONDC:TRV11 and version 2.0.0",
		},
		{
			name:          "No matching rule",
			url:           "https://example.com/v1/ondc/select",
			body:          `{"context": {"domain": "ONDC:SRV11", "version": "2.0.0"}}`,
			expectedError: "no matching routing rule found for domain ONDC:SRV11 and version 2.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsedURL, _ := url.Parse(tt.url)
			_, err := router.Route(ctx, parsedURL, []byte(tt.body))

			// Check for expected error
			if tt.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error %q, got %v", tt.expectedError, err)
				}
				return
			}

			// Ensure no error occurred
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
