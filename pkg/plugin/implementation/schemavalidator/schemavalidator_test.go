package schemavalidator

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// setupTestSchema creates a temporary directory and writes a sample schema file.
func setupTestSchema(t *testing.T) string {
	t.Helper()

	// Create a temporary directory for the schema
	schemaDir, err := os.MkdirTemp("", "schemas")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	// Create the directory structure for the schema file
	schemaFilePath := filepath.Join(schemaDir, "example", "v1.0", "endpoint.json")
	if err := os.MkdirAll(filepath.Dir(schemaFilePath), 0755); err != nil {
		t.Fatalf("Failed to create schema directory structure: %v", err)
	}

	// Define a sample schema
	schemaContent := `{
		"type": "object",
		"properties": {
			"context": {
				"type": "object",
				"properties": {
					"domain": {"type": "string"},
					"version": {"type": "string"},
					"action": {"type": "string"}
				},
				"required": ["domain", "version", "action"]
			}
		},
		"required": ["context"]
	}`

	// Write the schema to the file
	if err := os.WriteFile(schemaFilePath, []byte(schemaContent), 0644); err != nil {
		t.Fatalf("Failed to write schema file: %v", err)
	}

	return schemaDir
}

func TestValidator_Validate_Success(t *testing.T) {
	tests := []struct {
		name           string
		endpointAction string
		payload        string
		wantErr        bool
	}{
		{
			name:           "Valid payload",
			endpointAction: "endpoint",
			payload:        `{"context": {"domain": "example", "version": "1.0", "action": "endpoint"}}`,
			wantErr:        false,
		},
	}

	// Setup a temporary schema directory for testing
	schemaDir := setupTestSchema(t)
	defer os.RemoveAll(schemaDir)

	config := &Config{SchemaDir: schemaDir}
	v, _, err := New(context.Background(), config)
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(context.Background(), &url.URL{Path: tt.endpointAction}, []byte(tt.payload))
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			} else {
				t.Logf("Test %s passed with no errors", tt.name)
			}
		})
	}
}

func TestValidator_Validate_Failure(t *testing.T) {
	tests := []struct {
		name           string
		endpointAction string
		payload        string
		wantErr        string
	}{
		{
			name:           "Invalid JSON payload",
			endpointAction: "endpoint",
			payload:        `{"context": {"domain": "example", "version": "1.0"`,
			wantErr:        "failed to parse JSON payload",
		},
		{
			name:           "Schema validation failure",
			endpointAction: "endpoint",
			payload:        `{"context": {"domain": "example", "version": "1.0"}}`,
			wantErr:        "context: at '/context': missing property 'action'",
		},
		{
			name:           "Schema not found",
			endpointAction: "unknown_endpoint",
			payload:        `{"context": {"domain": "example", "version": "1.0"}}`,
			wantErr:        "schema not found for domain",
		},
	}

	// Setup a temporary schema directory for testing
	schemaDir := setupTestSchema(t)
	defer os.RemoveAll(schemaDir)

	config := &Config{SchemaDir: schemaDir}
	v, _, err := New(context.Background(), config)
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(context.Background(), &url.URL{Path: tt.endpointAction}, []byte(tt.payload))
			if tt.wantErr != "" {
				if err == nil {
					t.Errorf("Expected error containing '%s', but got nil", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Expected error containing '%s', but got '%v'", tt.wantErr, err)
				} else {
					t.Logf("Test %s passed with expected error: %v", tt.name, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				} else {
					t.Logf("Test %s passed with no errors", tt.name)
				}
			}
		})
	}
}

// TestValidator_Validate_SchemaErrorDetails confirms Validate() attaches
// Details with the JSON-schema cause's path through the real code path
// (extractSchemaErrors is inlined here, not a separately-testable helper).
// Note: setupTestSchema's schema only requires "context" at the root, so a
// root-level (empty-path) cause never occurs for THIS test's schema — that is
// not a general property of Validate() itself. Validate() never independently
// checks for a top-level "message" field before calling schema.Validate(), so
// a real schema requiring ["context", "message"] at root (as this plugin's
// own README documents) could still produce a root-level cause with no path.
func TestValidator_Validate_SchemaErrorDetails(t *testing.T) {
	schemaDir := setupTestSchema(t)
	defer os.RemoveAll(schemaDir)

	config := &Config{SchemaDir: schemaDir}
	v, _, err := New(context.Background(), config)
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	err = v.Validate(context.Background(), &url.URL{Path: "endpoint"}, []byte(`{"context": {"domain": "example", "version": "1.0"}}`))

	var schemaErr *model.SchemaValidationErr
	if !errors.As(err, &schemaErr) {
		t.Fatalf("expected *model.SchemaValidationErr, got %T: %v", err, err)
	}
	if len(schemaErr.Errors) == 0 {
		t.Fatal("expected at least one schema error")
	}

	hasDetails := false
	for _, got := range schemaErr.Errors {
		if got.Details != nil && got.Details.Path == "" {
			t.Errorf("Details = %+v, want either nil or a non-empty Path — never a non-nil Details with an empty Path", got.Details)
		}
		if got.Details != nil && got.Details.Path != "" {
			hasDetails = true
		}
	}
	if !hasDetails {
		t.Error("expected at least one schema error with a non-nil Details and a non-empty Path, got none")
	}
}

func TestValidator_Initialise(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(schemaDir string) error
		wantErr   string
	}{
		{
			name: "Schema directory does not exist",
			setupFunc: func(schemaDir string) error {
				// Do not create the schema directory
				return nil

			},
			wantErr: "schema directory does not exist",
		},
		{
			name: "Schema path is not a directory",
			setupFunc: func(schemaDir string) error {
				// Create a file instead of a directory
				return os.WriteFile(schemaDir, []byte{}, 0644)
			},
			wantErr: "provided schema path is not a directory",
		},
		{
			name: "Invalid schema file structure",
			setupFunc: func(schemaDir string) error {
				// Create an invalid schema file structure
				invalidSchemaFile := filepath.Join(schemaDir, "invalid_schema.json")
				if err := os.MkdirAll(filepath.Dir(invalidSchemaFile), 0755); err != nil {
					t.Fatalf("Failed to create directory: %v", err)
				}
				return os.WriteFile(invalidSchemaFile, []byte(`{}`), 0644)
			},
			wantErr: "invalid schema file structure",
		},
		{
			name: "Failed to compile JSON schema",
			setupFunc: func(schemaDir string) error {
				// Create a schema file with invalid JSON
				invalidSchemaFile := filepath.Join(schemaDir, "example", "1.0", "endpoint.json")
				if err := os.MkdirAll(filepath.Dir(invalidSchemaFile), 0755); err != nil {
					t.Fatalf("Failed to create directory: %v", err)
				}
				return os.WriteFile(invalidSchemaFile, []byte(`{invalid json}`), 0644)
			},
			wantErr: "failed to compile JSON schema",
		},
		{
			name: "Invalid schema file structure with empty components",
			setupFunc: func(schemaDir string) error {
				// Create a schema file with empty domain, version, or schema name
				invalidSchemaFile := filepath.Join(schemaDir, "", "1.0", "endpoint.json")
				if err := os.MkdirAll(filepath.Dir(invalidSchemaFile), 0755); err != nil {
					t.Fatalf("Failed to create directory: %v", err)
				}
				return os.WriteFile(invalidSchemaFile, []byte(`{
					"type": "object",
					"properties": {
						"context": {
							"type": "object",
							"properties": {
								"domain": {"type": "string"},
								"version": {"type": "string"}
							},
							"required": ["domain", "version"]
						}
					},
					"required": ["context"]
				}`), 0644)
			},
			wantErr: "failed to read schema directory: invalid schema file structure, expected domain/version/schema.json but got: 1.0/endpoint.json",
		},
		{
			name: "Failed to read directory",
			setupFunc: func(schemaDir string) error {
				// Create a directory and remove read permissions
				if err := os.MkdirAll(schemaDir, 0000); err != nil {
					t.Fatalf("Failed to create directory: %v", err)
				}
				return nil
			},
			wantErr: "failed to read directory",
		},
		{
			name: "Valid schema directory",
			setupFunc: func(schemaDir string) error {
				// Create a valid schema file
				validSchemaFile := filepath.Join(schemaDir, "example", "1.0", "endpoint.json")
				if err := os.MkdirAll(filepath.Dir(validSchemaFile), 0755); err != nil {
					t.Fatalf("Failed to create directory: %v", err)
				}
				return os.WriteFile(validSchemaFile, []byte(`{
					"type": "object",
					"properties": {
						"context": {
							"type": "object",
							"properties": {
								"domain": {"type": "string"},
								"version": {"type": "string"}
							},
							"required": ["domain", "version"]
						}
					},
					"required": ["context"]
				}`), 0644)
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup a temporary schema directory for testing
			schemaDir := filepath.Join(os.TempDir(), "schemas")
			defer os.RemoveAll(schemaDir)

			// Run the setup function to prepare the test case
			if err := tt.setupFunc(schemaDir); err != nil {
				t.Fatalf("setupFunc() error = %v", err)
			}

			config := &Config{SchemaDir: schemaDir}
			v := &schemaValidator{
				config:      config,
				schemaCache: make(map[string]*jsonschema.Schema),
			}

			err := v.initialise()
			if (err != nil && !strings.Contains(err.Error(), tt.wantErr)) || (err == nil && tt.wantErr != "") {
				t.Errorf("Error: initialise() returned error = %v, expected error = %v", err, tt.wantErr)
			} else if err == nil {
				t.Logf("Test %s passed: validator initialized successfully", tt.name)
			} else {
				t.Logf("Test %s passed with expected error: %v", tt.name, err)
			}
		})
	}
}

func TestValidatorNew_Success(t *testing.T) {
	schemaDir := setupTestSchema(t)
	defer os.RemoveAll(schemaDir)

	config := &Config{SchemaDir: schemaDir}
	_, _, err := New(context.Background(), config)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestValidatorNewFailure(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
		setupFunc func(schemaDir string) error
		wantErr   string
	}{
		{
			name:   "Config is nil",
			config: nil,
			setupFunc: func(schemaDir string) error {
				return nil
			},
			wantErr: "config cannot be nil",
		},
		{
			name: "Failed to initialise validators",
			config: &Config{
				SchemaDir: "/invalid/path",
			},
			setupFunc: func(schemaDir string) error {
				// Do not create the schema directory
				return nil
			},
			wantErr: "ailed to initialise schemaValidator: schema directory does not exist: /invalid/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run the setup function if provided
			if tt.setupFunc != nil {
				schemaDir := ""
				if tt.config != nil {
					schemaDir = tt.config.SchemaDir
				}
				if err := tt.setupFunc(schemaDir); err != nil {
					t.Fatalf("Setup function failed: %v", err)
				}
			}

			// Call the New function with the test config
			_, _, err := New(context.Background(), tt.config)
			if (err != nil && !strings.Contains(err.Error(), tt.wantErr)) || (err == nil && tt.wantErr != "") {
				t.Errorf("Error: New() returned error = %v, expected error = %v", err, tt.wantErr)
			} else {
				t.Logf("Test %s passed with expected error: %v", tt.name, err)
			}
		})
	}
}
