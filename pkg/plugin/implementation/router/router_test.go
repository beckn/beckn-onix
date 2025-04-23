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
	configDir := t.TempDir()

	content, err := testData.ReadFile("testData/" + yamlFileName)
	if err != nil {
		t.Fatalf("ReadFile() err = %v, want nil", err)
	}

	rulesPath := filepath.Join(configDir, "routing_rules.yaml")
	if err := os.WriteFile(rulesPath, content, 0644); err != nil {
		t.Fatalf("WriteFile() err = %v, want nil", err)
	}

	return rulesPath
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
				name    string
				config  *Config
				wantErr string
			}{
				{
					name: "Valid configuration",
					config: &Config{
						RoutingConfig: rulesFilePath,
					},
					wantErr: "",
				},
				{
					name:    "Empty config",
					config:  nil,
					wantErr: "config cannot be nil",
				},
				{
					name: "Empty routing config path",
					config: &Config{
						RoutingConfig: "",
					},
					wantErr: "routingConfig path is empty",
				},
				{
					name: "Routing config file does not exist",
					config: &Config{
						RoutingConfig: "/nonexistent/path/to/rules.yaml",
					},
					wantErr: "error reading config file",
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					router, _, err := New(ctx, tt.config)

					// Check for expected error
					if tt.wantErr != "" {
						if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
							t.Errorf("New(%v) = %v, want error containing %q", tt.config, err, tt.wantErr)
						}
						return
					}

					// Ensure no error occurred
					if err != nil {
						t.Errorf("New(%v) = %v, want nil error", tt.config, err)
						return
					}

					// Ensure the router and close function are not nil
					if router == nil {
						t.Errorf("New(%v, %v) = nil router, want non-nil", ctx, tt.config)
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
					Domain:     "retail",
					Version:    "1.0.0",
					TargetType: "url",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"on_search", "on_select"},
				},
			},
		},
		{
			name: "Valid rules with publisher routing",
			rules: []routingRule{
				{
					Domain:     "retail",
					Version:    "1.0.0",
					TargetType: "publisher",
					Target: target{
						PublisherID: "example_topic",
					},
					Endpoints: []string{"on_search", "on_select"},
				},
			},
		},
		{
			name: "Valid rules with bpp routing to gateway",
			rules: []routingRule{
				{
					Domain:     "retail",
					Version:    "1.0.0",
					TargetType: "bpp",
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
					Domain:     "retail",
					Version:    "1.0.0",
					TargetType: "bpp",
					Endpoints:  []string{"select"},
				},
			},
		},
		{
			name: "Valid rules with bap routing",
			rules: []routingRule{
				{
					Domain:     "retail",
					Version:    "1.0.0",
					TargetType: "bap",
					Endpoints:  []string{"select"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRules(tt.rules)
			if err != nil {
				t.Errorf("validateRules(%v) = %v, want nil error", tt.rules, err)
			}
		})
	}
}

// TestValidateRulesFailure tests the validate function for failure cases.
func TestValidateRulesFailure(t *testing.T) {
	tests := []struct {
		name    string
		rules   []routingRule
		wantErr string
	}{
		{
			name: "Missing domain",
			rules: []routingRule{
				{
					Version:    "1.0.0",
					TargetType: "url",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"search", "select"},
				},
			},
			wantErr: "invalid rule: domain, version, and targetType are required",
		},
		{
			name: "Missing version",
			rules: []routingRule{
				{
					Domain:     "retail",
					TargetType: "url",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"search", "select"},
				},
			},
			wantErr: "invalid rule: domain, version, and targetType are required",
		},
		{
			name: "Missing targetType",
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
			wantErr: "invalid rule: domain, version, and targetType are required",
		},
		{
			name: "Invalid targetType",
			rules: []routingRule{
				{
					Domain:     "retail",
					Version:    "1.0.0",
					TargetType: "invalid",
					Target: target{
						URL: "https://example.com/api",
					},
					Endpoints: []string{"search", "select"},
				},
			},
			wantErr: "invalid rule: unknown targetType 'invalid'",
		},
		{
			name: "Missing url for targetType: url",
			rules: []routingRule{
				{
					Domain:     "retail",
					Version:    "1.0.0",
					TargetType: "url",
					Target:     target{
						// URL is missing
					},
					Endpoints: []string{"search", "select"},
				},
			},
			wantErr: "invalid rule: url is required for targetType 'url'",
		},
		{
			name: "Invalid URL format for targetType: url",
			rules: []routingRule{
				{
					Domain:     "retail",
					Version:    "1.0.0",
					TargetType: "url",
					Target: target{
						URL: "htp:// invalid-url.com", // Invalid scheme
					},
					Endpoints: []string{"search"},
				},
			},
			wantErr: `invalid URL - htp:// invalid-url.com: parse "htp:// invalid-url.com": invalid character " " in host name`,
		},
		{
			name: "Missing topic_id for targetType: publisher",
			rules: []routingRule{
				{
					Domain:     "retail",
					Version:    "1.0.0",
					TargetType: "publisher",
					Target:     target{
						// PublisherID is missing
					},
					Endpoints: []string{"search", "select"},
				},
			},
			wantErr: "invalid rule: publisherID is required for targetType 'publisher'",
		},
		{
			name: "Invalid URL for BPP targetType",
			rules: []routingRule{
				{
					Domain:     "retail",
					Version:    "1.0.0",
					TargetType: "bpp",
					Target: target{
						URL: "htp:// invalid-url.com", // Invalid URL
					},
					Endpoints: []string{"search"},
				},
			},
			wantErr: `invalid URL - htp:// invalid-url.com defined in routing config for target type bpp: parse "htp:// invalid-url.com": invalid character " " in host name`,
		},
		{
			name: "Invalid URL for BAP targetType",
			rules: []routingRule{
				{
					Domain:     "retail",
					Version:    "1.0.0",
					TargetType: "bap",
					Target: target{
						URL: "http:// [invalid].com", // Invalid host
					},
					Endpoints: []string{"search"},
				},
			},
			wantErr: `invalid URL - http:// [invalid].com defined in routing config for target type bap: parse "http:// [invalid].com": invalid character " " in host name`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRules(tt.rules)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validateRules(%v) = %v, want error containing %q", tt.rules, err, tt.wantErr)
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
			name:       "Valid domain, version, and endpoint (publisher routing)",
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
				t.Errorf("router.Route(%v, %v, %v) = %v, want nil error", ctx, parsedURL, []byte(tt.body), err)
			}
		})
	}
}

// TestRouteFailure tests the Route function for failure cases.
func TestRouteFailure(t *testing.T) {
	ctx := context.Background()

	// Define failure test cases
	tests := []struct {
		name       string
		configFile string
		url        string
		body       string
		wantErr    string
	}{
		{
			name:       "Unsupported endpoint",
			configFile: "bpp_receiver.yaml",
			url:        "https://example.com/v1/ondc/unsupported",
			body:       `{"context": {"domain": "ONDC:TRV11", "version": "2.0.0"}}`,
			wantErr:    "endpoint 'unsupported' is not supported for domain ONDC:TRV11 and version 2.0.0",
		},
		{
			name:       "No matching rule",
			configFile: "bpp_receiver.yaml",
			url:        "https://example.com/v1/ondc/select",
			body:       `{"context": {"domain": "ONDC:SRV11", "version": "2.0.0"}}`,
			wantErr:    "no routing rules found for domain ONDC:SRV11",
		},
		{
			name:       "Missing bap_uri for bap routing",
			configFile: "bpp_caller.yaml",
			url:        "https://example.com/v1/ondc/on_search",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "2.0.0"}}`,
			wantErr:    "could not determine destination for endpoint 'on_search': neither request contained a BAP URI nor was a default URL configured in routing rules",
		},
		{
			name:       "Missing bpp_uri for bpp routing",
			configFile: "bap_caller.yaml",
			url:        "https://example.com/v1/ondc/select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "2.0.0"}}`,
			wantErr:    "could not determine destination for endpoint 'select': neither request contained a BPP URI nor was a default URL configured in routing rules",
		},
		{
			name:       "Invalid bpp_uri format in request",
			configFile: "bap_caller.yaml",
			url:        "https://example.com/v1/ondc/select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "2.0.0", "bpp_uri": "htp:// invalid-url"}}`, // Invalid scheme (htp instead of http)
			wantErr:    `invalid BPP URI - htp:// invalid-url in request body for select: parse "htp:// invalid-url": invalid character " " in host name`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _, rulesFilePath := setupRouter(t, tt.configFile)
			defer os.RemoveAll(filepath.Dir(rulesFilePath))

			parsedURL, _ := url.Parse(tt.url)
			_, err := router.Route(ctx, parsedURL, []byte(tt.body))

			// Check for expected error
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Route(%q, %q) = %v, want error containing %q", tt.url, tt.body, err, tt.wantErr)
			}
		})
	}
}
