package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	plugins "beckn_onix/shared/plugin"

	"gopkg.in/yaml.v2"
)

// PluginManagerConfig struct to parse YAML
type PluginManagerConfig struct {
	Plugins map[string]struct {
		ID     string            `yaml:"id"`
		Config map[string]string `yaml:"config"`
	} `yaml:"plugins"`
}

// HTTPRequest represents an HTTP request structure
type HTTPRequest struct {
	Method  string                 `json:"method"`
	URL     string                 `json:"url"`
	Headers map[string]string      `json:"headers"`
	Body    map[string]interface{} `json:"body"`
}

// getTTLFromConfig reads the TTL from the YAML config file
func getTTLFromConfig(configPath string) int64 {
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("Error reading config file: %v. Using default TTL: 3600", err)
		return 3600
	}

	var cfg PluginManagerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Printf("Error parsing YAML config: %v. Using default TTL: 3600", err)
		return 3600
	}

	if signingPlugin, exists := cfg.Plugins["signing_plugin"]; exists {
		if ttlStr, ok := signingPlugin.Config["ttl"]; ok {
			var ttl int64
			_, err := fmt.Sscanf(ttlStr, "%d", &ttl)
			if err == nil {
				return ttl
			}
		}
	}
	return 3600 // Default TTL
}

// loadHTTPRequest reads the HTTP request from a JSON file
func loadHTTPRequest(filePath string) (*HTTPRequest, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading JSON file: %w", err)
	}

	var request HTTPRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, fmt.Errorf("error parsing JSON file: %w", err)
	}

	return &request, nil
}

// writeHTTPRequest writes the updated HTTP request to a JSON file
func writeHTTPRequest(filePath string, request *HTTPRequest) error {
	data, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshalling JSON file: %w", err)
	}

	return os.WriteFile(filePath, data, 0644)
}

// loadPlugins initializes the plugin manager
func loadPlugins(configPath string) (*plugins.PluginManager, error) {
	pluginManager, err := plugins.New("./shared/plugin/implementations", configPath)
	if err != nil {
		return nil, fmt.Errorf("error initializing plugin manager: %w", err)
	}
	return pluginManager, nil
}

// signRequest signs the request using the signer plugin
func signRequest(ctx context.Context, signer plugins.Signer, privateKey string, request *HTTPRequest, ttl int64) (string, error) {
	bodyBytes, err := json.Marshal(request.Body)
	if err != nil {
		return "", fmt.Errorf("error marshalling request body: %w", err)
	}

	createdAt := time.Now().Unix()
	expiresAt := createdAt + ttl

	signature, err := signer.Sign(ctx, bodyBytes, privateKey, createdAt, expiresAt)
	if err != nil {
		return "", fmt.Errorf("error signing data: %w", err)
	}

	request.Headers["Authorization"] = fmt.Sprintf(
		`Signature keyId="sub123|ukid456|ed25519",algorithm="ed25519", created="%d", expires="%d", headers="(created) (expires) digest", signature="%s"`,
		createdAt, expiresAt, signature,
	)

	return signature, nil
}

// verifySignature verifies the signed request using the verifier plugin
func verifySignature(ctx context.Context, verifier plugins.Validator, request *HTTPRequest, publicKey string) (bool, error) {
	bodyBytes, err := json.Marshal(request.Body)
	if err != nil {
		return false, fmt.Errorf("error marshalling request body: %w", err)
	}

	authHeader, exists := request.Headers["Authorization"]
	if !exists {
		return false, fmt.Errorf("error: authorization header is missing in the request")
	}

	return verifier.Verify(ctx, bodyBytes, []byte(authHeader), publicKey)
}

func main() {
	configPath := "configs/plugin.yaml"

	//Load TTL from config
	ttl := getTTLFromConfig(configPath)

	//Generate Key Pair
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatalf("Error generating key pair: %v", err)
	}

	privateKeyBase64 := base64.StdEncoding.EncodeToString(privateKey)
	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKey)

	fmt.Println("Private Key:", privateKeyBase64)
	fmt.Println("Public Key:", publicKeyBase64)

	//Load Plugin Manager
	pluginManager, err := loadPlugins(configPath)
	if err != nil {
		log.Fatalf("Plugin Manager Initialization Failed: %v", err)
	}
	defer pluginManager.Close()

	//Load HTTP Request
	request, err := loadHTTPRequest("request/request.json")
	if err != nil {
		log.Fatalf("Error loading HTTP request: %v", err)
	}

	//Get Signer Plugin
	signer, err := pluginManager.GetSigner()
	if err != nil {
		log.Fatalf("Error loading signer plugin: %v", err)
	}

	//Sign Request
	signature, err := signRequest(context.Background(), signer, privateKeyBase64, request, ttl)
	if err != nil {
		log.Fatalf("Error signing request: %v", err)
	}
	fmt.Println("Generated Signature:", signature)

	//Save Updated Request
	if err := writeHTTPRequest("request/request.json", request); err != nil {
		log.Fatalf("Error updating request file: %v", err)
	}

	//Get Verifier Plugin
	verifier, err := pluginManager.GetVerifier()
	if err != nil {
		log.Fatalf("Error loading verifier plugin: %v", err)
	}

	//Verify Signature
	valid, err := verifySignature(context.Background(), verifier, request, publicKeyBase64)
	if err != nil {
		log.Fatalf("Error verifying signature: %v", err)
	}

	fmt.Println("Signature Valid:", valid)
}
