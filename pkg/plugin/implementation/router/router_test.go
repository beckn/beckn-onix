package router

import (
	"context"
	"embed"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed testData/*
var testData embed.FS

func setupTestConfig(t *testing.T, yamlFileName string) string {
	t.Helper()

	// Create a temporary directory for the routing rules
	configDir, err := os.MkdirTemp("", "routing_rules")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	// Read the YAML file content
	yamlContent := readYAMLFile(t, yamlFileName)

	// Write the routing rules to a file
	rulesFilePath := filepath.Join(configDir, "routing_rules.yaml")
	if err := os.WriteFile(rulesFilePath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("Failed to write routing rules file: %v", err)
	}

	return rulesFilePath
}

func readYAMLFile(t *testing.T, fileName string) string {
	t.Helper()

	// Read the YAML file
	content, err := testData.ReadFile("testData/" + fileName)
	if err != nil {
		t.Fatalf("Failed to read YAML file: %v", err)
	}

	return string(content)
}

// setupRouter is a helper function to create router instance.
func setupRouter(t *testing.T, configFile string) (*Router, func() error, string) {
	rulesFilePath := setupTestConfig(t, configFile)
	config := &Config{
		RoutingConfig: rulesFilePath,
	}
	router, _, err := New(context.Background(), config)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return router, nil, rulesFilePath
}

// TestNew tests the New function.
func TestNew(t *testing.T) {
	ctx := context.Background()

	// List of YAML files in the testData directory
	yamlFiles := []string{
		"bap_caller.yaml",
		"bap_receiver.yaml",
		"bpp_caller.yaml",
		"bpp_receiver.yaml",
	}

	for _, yamlFile := range yamlFiles {
		t.Run(yamlFile, func(t *testing.T) {
			rulesFilePath := setupTestConfig(t, yamlFile)
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
					expectedError: "routingConfig path is empty",
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
					router, _, err := New(ctx, tt.config)

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
				})
			}
		})
	}
}

// TestValidateRulesSuccess tests the validate function for success cases.
func TestValidateRulesSuccess(t *testing.T) {
	tests := []struct {
		name  string
		rules []routingRule
	}{
		{
			name: "Valid rules with url routing",
			rules: []routingRule{
				{
					Domain:      "retail",
					Version:     "1.0.0",
					RoutingType: "url",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"on_search", "on_select"},
				},
			},
		},
		{
			name: "Valid rules with msgq routing",
			rules: []routingRule{
				{
					Domain:      "retail",
					Version:     "1.0.0",
					RoutingType: "msgq",
					Target: target{
						TopicID: "example_topic",
					},
					Endpoints: []string{"on_search", "on_select"},
				},
			},
		},
		{
			name: "Valid rules with bpp routing to gateway",
			rules: []routingRule{
				{
					Domain:      "retail",
					Version:     "1.0.0",
					RoutingType: "bpp",
					Target: target{
						URL: "https://mock_gateway.com/api",
					},
					Endpoints: []string{"search"},
				},
			},
		},
		{
			name: "Valid rules with bpp routing",
			rules: []routingRule{
				{
					Domain:      "retail",
					Version:     "1.0.0",
					RoutingType: "bpp",
					Endpoints:   []string{"select"},
				},
			},
		},
		{
			name: "Valid rules with bap routing",
			rules: []routingRule{
				{
					Domain:      "retail",
					Version:     "1.0.0",
					RoutingType: "bap",
					Endpoints:   []string{"select"},
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

// TestValidateRulesFailure tests the validate function for failure cases.
func TestValidateRulesFailure(t *testing.T) {
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
			expectedErr: "invalid rule: domain, version, and routingType are required",
		},
		{
			name: "Missing version",
			rules: []routingRule{
				{
					Domain:      "retail",
					RoutingType: "url",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"search", "select"},
				},
			},
			expectedErr: "invalid rule: domain, version, and routingType are required",
		},
		{
			name: "Missing routingType",
			rules: []routingRule{
				{
					Domain:  "retail",
					Version: "1.0.0",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"search", "select"},
				},
			},
			expectedErr: "invalid rule: domain, version, and routingType are required",
		},
		{
			name: "Invalid routingType",
			rules: []routingRule{
				{
					Domain:      "retail",
					Version:     "1.0.0",
					RoutingType: "invalid",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"search", "select"},
				},
			},
			expectedErr: "invalid rule: unknown routingType 'invalid'",
		},
		{
			name: "Missing url for routingType: url",
			rules: []routingRule{
				{
					Domain:      "retail",
					Version:     "1.0.0",
					RoutingType: "url",
					Target:      target{
						// URL is missing
					},
					Endpoints: []string{"search", "select"},
				},
			},
			expectedErr: "invalid rule: url is required for routingType 'url'",
		},
		{
			name: "Missing topic_id for routingType: msgq",
			rules: []routingRule{
				{
					Domain:      "retail",
					Version:     "1.0.0",
					RoutingType: "msgq",
					Target:      target{
						// TopicID is missing
					},
					Endpoints: []string{"search", "select"},
				},
			},
			expectedErr: "invalid rule: topicId is required for routingType 'msgq'",
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

// TestRouteSuccess tests the Route function for success cases.
func TestRouteSuccess(t *testing.T) {
	ctx := context.Background()

	// Define success test cases
	tests := []struct {
		name       string
		configFile string
		url        string
		body       string
	}{
		{
			name:       "Valid domain, version, and endpoint (bpp routing with gateway URL)",
			configFile: "bap_caller.yaml",
			url:        "https://example.com/v1/ondc/search",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "2.0.0"}}`,
		},
		{
			name:       "Valid domain, version, and endpoint (bpp routing with bpp_uri)",
			configFile: "bap_caller.yaml",
			url:        "https://example.com/v1/ondc/select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "2.0.0", "bpp_uri": "https://bpp1.example.com"}}`,
		},
		{
			name:       "Valid domain, version, and endpoint (url routing)",
			configFile: "bpp_receiver.yaml",
			url:        "https://example.com/v1/ondc/select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "2.0.0"}}`,
		},
		{
			name:       "Valid domain, version, and endpoint (msgq routing)",
			configFile: "bpp_receiver.yaml",
			url:        "https://example.com/v1/ondc/search",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "2.0.0"}}`,
		},
		{
			name:       "Valid domain, version, and endpoint (bap routing with bap_uri)",
			configFile: "bpp_caller.yaml",
			url:        "https://example.com/v1/ondc/on_select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "2.0.0", "bap_uri": "https://bap1.example.com"}}`,
		},
		{
			name:       "Valid domain, version, and endpoint (bpp routing with bpp_uri)",
			configFile: "bap_receiver.yaml",
			url:        "https://example.com/v1/ondc/on_select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "2.0.0", "bpp_uri": "https://bpp1.example.com"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _, rulesFilePath := setupRouter(t, tt.configFile)
			defer os.RemoveAll(filepath.Dir(rulesFilePath))

			parsedURL, _ := url.Parse(tt.url)
			_, err := router.Route(ctx, parsedURL, []byte(tt.body))

			// Ensure no error occurred
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestRouteFailure tests the Route function for failure cases.
func TestRouteFailure(t *testing.T) {
	ctx := context.Background()

	// Define failure test cases
	tests := []struct {
		name          string
		configFile    string
		url           string
		body          string
		expectedError string
	}{
		{
			name:          "Unsupported endpoint",
			configFile:    "bpp_receiver.yaml",
			url:           "https://example.com/v1/ondc/unsupported",
			body:          `{"context": {"domain": "ONDC:TRV11", "version": "2.0.0"}}`,
			expectedError: "endpoint 'unsupported' is not supported for domain ONDC:TRV11 and version 2.0.0",
		},
		{
			name:          "No matching rule",
			configFile:    "bpp_receiver.yaml",
			url:           "https://example.com/v1/ondc/select",
			body:          `{"context": {"domain": "ONDC:SRV11", "version": "2.0.0"}}`,
			expectedError: "no matching routing rule found for domain ONDC:SRV11 and version 2.0.0",
		},
		{
			name:          "Missing bap_uri for bap routing",
			configFile:    "bpp_caller.yaml",
			url:           "https://example.com/v1/ondc/on_search",
			body:          `{"context": {"domain": "ONDC:TRV10", "version": "2.0.0"}}`,
			expectedError: "no target URI or URL found for bap routing type and on_search endpoint",
		},
		{
			name:          "Missing bpp_uri for bpp routing",
			configFile:    "bap_caller.yaml",
			url:           "https://example.com/v1/ondc/select",
			body:          `{"context": {"domain": "ONDC:TRV10", "version": "2.0.0"}}`,
			expectedError: "no target URI or URL found for bpp routing type and select endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _, rulesFilePath := setupRouter(t, tt.configFile)
			defer os.RemoveAll(filepath.Dir(rulesFilePath))

			parsedURL, _ := url.Parse(tt.url)
			_, err := router.Route(ctx, parsedURL, []byte(tt.body))

			// Check for expected error
			if err == nil || !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("expected error %q, got %v", tt.expectedError, err)
			}
		})
	}
}
