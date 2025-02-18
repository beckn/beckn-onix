package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"beckn-onix/plugins/plugin_definition"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// tekuriValidator implements the Validator interface using the santhosh-tekuri/jsonschema package.
type tekuriValidator struct {
	schema *jsonschema.Schema
}

type tekuriValidatorProvider struct {
	compiledSchemas map[string]map[string]*jsonschema.Schema // Cache for compiled schemas
}

// Validate validates the given data against the schema.
func (v *tekuriValidator) Validate(ctx context.Context, data []byte) error {
	var jsonData interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return err
	}
	return v.schema.Validate(jsonData)
}

func (vp *tekuriValidatorProvider) Initialize(schemaDir string) error {
	// Check if vp is nil
	if vp == nil {
		return fmt.Errorf("tekuriValidatorProvider is not initialized")
	}

	// Initialize compiledSchemas
	vp.compiledSchemas = make(map[string]map[string]*jsonschema.Schema)

	compiler := jsonschema.NewCompiler()
	if compiler == nil {
		return fmt.Errorf("jsonschema compiler is not initialized")
	}

	files, err := ioutil.ReadDir(schemaDir)
	if err != nil {
		return fmt.Errorf("failed to read schema directory: %w", err)
	}

	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") {
			schemaPath := filepath.Join(schemaDir, file.Name())
			absSchemaPath, _ := filepath.Abs(schemaPath)
			fmt.Println("path:", absSchemaPath)
			fmt.Printf("Reading schema file: %s\n", schemaPath)

			fileContent, err := ioutil.ReadFile(schemaPath)
			if err != nil {
				return fmt.Errorf("failed to read schema file %s: %w", schemaPath, err)
			}

			var schemaData map[string]interface{}
			if err := json.Unmarshal(fileContent, &schemaData); err != nil {
				return fmt.Errorf("failed to unmarshal schema file %s: %w", schemaPath, err)
			}

			if defs, ok := schemaData["$defs"].(map[string]interface{}); ok {
				nameParts := strings.Split(strings.TrimSuffix(file.Name(), ".json"), "_")
				if len(nameParts) != 3 {
					return fmt.Errorf("invalid schema file name format: %s", file.Name())
				}
				domain := strings.ReplaceAll(strings.ToLower(nameParts[0]+"_"+nameParts[1]), ":", "_")
				version := strings.ToLower(nameParts[2])
				fmt.Printf("Domain: %s, Version: %s\n", domain, version)

				for defKey, defValue := range defs {
					fmt.Println("value :", defValue)
					tempSchema := map[string]interface{}{
						"$id":     fmt.Sprintf("file://%s", absSchemaPath),
						"$schema": schemaData["$schema"],
						"$defs": map[string]interface{}{
							defKey: defValue,
						},
					}
					tempSchemaData, err := json.Marshal(tempSchema)
					if err != nil {
						return fmt.Errorf("failed to marshal temporary schema for $defs.%s: %w", defKey, err)
					}
					fmt.Println(" ")

					// Use a unique ID for the resource to avoid conflict
					resourceId := fmt.Sprintf("file://%s/%s", absSchemaPath, defKey)
					fmt.Println("resource Id :", resourceId)
					compiler.AddResource(resourceId, strings.NewReader(string(tempSchemaData)))

					// Compile the specific $defs section
					defSchemaPath := fmt.Sprintf("file://%s", absSchemaPath)

					fmt.Println("def schema path printing :::  ", defSchemaPath)

					compiledSchema, err := compiler.Compile(defSchemaPath)
					if err != nil {
						fmt.Printf("Failed to compile $defs.%s in schema file: %s\nError: %v\n", defKey, schemaPath, err)
						continue
					}

					fmt.Println("schema :", compiledSchema)

					// Initialize nested map if not already initialized
					if _, exists := vp.compiledSchemas[domain]; !exists {
						vp.compiledSchemas[domain] = make(map[string]*jsonschema.Schema)
					}

					cacheKey := fmt.Sprintf("%s_%s_%s", domain, version, defKey)
					fmt.Println("key :", cacheKey)
					vp.compiledSchemas[domain][cacheKey] = compiledSchema
					fmt.Printf("Compiled and cached $defs.%s schema: %s\n", defKey, cacheKey)
				}
			}
		}
	}

	return nil
}

func (vp *tekuriValidatorProvider) Get(schemaKey string) (plugin_definition.Validator, error) {
	// Extract domain, version, and defKey from the schemaKey
	schemaKey = strings.Replace(schemaKey, ":", "_", -1)

	parts := strings.Split(schemaKey, "_")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid schema key format: %s", schemaKey)
	}
	domain := parts[0] + "_" + parts[1]
	defKey := parts[2]

	// Look up the compiled schema in the nested map
	if domainMap, ok := vp.compiledSchemas[domain]; ok {
		if schema, ok := domainMap[defKey]; ok {
			return &tekuriValidator{schema: schema}, nil
		}
	}
	return nil, fmt.Errorf("schema not found: %s", schemaKey)
}

// Ensure tekuriValidatorProvider implements ValidatorProvider
var _ plugin_definition.ValidatorProvider = (*tekuriValidatorProvider)(nil)

var providerInstance = &tekuriValidatorProvider{}

// Exported function to return the provider instance
func GetProvider() plugin_definition.ValidatorProvider {
	return providerInstance
}
