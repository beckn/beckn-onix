package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"beckn-onix/plugins"

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
func (vp *TekuriValidatorProvider) Initialize(schemaDir string) (map[string]plugins.Validator, error) {
	// Initialize the SchemaCache map to store the compiled schemas using a unique key (domain/version/schema).
	vp.SchemaCache = make(map[string]*jsonschema.Schema)
	// Initialize the validatorCache map to store the Validator instances associated with each schema.
	validatorCache := make(map[string]plugins.Validator)
	compiler := jsonschema.NewCompiler()

	// Walk through the schema directory and process each file.
	err := filepath.Walk(schemaDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only process files (ignore directories) and ensure the file has a ".json" extension.
		if !info.IsDir() && filepath.Ext(info.Name()) == ".json" {
			compiledSchema, err := compiler.Compile(path)
			if err != nil {
				return fmt.Errorf("failed to compile JSON schema from file %s: %v", info.Name(), err)
			}
			if compiledSchema == nil {
				return fmt.Errorf("compiled schema is nil for file %s", info.Name())
			}

			// Use relative path from schemaDir to avoid absolute paths and make schema keys domain/version specific.
			relativePath, err := filepath.Rel(schemaDir, path)
			if err != nil {
				return fmt.Errorf("failed to get relative path for file %s: %v", info.Name(), err)
			}
			// Split the relative path to get domain, version, and schema.
			parts := strings.Split(relativePath, string(os.PathSeparator))

			// Ensure that the file path has at least 3 parts: domain, version, and schema file.
			if len(parts) < 3 {
				return fmt.Errorf("invalid schema file structure, expected domain/version/schema.json but got: %s", relativePath)
			}

			// Extract domain, version, and schema filename from the parts.
			domain := parts[0]
			version := parts[1]
			schemaFileName := parts[2]

			// Construct a unique key combining domain, version, and schema name (e.g., ondc_trv10/v2.0.0/schema.json).
			uniqueKey := fmt.Sprintf("%s/%s/%s", domain, version, schemaFileName)
			// Store the compiled schema in the SchemaCache using the unique key.
			vp.SchemaCache[uniqueKey] = compiledSchema
			// Store the corresponding validator in the validatorCache using the same unique key.
			validatorCache[uniqueKey] = &TekuriValidator{schema: compiledSchema}

		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read schema directory: %v", err)
	}

	return validatorCache, nil
}

var _ plugins.ValidatorProvider = (*TekuriValidatorProvider)(nil)

var providerInstance = &TekuriValidatorProvider{}

// GetProvider returns the ValidatorProvider instance.
func GetProvider() plugins.ValidatorProvider {
	return providerInstance
}
