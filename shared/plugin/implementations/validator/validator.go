package validator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Validator implements the Validator interface.
type Validator struct {
	config      map[string]string
	schema      *jsonschema.Schema
	SchemaCache map[string]*jsonschema.Schema
}

// New creates a new Verifier instance.
func New(ctx context.Context, config map[string]string) (map[string]*Validator, error) {
	v := &Validator{config: config}
	// Call Initialise function to load schemas and get validators
	validators, err := v.Initialise()
	if err != nil {
		return nil, fmt.Errorf("failed to initialise validators: %v", err)
	}
	return validators, nil
}

// Payload represents the structure of the data payload with context information.
type Payload struct {
	Context struct {
		Domain  string `json:"domain"`
		Version string `json:"version"`
	} `json:"context"`
}

// ValidatorProvider provides instances of Validator.
type ValidatorProvider struct{}

// Validate validates the given data against the schema.
func (v *Validator) Validate(ctx context.Context, url url.URL, payload []byte) (bool, error) {
	var payloadData Payload
	err := json.Unmarshal(payload, &payloadData)
	if err != nil {
		return false, fmt.Errorf("failed to parse JSON payload: %v", err)
	}

	// Extract domain, version, and endpoint from the payload and uri
	domain := payloadData.Context.Domain
	version := payloadData.Context.Version
	version = fmt.Sprintf("v%s", version)

	endpoint := path.Base(url.String())
	fmt.Println("Handling request for endpoint:", endpoint)
	domain = strings.ToLower(domain)
	domain = strings.ReplaceAll(domain, ":", "_")

	//schemaFileName := fmt.Sprintf("%s_%s_%s", domain, version, endpoint)
	var jsonData interface{}
	if err := json.Unmarshal(payload, &jsonData); err != nil {
		return false, err
	}
	err = v.schema.Validate(jsonData)
	if err != nil {
		// TODO: Integrate with the logging module once it is ready
		return false, fmt.Errorf("Validation failed: %v", err)
	}

	return true, nil
}

// Initialise initialises the validator provider by compiling all the JSON schema files
// from the specified directory and storing them in a cache. It returns a map of validators
// indexed by their schema filenames.
func (v *Validator) Initialise() (map[string]*Validator, error) {
	// Initialize SchemaCache as an empty Map if it's nil
	if v.SchemaCache == nil {
		v.SchemaCache = make(map[string]*jsonschema.Schema)
	}
	schemaDir := v.config["schema_dir"]
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
	validatorCache := make(map[string]*Validator)
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
				schemaFileName = strings.TrimSuffix(schemaFileName, ".json")

				if domain == "" || version == "" || schemaFileName == "" {
					return fmt.Errorf("invalid schema file structure, one or more components are empty. Relative path: %s", relativePath)
				}

				// Construct a unique key combining domain, version, and schema name (e.g., ondc_trv10_v2.0.0_schema).
				uniqueKey := fmt.Sprintf("%s_%s_%s", domain, version, schemaFileName)
				// Store the compiled schema in the SchemaCache using the unique key.
				v.SchemaCache[uniqueKey] = compiledSchema
				// Store the corresponding validator in the validatorCache using the same unique key.
				validatorCache[uniqueKey] = &Validator{schema: compiledSchema}
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
