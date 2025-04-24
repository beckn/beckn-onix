package schemavalidator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Payload represents the structure of the data payload with context information.
type payload struct {
	Context struct {
		Domain        string `json:"domain"`
		Version       string `json:"version"`
		Action        string `json:"action"`
		BapID         string `json:"bap_id"`
		BapURI        string `json:"bap_uri"`
		BppID         string `json:"bpp_id"`
		BppURI        string `json:"bpp_uri"`
		MessageID     string `json:"message_id"`
		TransactionID string `json:"transaction_id"`
		Timestamp     string `json:"timestamp"`
		TTL           string `json:"ttl"`
		Location      struct {
			Country struct {
				Code string `json:"code"`
			} `json:"country"`
			City struct {
				Code string `json:"code"`
			} `json:"city"`
		} `json:"location"`
	} `json:"context"`
}

// schemaValidator implements the Validator interface.
type schemaValidator struct {
	config      *Config
	schemaCache map[string]*jsonschema.Schema
}

// Config struct for SchemaValidator.
type Config struct {
	SchemaDir string
}

// New creates a new ValidatorProvider instance.
func New(ctx context.Context, config *Config) (*schemaValidator, func() error, error) {
	// Check if config is nil
	if config == nil {
		return nil, nil, fmt.Errorf("config cannot be nil")
	}
	v := &schemaValidator{
		config:      config,
		schemaCache: make(map[string]*jsonschema.Schema),
	}

	// Call Initialise function to load schemas and get validators
	if err := v.initialise(); err != nil {
		return nil, nil, fmt.Errorf("failed to initialise schemaValidator: %v", err)
	}
	return v, nil, nil
}

// Validate validates the given data against the schema.
func (v *schemaValidator) Validate(ctx context.Context, url *url.URL, data []byte) error {
	var payloadData payload
	err := json.Unmarshal(data, &payloadData)
	if err != nil {
		return model.NewBadReqErr(fmt.Errorf("failed to parse JSON payload: %v", err))
	}

	if err := validateContext(payloadData); err != nil {
		return err
	}

	// Extract domain, version, and endpoint from the payload and uri.
	cxtDomain := payloadData.Context.Domain
	version := payloadData.Context.Version
	version = fmt.Sprintf("v%s", version)

	endpoint := path.Base(url.String())
	log.Debugf(ctx, "Handling request for endpoint: %s", endpoint)
	domain := strings.ToLower(cxtDomain)
	domain = strings.ReplaceAll(domain, ":", "_")

	// Construct the schema file name.
	schemaFileName := fmt.Sprintf("%s_%s_%s", domain, version, endpoint)

	// Retrieve the schema from the cache.
	schema, exists := v.schemaCache[schemaFileName]
	if !exists {
		return model.NewBadReqErr(fmt.Errorf("schema not found for domain: %s", domain))
	}

	var jsonData any
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return model.NewBadReqErr(fmt.Errorf("failed to parse JSON data: %v", err))
	}
	err = schema.Validate(jsonData)
	if err != nil {
		// Handle schema validation errors
		if validationErr, ok := err.(*jsonschema.ValidationError); ok {
			// Convert validation errors into an array of SchemaValError
			var schemaErrors []model.Error
			for _, cause := range validationErr.Causes {
				// Extract the path and message from the validation error
				path := strings.Join(cause.InstanceLocation, ".") // JSON path to the invalid field
				message := cause.Error()                          // Validation error message

				// Append the error to the schemaErrors array
				schemaErrors = append(schemaErrors, model.Error{
					Paths:   path,
					Message: message,
				})
			}
			// Return the array of schema validation errors
			return &model.SchemaValidationErr{Errors: schemaErrors}
		}
		return fmt.Errorf("validation failed: %v", err)
	}

	// Return nil if validation succeeds
	return nil
}

func validateContext(p payload) error {
	// Validate the payload context fields FIRST
	if p.Context.Domain == "" {
		return fmt.Errorf("missing field Domain in context")
	}
	if p.Context.Version == "" {
		return fmt.Errorf("missing field Version in context")
	}
	if p.Context.Action == "" {
		return fmt.Errorf("missing field action in context")
	}
	if p.Context.BapID == "" {
		return fmt.Errorf("missing field bpp_id in context")
	}
	if p.Context.BapURI == "" {
		return fmt.Errorf("missing field bpp_uri in context")
	}
	if p.Context.BppID == "" {
		return fmt.Errorf("missing field bpp_id in context")
	}

	if p.Context.BppURI == "" {
		return fmt.Errorf("missing field bpp_uri in context")
	}
	if p.Context.MessageID == "" {
		return fmt.Errorf("missing field message_id in context")
	}
	if p.Context.TransactionID == "" {
		return fmt.Errorf("missing field transaction_id in context")
	}
	if p.Context.Timestamp == "" {
		return fmt.Errorf("missing field timestamp in context")
	}
	if p.Context.TTL == "" {
		return fmt.Errorf("missing field ttl in context")
	}
	if p.Context.Location.Country.Code == "" {
		return fmt.Errorf("missing Location.Country.Code in context")
	}
	if p.Context.Location.City.Code == "" {
		return fmt.Errorf("missing field Location.City.Code in context")
	}
	return nil

}

// Initialise initialises the validator provider by compiling all the JSON schema files
// from the specified directory and storing them in a cache indexed by their schema filenames.
func (v *schemaValidator) initialise() error {
	schemaDir := v.config.SchemaDir
	// Check if the directory exists and is accessible.
	info, err := os.Stat(schemaDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("schema directory does not exist: %s", schemaDir)
		}
		return fmt.Errorf("failed to access schema directory: %v", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("provided schema path is not a directory: %s", schemaDir)
	}

	compiler := jsonschema.NewCompiler()

	// Helper function to process directories recursively.
	var processDir func(dir string) error
	processDir = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("failed to read directory: %v", err)
		}

		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				// Recursively process subdirectories.
				if err := processDir(path); err != nil {
					return err
				}
			} else if filepath.Ext(entry.Name()) == ".json" {
				// Process JSON files.
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
				// Validate that the extracted parts are non-empty.
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
				v.schemaCache[uniqueKey] = compiledSchema
			}
		}
		return nil
	}

	// Start processing from the root schema directory.
	if err := processDir(schemaDir); err != nil {
		return fmt.Errorf("failed to read schema directory: %v", err)
	}

	return nil
}
