package router

import (
	"context"
	"embed"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
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

	validConfigFile := "bap_caller.yaml"
	rulesFilePath := setupTestConfig(t, validConfigFile)
	defer os.RemoveAll(filepath.Dir(rulesFilePath))

	config := &Config{
		RoutingConfig: rulesFilePath,
	}

	router, _, err := New(ctx, config)
	if err != nil {
		t.Errorf("New(%v) = %v, want nil error", config, err)
		return
	}
	if router == nil {
		t.Errorf("New(%v) = nil router, want non-nil", config)
	}
	if len(router.rules) == 0 {
		t.Error("Expected router to have loaded rules, but rules map is empty")
	}
}

// TestNewErrors tests the New function for failure cases.
func TestNewErrors(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		config  *Config
		wantErr string
	}{
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _, err := New(ctx, tt.config)

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New(%v) = %v, want error containing %q", tt.config, err, tt.wantErr)
			}
			if router != nil {
				t.Errorf("New(%v) = %v, want nil router on error", tt.config, router)
			}
		})
	}
}

// TestLoadRules tests the loadRules function for successful loading and map construction.
func TestLoadRules(t *testing.T) {
	router := &Router{
		rules: make(map[string]map[string]map[string]*model.Route),
	}
	rulesFilePath := setupTestConfig(t, "valid_all_routes.yaml")
	defer os.RemoveAll(filepath.Dir(rulesFilePath))

	err := router.loadRules(rulesFilePath)
	if err != nil {
		t.Fatalf("loadRules() err = %v, want nil", err)
	}

	// Expected router.rules map structure based on the yaml.
	expectedRules := map[string]map[string]map[string]*model.Route{
		"ONDC:TRV10": {
			"1.1.0": {
				"search":    {TargetType: targetTypeURL, URL: parseURL(t, "https://mock_gateway.com/v2/ondc/search")},
				"init":      {TargetType: targetTypeBAP, URL: parseURL(t, "https://mock_bpp.com/v2/ondc/init")},
				"select":    {TargetType: targetTypeBAP, URL: parseURL(t, "https://mock_bpp.com/v2/ondc/select")},
				"on_search": {TargetType: targetTypeBAP, URL: parseURL(t, "https://mock_bap_gateway.com/v2/ondc/on_search")},
				"confirm":   {TargetType: targetTypePublisher, PublisherID: "beckn_onix_topic", URL: nil},
			},
		},
	}

	if !reflect.DeepEqual(router.rules, expectedRules) {
		t.Errorf("Loaded rules mismatch.\nGot:\n%#v\nWant:\n%#v", router.rules, expectedRules)
	}
}

// mustParseURL is a helper for TestLoadRules to parse URLs.
func parseURL(t *testing.T, rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("Failed to parse URL %s: %v", rawURL, err)
	}
	return u
}

// TestLoadRulesErrors tests the loadRules function for various error cases.
func TestLoadRulesErrors(t *testing.T) {
	router := &Router{
		rules: make(map[string]map[string]map[string]*model.Route),
	}

	tests := []struct {
		name       string
		configPath string
		wantErr    string
	}{
		{
			name:       "Empty routing config path",
			configPath: "",
			wantErr:    "routingConfig path is empty",
		},
		{
			name:       "Routing config file does not exist",
			configPath: "/nonexistent/path/to/rules.yaml",
			wantErr:    "error reading config file",
		},
		{
			name:       "Invalid YAML (Unmarshal error)",
			configPath: setupTestConfig(t, "invalid_yaml.yaml"),
			wantErr:    "error parsing YAML",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.configPath, "/nonexistent/") && tt.configPath != "" {
				defer os.RemoveAll(filepath.Dir(tt.configPath))
			}

			err := router.loadRules(tt.configPath)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("loadRules(%q) = %v, want error containing %q", tt.configPath, err, tt.wantErr)
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
			wantErr: "invalid rule: domain is required for version 1.0.0",
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
			wantErr: "invalid rule: version and targetType are required",
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
			wantErr: "invalid rule: version and targetType are required",
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
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "1.1.0"}}`,
		},
		{
			name:       "Valid domain, version, and endpoint (bpp routing with bpp_uri)",
			configFile: "bap_caller.yaml",
			url:        "https://example.com/v1/ondc/select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "1.1.0", "bpp_uri": "https://bpp1.example.com"}}`,
		},
		{
			name:       "Valid domain, version, and endpoint (url routing)",
			configFile: "bpp_receiver.yaml",
			url:        "https://example.com/v1/ondc/select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "1.1.0"}}`,
		},
		{
			name:       "Valid domain, version, and endpoint (publisher routing)",
			configFile: "bpp_receiver.yaml",
			url:        "https://example.com/v1/ondc/search",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "1.1.0"}}`,
		},
		{
			name:       "Valid domain, version, and endpoint (bap routing with bap_uri)",
			configFile: "bpp_caller.yaml",
			url:        "https://example.com/v1/ondc/on_select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "1.1.0", "bap_uri": "https://bap1.example.com"}}`,
		},
		{
			name:       "Valid domain, version, and endpoint (bpp routing with bpp_uri)",
			configFile: "bap_receiver.yaml",
			url:        "https://example.com/v1/ondc/on_select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "1.1.0", "bpp_uri": "https://bpp1.example.com"}}`,
		},
		// camelCase variants (beckn spec camelCase migration)
		{
			name:       "camelCase: bppUri in context is resolved for bpp routing",
			configFile: "bap_caller.yaml",
			url:        "https://example.com/v1/ondc/select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "1.1.0", "bppUri": "https://bpp1.example.com"}}`,
		},
		{
			name:       "camelCase: bapUri in context is resolved for bap routing",
			configFile: "bpp_caller.yaml",
			url:        "https://example.com/v1/ondc/on_select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "1.1.0", "bapUri": "https://bap1.example.com"}}`,
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
			body:       `{"context": {"domain": "ONDC:TRV11", "version": "1.1.0"}}`,
			wantErr:    "endpoint 'unsupported' is not supported for domain ONDC:TRV11 and version 1.1.0",
		},
		{
			name:       "No matching rule",
			configFile: "bpp_receiver.yaml",
			url:        "https://example.com/v1/ondc/select",
			body:       `{"context": {"domain": "ONDC:SRV11", "version": "1.1.0"}}`,
			wantErr:    "no routing rules found for domain ONDC:SRV11",
		},
		{
			name:       "Missing bap_uri for bap routing",
			configFile: "bpp_caller.yaml",
			url:        "https://example.com/v1/ondc/on_search",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "1.1.0"}}`,
			wantErr:    "could not determine destination for endpoint 'on_search': neither request contained a BAP URI nor was a default URL configured in routing rules",
		},
		{
			name:       "Missing bpp_uri for bpp routing",
			configFile: "bap_caller.yaml",
			url:        "https://example.com/v1/ondc/select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "1.1.0"}}`,
			wantErr:    "could not determine destination for endpoint 'select': neither request contained a BPP URI nor was a default URL configured in routing rules",
		},
		{
			name:       "Invalid bpp_uri format in request",
			configFile: "bap_caller.yaml",
			url:        "https://example.com/v1/ondc/select",
			body:       `{"context": {"domain": "ONDC:TRV10", "version": "1.1.0", "bpp_uri": "htp:// invalid-url"}}`, // Invalid scheme (htp instead of http)
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

// TestExcludeAction tests the excludeAction feature for URL routing
func TestExcludeAction(t *testing.T) {
	tests := []struct {
		name           string
		configFile     string
		expectedRoutes map[string]map[string]map[string]*model.Route
	}{
		{
			name:       "excludeAction true - action not appended to URL",
			configFile: "exclude_action_true.yaml",
			expectedRoutes: map[string]map[string]map[string]*model.Route{
				"ONDC:TRV10": {
					"1.1.0": {
						"search": {TargetType: targetTypeURL, URL: parseURL(t, "https://services-backend.com/v2/ondc")},
						"init":   {TargetType: targetTypeURL, URL: parseURL(t, "https://services-backend.com/v2/ondc")},
					},
				},
			},
		},
		{
			name:       "excludeAction false - action appended to URL",
			configFile: "exclude_action_false.yaml",
			expectedRoutes: map[string]map[string]map[string]*model.Route{
				"ONDC:TRV10": {
					"1.1.0": {
						"search": {TargetType: targetTypeURL, URL: parseURL(t, "https://services-backend.com/v2/ondc/search")},
						"init":   {TargetType: targetTypeURL, URL: parseURL(t, "https://services-backend.com/v2/ondc/init")},
					},
				},
			},
		},
		{
			name:       "excludeAction not specified - defaults to false (action appended)",
			configFile: "exclude_action_default.yaml",
			expectedRoutes: map[string]map[string]map[string]*model.Route{
				"ONDC:TRV10": {
					"1.1.0": {
						"search": {TargetType: targetTypeURL, URL: parseURL(t, "https://services-backend.com/v2/ondc/search")},
						"init":   {TargetType: targetTypeURL, URL: parseURL(t, "https://services-backend.com/v2/ondc/init")},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := &Router{
				rules: make(map[string]map[string]map[string]*model.Route),
			}
			rulesFilePath := setupTestConfig(t, tt.configFile)
			defer os.RemoveAll(filepath.Dir(rulesFilePath))

			err := router.loadRules(rulesFilePath)
			if err != nil {
				t.Fatalf("loadRules() err = %v, want nil", err)
			}

			if !reflect.DeepEqual(router.rules, tt.expectedRoutes) {
				t.Errorf("Loaded rules mismatch for %s.\nGot:\n%#v\nWant:\n%#v", tt.name, router.rules, tt.expectedRoutes)
			}
		})
	}
}

// TestExcludeActionWithNonURLTargetTypes tests that excludeAction is ignored for non-URL target types
func TestExcludeActionWithNonURLTargetTypes(t *testing.T) {
	tests := []struct {
		name           string
		configFile     string
		expectedRoutes map[string]map[string]map[string]*model.Route
	}{
		{
			name:       "BPP target type - excludeAction should be ignored",
			configFile: "exclude_action_bpp.yaml",
			expectedRoutes: map[string]map[string]map[string]*model.Route{
				"ONDC:TRV10": {
					"1.1.0": {
						"search": {TargetType: targetTypeBPP, URL: parseURL(t, "https://mock_bpp.com/v2/ondc/search")},
						"init":   {TargetType: targetTypeBPP, URL: parseURL(t, "https://mock_bpp.com/v2/ondc/init")},
					},
				},
			},
		},
		{
			name:       "BAP target type - excludeAction should be ignored",
			configFile: "exclude_action_bap.yaml",
			expectedRoutes: map[string]map[string]map[string]*model.Route{
				"ONDC:TRV10": {
					"1.1.0": {
						"search": {TargetType: targetTypeBAP, URL: parseURL(t, "https://mock_bap.com/v2/ondc/search")},
						"init":   {TargetType: targetTypeBAP, URL: parseURL(t, "https://mock_bap.com/v2/ondc/init")},
					},
				},
			},
		},
		{
			name:       "Publisher target type - excludeAction should be ignored",
			configFile: "exclude_action_publisher.yaml",
			expectedRoutes: map[string]map[string]map[string]*model.Route{
				"ONDC:TRV10": {
					"1.1.0": {
						"search": {TargetType: targetTypePublisher, PublisherID: "test_topic", URL: nil},
						"init":   {TargetType: targetTypePublisher, PublisherID: "test_topic", URL: nil},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := &Router{
				rules: make(map[string]map[string]map[string]*model.Route),
			}
			rulesFilePath := setupTestConfig(t, tt.configFile)
			defer os.RemoveAll(filepath.Dir(rulesFilePath))

			err := router.loadRules(rulesFilePath)
			if err != nil {
				t.Fatalf("loadRules() err = %v, want nil", err)
			}

			if !reflect.DeepEqual(router.rules, tt.expectedRoutes) {
				t.Errorf("Loaded rules mismatch for %s.\nGot:\n%#v\nWant:\n%#v", tt.name, router.rules, tt.expectedRoutes)
			}
		})
	}
}

// TestV2RouteSuccess tests v2 routing with domain-agnostic behavior
func TestV2RouteSuccess(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		configFile string
		url        string
		body       string
	}{
		{
			name:       "v2 BAP caller - domain ignored",
			configFile: "v2_bap_caller.yaml",
			url:        "https://example.com/v2/search",
			body:       `{"context": {"domain": "any_domain", "version": "2.0.0"}}`,
		},
		{
			name:       "v2 BPP receiver - domain ignored",
			configFile: "v2_bpp_receiver.yaml",
			url:        "https://example.com/v2/select",
			body:       `{"context": {"domain": "different_domain", "version": "2.0.0"}}`,
		},
		{
			name:       "v2 request without domain field",
			configFile: "v2_bap_caller.yaml",
			url:        "https://example.com/v2/search",
			body:       `{"context": {"version": "2.0.0"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _, rulesFilePath := setupRouter(t, tt.configFile)
			defer os.RemoveAll(filepath.Dir(rulesFilePath))

			parsedURL, _ := url.Parse(tt.url)
			_, err := router.Route(ctx, parsedURL, []byte(tt.body))

			if err != nil {
				t.Errorf("router.Route() = %v, want nil (domain should be ignored for v2)", err)
			}
		})
	}
}

// TestV2ConflictingRules tests that conflicting v2 rules are detected at load time
func TestV2ConflictingRules(t *testing.T) {
	router := &Router{
		rules: make(map[string]map[string]map[string]*model.Route),
	}

	configDir := t.TempDir()
	conflictingConfig := `routingRules:
  - version: 2.0.0
    targetType: bap
    endpoints:
      - on_search
  - version: 2.0.0
    targetType: bap
    endpoints:
      - on_search
`
	rulesPath := filepath.Join(configDir, "conflicting_rules.yaml")
	if err := os.WriteFile(rulesPath, []byte(conflictingConfig), 0644); err != nil {
		t.Fatalf("WriteFile() err = %v, want nil", err)
	}
	defer os.RemoveAll(configDir)

	err := router.loadRules(rulesPath)
	if err == nil {
		t.Error("loadRules() with conflicting v2 rules should return error, got nil")
	}

	expectedErr := "duplicate endpoint 'on_search' found for version 2.0.0"
	if err != nil && !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("loadRules() error = %v, want error containing %q", err, expectedErr)
	}
}

// TestGetContextString tests the dual-key lookup helper used to support both
// snake_case (legacy) and camelCase (new beckn spec) context attribute names.
func TestGetContextString(t *testing.T) {
	tests := []struct {
		name      string
		ctx       map[string]interface{}
		snakeKey  string
		camelKey  string
		want      string
	}{
		{
			name:     "snake_case key present",
			ctx:      map[string]interface{}{"bpp_uri": "https://bpp.example.com"},
			snakeKey: "bpp_uri",
			camelKey: "bppUri",
			want:     "https://bpp.example.com",
		},
		{
			name:     "camelCase key present",
			ctx:      map[string]interface{}{"bppUri": "https://bpp.example.com"},
			snakeKey: "bpp_uri",
			camelKey: "bppUri",
			want:     "https://bpp.example.com",
		},
		{
			name:     "snake_case takes precedence when both present",
			ctx:      map[string]interface{}{"bpp_uri": "https://snake.example.com", "bppUri": "https://camel.example.com"},
			snakeKey: "bpp_uri",
			camelKey: "bppUri",
			want:     "https://snake.example.com",
		},
		{
			name:     "neither key present returns empty string",
			ctx:      map[string]interface{}{"domain": "ONDC:TRV10"},
			snakeKey: "bpp_uri",
			camelKey: "bppUri",
			want:     "",
		},
		{
			name:     "empty snake_case value falls through to camelCase",
			ctx:      map[string]interface{}{"bpp_uri": "", "bppUri": "https://bpp.example.com"},
			snakeKey: "bpp_uri",
			camelKey: "bppUri",
			want:     "https://bpp.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getContextString(tt.ctx, tt.snakeKey, tt.camelKey)
			if got != tt.want {
				t.Errorf("getContextString(%v, %q, %q) = %q, want %q", tt.ctx, tt.snakeKey, tt.camelKey, got, tt.want)
			}
		})
	}
}

// TestRouteNilContext tests that Route returns a clear error when the context
// field is absent from the request body.
func TestRouteNilContext(t *testing.T) {
	ctx := context.Background()
	router, _, rulesFilePath := setupRouter(t, "bap_caller.yaml")
	defer os.RemoveAll(filepath.Dir(rulesFilePath))

	parsedURL, _ := url.Parse("https://example.com/v1/ondc/select")
	_, err := router.Route(ctx, parsedURL, []byte(`{"message": {}}`))

	if err == nil || !strings.Contains(err.Error(), "context field not found or invalid") {
		t.Errorf("Route() with missing context = %v, want error containing 'context field not found or invalid'", err)
	}
}

// TestV1DomainRequired tests that domain is required for v1 configs
func TestV1DomainRequired(t *testing.T) {
	router := &Router{
		rules: make(map[string]map[string]map[string]*model.Route),
	}

	configDir := t.TempDir()
	v1ConfigWithoutDomain := `routingRules:
  - version: 1.0.0
    targetType: bap
    endpoints:
      - on_search
`
	rulesPath := filepath.Join(configDir, "v1_no_domain.yaml")
	if err := os.WriteFile(rulesPath, []byte(v1ConfigWithoutDomain), 0644); err != nil {
		t.Fatalf("WriteFile() err = %v, want nil", err)
	}
	defer os.RemoveAll(configDir)

	err := router.loadRules(rulesPath)
	if err == nil {
		t.Error("loadRules() with v1 config without domain should fail, got nil")
	}

	expectedErr := "invalid rule: domain is required for version 1.0.0"
	if err != nil && !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("loadRules() error = %v, want error containing %q", err, expectedErr)
	}
}
