package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"beckn-onix/plugins"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// tekuriValidator implements the Validator interface using the santhosh-tekuri/jsonschema package.
type tekuriValidator struct {
	schema *jsonschema.Schema
}

type tekuriValidatorProvider struct {
	schemaCache map[string]map[string]*jsonschema.Schema
	//schemaCache map[string]*jsonschema.Schema
}

// Validate validates the given data against the schema.
func (v *tekuriValidator) Validate(ctx context.Context, data []byte) error {
	// start := time.Now()
	var jsonData interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return err
	}
	fmt.Println("json data : ", jsonData)
	err := v.schema.Validate(jsonData)
	if err != nil {
		fmt.Printf("Validation error: %v\n", err)
	}

	// fmt.Printf("validate executed in %s\n", time.Since(start))

	return err
}

//(Approach 2)(all json files for each schema)

// Initialize reads all .json files from the given schema directory, validates them using JSON Schema, and prints the result.
func (vp *tekuriValidatorProvider) Initialize(schemaDir string) (map[string]plugins.Validator, error) {
	//start := time.Now()
	// Initialize the cache
	vp.schemaCache = make(map[string]map[string]*jsonschema.Schema)
	validatorCache := make(map[string]plugins.Validator)

	// Read the directory
	files, err := ioutil.ReadDir(schemaDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema directory: %v", err)
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".json" {
			// Read the JSON file
			filePath := filepath.Join(schemaDir, file.Name())
			fmt.Println("file path : ", filePath)
			compiler := jsonschema.NewCompiler()
			compiledSchema, err := compiler.Compile(filePath)
			if err != nil {
				return nil, fmt.Errorf("failed to compile JSON schema from file %s: %v", file.Name(), err)
			}
			if compiledSchema == nil {
				return nil, fmt.Errorf("compiled schema is nil for file %s", file.Name())
			}
			// Extract directory and filename to use in the nested map
			dir := filepath.Base(filepath.Dir(filePath))
			if vp.schemaCache[dir] == nil {
				vp.schemaCache[dir] = make(map[string]*jsonschema.Schema)
			}
			// Store the compiled schema in the nested cache
			vp.schemaCache[dir][file.Name()] = compiledSchema

			validatorCache[file.Name()] = &tekuriValidator{schema: compiledSchema}
		}
	}
	//	fmt.Printf("initialize  executed in %s\n", time.Since(start))

	return validatorCache, nil
}

// (Approach 1)
// func (vp *tekuriValidatorProvider) Initialize(schemaDir string) (map[string]plugins.Validator, error) {
// 	// start := time.Now()
// 	vp.schemaCache = make(map[string]*jsonschema.Schema)
// 	validatorCache := make(map[string]plugins.Validator)

// 	files, err := ioutil.ReadDir(schemaDir)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to read schema directory: %w", err)
// 	}
// 	for _, file := range files {
// 		if filepath.Ext(file.Name()) == ".json" {
// 			filePath := filepath.Join(schemaDir, file.Name())
// 			// Read the file content
// 			content, err := ioutil.ReadFile(filePath)
// 			if err != nil {
// 				return nil, fmt.Errorf("failed to read file %s: %v", filePath, err)
// 			}

// 			var schemaDoc map[string]interface{}
// 			if err := json.Unmarshal(content, &schemaDoc); err != nil {
// 				return nil, fmt.Errorf("failed to unmarshal JSON schema from file %s: %v", filePath, err)
// 			}

// 			if defs, exists := schemaDoc["$defs"]; exists {
// 				defsMap := defs.(map[string]interface{})

// 				for name, defSchema := range defsMap {
// 					_, err := json.Marshal(defSchema)
// 					if err != nil {
// 						return nil, fmt.Errorf("failed to marshal schema definition %s: %v", name, err)
// 					}

// 					compiler := jsonschema.NewCompiler()
// 					if err := compiler.AddResource(name, filepath.Dir(filePath)); err != nil {
// 						return nil, fmt.Errorf("failed to add resource for schema definition %s: %v", name, err)
// 					}

// 					compiledSchema, err := compiler.Compile(filePath)
// 					if err != nil {
// 						return nil, fmt.Errorf("failed to compile schema definition: %v", err)
// 					}

// 					schemaKey := fmt.Sprintf("%s.%s", file.Name(), name)
// 					vp.schemaCache[schemaKey] = compiledSchema
// 					validatorCache[schemaKey] = &tekuriValidator{schema: compiledSchema}
// 				}
// 			}
// 		}
// 	}
// 	// fmt.Printf("Initialize executed in %s\n", time.Since(start))
// 	return validatorCache, nil
// }

// Ensure tekuriValidatorProvider implements ValidatorProvider
var _ plugins.ValidatorProvider = (*tekuriValidatorProvider)(nil)

var providerInstance = &tekuriValidatorProvider{}

// Exported function to return the provider instance
func GetProvider() plugins.ValidatorProvider {
	return providerInstance
}
