package validator

import (
	"beckn-onix/shared/plugin/definition"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidator_Validate(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		payload   string
		wantValid bool
		wantErr   string
	}{
		{
			name:      "Valid payload",
			url:       "http://example.com/endpoint",
			payload:   `{"context": {"domain": "example", "version": "1.0"}}`,
			wantValid: true,
			wantErr:   "",
		},
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
			payload:   `{"context": {"domain": "invalid", "version": "1.0"}}`,
			wantValid: false,
			wantErr:   "Validation failed",
		},
	}

	// Setup a temporary schema directory for testing
	schemaDir := filepath.Join(os.TempDir(), "schemas")
	defer os.RemoveAll(schemaDir)
	os.MkdirAll(schemaDir, 0755)

	// Create a sample schema file
	schemaContent := `{
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
	}`
	schemaFile := filepath.Join(schemaDir, "example", "1.0", "endpoint.json")
	os.MkdirAll(filepath.Dir(schemaFile), 0755)
	os.WriteFile(schemaFile, []byte(schemaContent), 0644)

	config := map[string]string{"schema_dir": schemaDir}
	v, err := New(context.Background(), config)
	if err != (definition.Error{}) {
		t.Fatalf("Failed to create validator: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, _ := url.Parse(tt.url)
			valid, err := v["example_1.0_endpoint"].Validate(context.Background(), u, []byte(tt.payload))
			if (err != (definition.Error{}) && !strings.Contains(err.Message, tt.wantErr)) || (err == (definition.Error{}) && tt.wantErr != "") {
				t.Errorf("Error: Validate() returned error = %v, expected error = %v", err, tt.wantErr)
				return
			}
			if valid != tt.wantValid {
				t.Errorf("Error: Validate() returned valid = %v, expected valid = %v", valid, tt.wantValid)
			} else {
				t.Logf("Test %s passed: valid = %v", tt.name, valid)
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
				return fmt.Errorf("schema directory does not exist: %s", schemaDir)

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
				os.MkdirAll(filepath.Dir(invalidSchemaFile), 0755)
				return os.WriteFile(invalidSchemaFile, []byte(`{}`), 0644)
			},
			wantErr: "invalid schema file structure",
		},
		{
			name: "Failed to compile JSON schema",
			setupFunc: func(schemaDir string) error {
				// Create a schema file with invalid JSON
				invalidSchemaFile := filepath.Join(schemaDir, "example", "1.0", "endpoint.json")
				os.MkdirAll(filepath.Dir(invalidSchemaFile), 0755)
				return os.WriteFile(invalidSchemaFile, []byte(`{invalid json}`), 0644)
			},
			wantErr: "failed to compile JSON schema",
		},
		{
			name: "Invalid schema file structure with empty components",
			setupFunc: func(schemaDir string) error {
				// Create a schema file with empty domain, version, or schema name
				invalidSchemaFile := filepath.Join(schemaDir, "", "1.0", "endpoint.json")
				os.MkdirAll(filepath.Dir(invalidSchemaFile), 0755)
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
			wantErr: "invalid schema file structure, one or more components are empty",
		},
		{
			name: "Failed to read directory",
			setupFunc: func(schemaDir string) error {
				// Create a directory and remove read permissions
				os.MkdirAll(schemaDir, 0000)
				return nil
			},
			wantErr: "failed to read directory",
		},
		{
			name: "Failed to access schema directory",
			setupFunc: func(schemaDir string) error {
				// Create a directory and remove access permissions
				os.MkdirAll(schemaDir, 0000)
				return nil
			},
			wantErr: "failed to access schema directory",
		},
		{
			name: "Valid schema directory",
			setupFunc: func(schemaDir string) error {
				// Create a valid schema file
				validSchemaFile := filepath.Join(schemaDir, "example", "1.0", "endpoint.json")
				os.MkdirAll(filepath.Dir(validSchemaFile), 0755)
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

			config := map[string]string{"schema_dir": schemaDir}
			v := &Validator{config: config}

			_, err := v.Initialise()
			if (err != (definition.Error{}) && !strings.Contains(err.Message, tt.wantErr)) || (err == (definition.Error{}) && tt.wantErr != "") {
				t.Errorf("Error: Initialise() returned error = %v, expected error = %v", err, tt.wantErr)
			} else if err == (definition.Error{}) {
				t.Logf("Test %s passed: validator initialized successfully", tt.name)
			} else {
				t.Logf("Test %s passed with expected error: %v", tt.name, err)
			}
		})
	}
}

func TestValidator_New(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]string
		setupFunc func(schemaDir string) error
		wantErr   string
	}{
		{
			name: "Failed to initialise validators",
			config: map[string]string{
				"schema_dir": "/invalid/path",
			},
			setupFunc: func(schemaDir string) error {
				// Do not create the schema directory
				return nil
			},
			wantErr: "failed to initialise validators",
		},
		{
			name: "Valid initialisation",
			config: map[string]string{
				"schema_dir": "/valid/path",
			},
			setupFunc: func(schemaDir string) error {
				// Create a valid schema directory and file
				validSchemaFile := filepath.Join(schemaDir, "example", "1.0", "endpoint.json")
				os.MkdirAll(filepath.Dir(validSchemaFile), 0755)
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
			schemaDir := tt.config["schema_dir"]
			defer os.RemoveAll(schemaDir)

			// Run the setup function to prepare the test case
			if err := tt.setupFunc(schemaDir); err != nil {
				t.Fatalf("setupFunc() error = %v", err)
			}

			_, err := New(context.Background(), tt.config)
			if (err != (definition.Error{}) && !strings.Contains(err.Message, tt.wantErr)) || (err == (definition.Error{}) && tt.wantErr != "") {
				t.Errorf("Error: New() returned error = %v, expected error = %v", err, tt.wantErr)
			} else if err == (definition.Error{}) {
				t.Logf("Test %s passed: validator initialized successfully", tt.name)
			} else {
				t.Logf("Test %s passed with expected error: %v", tt.name, err)
			}
		})
	}
}
