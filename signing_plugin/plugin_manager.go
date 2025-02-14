package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"plugin"
	"strings"

	plugindefinitions "beckn_signing.go/plugin_definitions"
	"gopkg.in/yaml.v3"
)

// PluginConfig struct for YAML
type PluginConfig struct {
	Plugins struct {
		SigningPlugin struct {
			ID     string `yaml:"id"`
			Config struct {
				Algorithm   string `yaml:"algorithm"`
				PrivateKey  string `yaml:"private_key"`
				PublicKey   string `yaml:"public_key"`
				RequestFile string `yaml:"request_file"`
				VerifyFile  string `yaml:"verify_file"`
				Subscriber  string `yaml:"subscriber"`
				KeyID       string `yaml:"key_id"`
			} `yaml:"config"`
		} `yaml:"signing_plugin"`
	} `yaml:"plugins"`
}

// RequestData struct
type RequestData struct {
	Method  string                 `json:"method"`
	URL     string                 `json:"url"`
	Headers map[string]string      `json:"headers"`
	Body    map[string]interface{} `json:"body"`
}

func loadConfig(filename string) (*PluginConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var config PluginConfig
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func loadRequest(filename string) ([]byte, string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, "", err
	}

	var reqData RequestData
	err = json.Unmarshal(data, &reqData)
	if err != nil {
		return nil, "", err
	}
	jsonBody, err := json.Marshal(reqData.Body)
	if err != nil {
		return nil, "", err
	}
	var headerStringBuilder strings.Builder
	for key, value := range reqData.Headers {
		headerStringBuilder.WriteString(fmt.Sprintf("%s: %s\n", key, value))
	}
	headerString := headerStringBuilder.String()

	return jsonBody, headerString, nil
}

func main() {
	config, err := loadConfig("plugin_config.yaml")
	if err != nil {
		fmt.Println("Error loading config:", err)
		return
	}

	publicKeyPath := config.Plugins.SigningPlugin.Config.PublicKey
	privateKeyPath := config.Plugins.SigningPlugin.Config.PrivateKey
	// requestFilePath := config.Plugins.SigningPlugin.Config.RequestFile
	// verifyFilePath := config.Plugins.SigningPlugin.Config.VerifyFile
	subscriberID := config.Plugins.SigningPlugin.Config.Subscriber
	keyID := config.Plugins.SigningPlugin.Config.KeyID

	plug, err := plugin.Open("beckn_signing.so")
	if err != nil {
		fmt.Println("Error loading plugin:", err)
		return
	}

	symNewSigning, err := plug.Lookup("NewSigning")
	if err != nil {
		fmt.Println("Error looking up NewSigning:", err)
		return
	}

	newSigningFunc, ok := symNewSigning.(func(string, string) plugindefinitions.SignatureAndValidation)
	if !ok {
		fmt.Println("Error asserting NewSigning function type")
		return
	}

	signing := newSigningFunc(publicKeyPath, privateKeyPath)

	reqBody, _, err := loadRequest(config.Plugins.SigningPlugin.Config.RequestFile)
	if err != nil {
		fmt.Println("Error loading request body:", err)
		return
	}

	// sampleBody := "sample string"
	// fmt.Println("Printing the body for generate : ", string(reqBody))
	signature, err := signing.Sign(reqBody, subscriberID, keyID)
	if err != nil {
		fmt.Println("Error signing request:", err)
		return
	}

	fmt.Println("Generated Signature:", signature)

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Println("Error generating keys:", err)
		return
	}

	fmt.Println("Public Key (Base64):", base64.StdEncoding.EncodeToString(publicKey))
	fmt.Println("Private Key (Base64):", base64.StdEncoding.EncodeToString(privateKey))

	body, header, err := loadRequest(config.Plugins.SigningPlugin.Config.VerifyFile)
	if err != nil {
		fmt.Println("Error varify request:", err)
		return
	}

	// fmt.Println("Printing the body for verify : ", string(body))
	isValid, err := signing.Verify(body, []byte(header))
	if err != nil {
		fmt.Println("Verification failed:", err)
	} else {
		fmt.Println("Verification success:", isValid)
	}

	fmt.Println("Public Key Length:", len(publicKey))
	fmt.Println("Private Key Length:", len(privateKey))
}
