package schemaValidator

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
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
		name      string
		url       string
		payload   string
		wantValid bool
	}{
		{
			name:      "Valid payload",
			url:       "http://example.com/endpoint",
			payload:   `{"context": {"domain": "example", "version": "1.0", "action": "endpoint"}}`,
			wantValid: true,
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
			u, _ := url.Parse(tt.url)
			valid, err := v.Validate(context.Background(), u, []byte(tt.payload))
			if err != (definition.SchemaValError{}) {
				t.Errorf("Unexpected error: %v", err)
			}
			if valid != tt.wantValid {
				t.Errorf("Error: Validate() returned valid = %v, expected valid = %v", valid, tt.wantValid)
			}
		})
	}
}

func TestValidator_Validate_Failure(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		payload   string
		wantValid bool
		wantErr   string
	}{
		{
			name:      "Invalid JSON payload",
			url:       "http://example.com/endpoint",
			payload:   `{"context": {"domain": "example", "version": "1.0"`,
			wantValid: false,
			wantErr:   "failed to parse JSON payload",
		},
		{
			name:      "Schema validation failure",
			url:       "http://example.com/endpoint",
			payload:   `{"context": {"domain": "example", "version": "1.0"}}`,
			wantValid: false,
			wantErr:   "Validation failed",
		},
		{
			name:      "Schema not found",
			url:       "http://example.com/unknown_endpoint",
			payload:   `{"context": {"domain": "example", "version": "1.0"}}`,
			wantValid: false,
			wantErr:   "schema not found for domain",
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
			u, _ := url.Parse(tt.url)
			valid, err := v.Validate(context.Background(), u, []byte(tt.payload))
			if (err != (definition.SchemaValError{}) && !strings.Contains(err.Message, tt.wantErr)) || (err == (definition.SchemaValError{}) && tt.wantErr != "") {
				t.Errorf("Error: Validate() returned error = %v, expected error = %v", err, tt.wantErr)
				return
			}
			if valid != tt.wantValid {
				t.Errorf("Validate() returned valid = %v, expected valid = %v", valid, tt.wantValid)
			}
		})
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
			v := &Validator{config: config}

			err := v.Initialise()
			if (err != nil && !strings.Contains(err.Error(), tt.wantErr)) || (err == nil && tt.wantErr != "") {
				t.Errorf("Error: Initialise() returned error = %v, expected error = %v", err, tt.wantErr)
			} else if err == nil {
				t.Logf("Test %s passed: validator initialized successfully", tt.name)
			} else {
				t.Logf("Test %s passed with expected error: %v", tt.name, err)
			}
		})
	}
}

func TestValidator_New_Success(t *testing.T) {
	schemaDir := setupTestSchema(t)
	defer os.RemoveAll(schemaDir)

	config := &Config{SchemaDir: schemaDir}
	_, _, err := New(context.Background(), config)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestValidator_New_Failure(t *testing.T) {
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
			name:   "Config is empty",
			config: &Config{},
			setupFunc: func(schemaDir string) error {
				return nil
			},
			wantErr: "config must contain 'schema_dir'",
		},
		{
			name:   "schema_dir is empty",
			config: &Config{SchemaDir: ""},
			setupFunc: func(schemaDir string) error {
				return nil
			},
			wantErr: "config must contain 'schema_dir'",
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
			wantErr: "failed to initialise validator",
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
