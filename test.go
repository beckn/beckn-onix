package main

import (
	"beckn-onix/plugins"
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"strings"
)

// Payload represents the structure of the data payload with context information.
type Payload struct {
	Context struct {
		Domain  string `json:"domain"`
		Version string `json:"version"`
	} `json:"context"`
}

func main() {

	pluginsConfig, err := plugins.LoadPluginsConfig("plugins/config.yaml")
	if err != nil {
		log.Fatalf("Failed to load plugins configuration: %v", err)
	}

	_, validators, err := plugins.NewValidatorProvider(pluginsConfig)
	if err != nil {
		log.Fatalf("Failed to create PluginManager: %v", err)
	}

	for fileName, validator := range validators {
		fmt.Printf("%s: %v\n", fileName, validator)
	}
	requestURL := "http://example.com/select"

	// Extract endpoint from request URL
	u, err := url.Parse(requestURL)
	if err != nil {
		log.Fatalf("Failed to parse request URL: %v", err)
	}
	schemaFileName := fmt.Sprintf("%s.json", strings.Trim(u.Path, "/"))

	//approch 1 start
	//	endpoint := strings.Trim(u.Path, "/")

	payloadData, err := ioutil.ReadFile("plugins/test/payload.json") //approach 2
	if err != nil {
		log.Fatalf("Failed to read payload data: %v", err)
	}
	// var payload Payload
	// if err := json.Unmarshal(payloadData, &payload); err != nil {
	// 	log.Fatalf("Failed to unmarshal payload: %v", err)
	// }
	// domain := strings.Replace(strings.ToLower(payload.Context.Domain), ":", "_", -1)
	// schemaFileName := fmt.Sprintf("%s_%s.json.%s", domain,
	// 	strings.ToLower(payload.Context.Version), endpoint)

	//end

	validator, exists := validators[schemaFileName]
	if !exists {
		log.Fatalf("Validator not found for %s", schemaFileName)
	}
	ctx := context.Background()
	err = validator.Validate(ctx, payloadData)
	if err != nil {
		fmt.Printf("Document validation failed: %v\n", err)
	} else {
		fmt.Println("Document validation succeeded!")
	}
}
