package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"beckn-onix/plugins/definitions"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TekuriValidator implements the Validator interface using the santhosh-tekuri/jsonschema package.
type TekuriValidator struct {
	schema *jsonschema.Schema
}

// TekuriValidatorProvider is responsible for managing and providing access to the JSON schema validators.
type TekuriValidatorProvider struct {
	SchemaCache map[string]*jsonschema.Schema
}

// Validate validates the given data against the schema.
func (v *TekuriValidator) Validate(ctx context.Context, data []byte) error {
	var jsonData interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return err
	}
	err := v.schema.Validate(jsonData)
	if err != nil {
		// TODO: Integrate with the logging module once it is ready
		fmt.Printf("Validation error: %v\n", err)
	}

	return err
}

// Initialize initializes the validator provider by compiling all the JSON schema files
// from the specified directory and storing them in a cache. It returns a map of validators
// indexed by their schema filenames.
func (vp *TekuriValidatorProvider) Initialize(schemaDir string) (map[string]definitions.Validator, error) {
	// Initialize SchemaCache if it's nil
	if vp.SchemaCache == nil {
		vp.SchemaCache = make(map[string]*jsonschema.Schema)
	}
	// Check if the directory exists and is accessible
	info, err := os.Stat(schemaDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("schema directory does not exist: %s", schemaDir)
		}
		return nil, fmt.Errorf("failed to access schema directory: %v", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("provided schema path is not a directory: %s", schemaDir)
	}

	// Initialize the validatorCache map to store the Validator instances associated with each schema.
	validatorCache := make(map[string]definitions.Validator)
	compiler := jsonschema.NewCompiler()

	// Helper function to process directories recursively
	var processDir func(dir string) error
	processDir = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("failed to read directory: %v", err)
		}

		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				// Recursively process subdirectories
				if err := processDir(path); err != nil {
					return err
				}
			} else if filepath.Ext(entry.Name()) == ".json" {
				// Process JSON files
				compiledSchema, err := compiler.Compile(path)
				if err != nil {
					return fmt.Errorf("failed to compile JSON schema from file %s: %v", entry.Name(), err)
				}
				if compiledSchema == nil {
					return fmt.Errorf("compiled schema is nil for file %s", entry.Name())
				}

				// Use relative path from schemaDir to avoid absolute paths and make schema keys domain/version specific.
				relativePath, err := filepath.Rel(schemaDir, path)
				if err != nil {
					return fmt.Errorf("failed to get relative path for file %s: %v", entry.Name(), err)
				}
				// Split the relative path to get domain, version, and schema.
				parts := strings.Split(relativePath, string(os.PathSeparator))

				// Ensure that the file path has at least 3 parts: domain, version, and schema file.
				if len(parts) < 3 {
					return fmt.Errorf("invalid schema file structure, expected domain/version/schema.json but got: %s", relativePath)
				}

				// Extract domain, version, and schema filename from the parts.
				// Validate that the extracted parts are non-empty
				domain := strings.TrimSpace(parts[0])
				version := strings.TrimSpace(parts[1])
				schemaFileName := strings.TrimSpace(parts[2])

				if domain == "" || version == "" || schemaFileName == "" {
					return fmt.Errorf("invalid schema file structure, one or more components are empty. Relative path: %s", relativePath)
				}

				// Construct a unique key combining domain, version, and schema name (e.g., ondc_trv10/v2.0.0/schema.json).
				uniqueKey := fmt.Sprintf("%s/%s/%s", domain, version, schemaFileName)
				// Store the compiled schema in the SchemaCache using the unique key.
				vp.SchemaCache[uniqueKey] = compiledSchema
				// Store the corresponding validator in the validatorCache using the same unique key.
				validatorCache[uniqueKey] = &TekuriValidator{schema: compiledSchema}
			}
		}
		return nil
	}

	// Start processing from the root schema directory
	if err := processDir(schemaDir); err != nil {
		return nil, fmt.Errorf("failed to read schema directory: %v", err)
	}

	return validatorCache, nil
}

var _ definitions.ValidatorProvider = (*TekuriValidatorProvider)(nil)

var providerInstance = &TekuriValidatorProvider{}

// GetProvider returns the ValidatorProvider instance.
func GetProvider() definitions.ValidatorProvider {
	return providerInstance
}
