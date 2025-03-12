package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	log "beckn-onix/shared/log"
	"beckn-onix/shared/plugin"
	"strings"

	"gopkg.in/yaml.v2"
)

type config struct {
	AppName string       `yaml:"appName"`
	Port    int          `yaml:"port"`
	Plugin  PluginConfig `yaml:"plugin"`
}

type PluginConfig struct {
	Root       string           `yaml:"root"`
	Publisher  PublisherConfig  `yaml:"publisher"`
	Encryption EncryptionConfig `yaml:"encryption"`
}

type PublisherConfig struct {
	ID     string                 `yaml:"id"`
	Config map[string]interface{} `yaml:"config"`
}

type EncryptionConfig struct {
	ID     string                 `yaml:"id"`
	Config map[string]interface{} `yaml:"config"`
}

type server struct {
	publisher plugin.Publisher
	encryption plugin.Encryption
}

func (s *server) handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Log.Error("Error reading request body:", err)
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Get public key from request header or config
	publicKey := r.Header.Get("X-Public-Key")
	if publicKey == "" {
		log.Log.Error("Public key not provided")
		http.Error(w, "Public key required", http.StatusBadRequest)
		return
	}

	// Encrypt the message
	encryptedData, err := s.encryption.Encrypt(r.Context(), string(body), publicKey)
	if err != nil {
		log.Log.Error("Failed to encrypt message:", err)
		http.Error(w, "Encryption failed", http.StatusInternalServerError)
		return
	}

	// Publish encrypted message
	go func() {
		if err := s.publisher.Publish(encryptedData); err != nil {
			log.Log.Error("Failed to publish message:", err)
		}
	}()

	log.Log.Info("Received and encrypted request:", r.Method, r.URL.Path)
	w.WriteHeader(http.StatusOK)
}

func run(ctx context.Context, configPath string) error {
	log.Log.Debug("Path: ", configPath)
	configuration, err := initConfig(ctx, configPath)
	if err != nil {
		log.Log.Error("Error initializing config: ", err)
		return err
	}
	log.Log.Debug("Config: ", configuration)
	pm := plugin.NewPluginManager(configuration.Plugin.Root, configPath)
	if err := pm.LoadPlugin(configuration.Plugin.Publisher.ID); err != nil {
		return fmt.Errorf("failed to load publisher plugin: %w", err)
	}

	pub, err := pm.GetPublisher(configuration.Plugin.Publisher.ID)
	if err != nil {
		return fmt.Errorf("failed to get publisher: %w", err)
	}

	if err := pub.Configure(configuration.Plugin.Publisher.Config); err != nil {
		return fmt.Errorf("failed to configure publisher: %w", err)
	}

	// Load and configure encryption plugin
	if err := pm.LoadPlugin(configuration.Plugin.Encryption.ID); err != nil {
		return fmt.Errorf("failed to load encryption plugin: %w", err)
	}

	enc, err := pm.GetEncrypter(configuration.Plugin.Encryption.ID)
	if err != nil {
		return fmt.Errorf("failed to get encrypter: %w", err)
	}

	if err := enc.Configure(configuration.Plugin.Encryption.Config); err != nil {
		return fmt.Errorf("failed to configure encrypter: %w", err)
	}

	srv := &server{
		publisher: pub,
		encryption: enc,
	}

	port := fmt.Sprintf(":%d", configuration.Port)
	http.HandleFunc("/", srv.handler)

	httpServer := &http.Server{Addr: port}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Log.Error("Server failed:", err)
		}
	}()

	<-ctx.Done()
	log.Log.Info("Shutting down server...")
	return httpServer.Shutdown(context.Background())
}

func (c *config) validate() error {
	if len(strings.TrimSpace(c.AppName)) == 0 {
		return fmt.Errorf("appName is required but was empty")
	}

	if c.Port == 0 {
		return fmt.Errorf("port is required but was not set")
	}

	if c.Port < 1024 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1024 and 65535, got %d", c.Port)
	}

	if c.Plugin.Root == "" {
		return fmt.Errorf("plugin root path is required")
	}

	if c.Plugin.Publisher.ID == "" {
		return fmt.Errorf("publisher ID is required")
	}

	if c.Plugin.Encryption.ID == "" {
		return fmt.Errorf("encryption ID is required")
	}

	return nil
}

func initConfig(ctx context.Context, path string) (*config, error) {
	file, err := os.Open(path)
	if err != nil {
		log.Log.Error("Could not open config file: ", err)
		return nil, err
	}
	defer file.Close()

	var config config
	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		log.Log.Error("Could not unmarshal config data: ", err)
		return nil, err
	}
	if err := config.validate(); err != nil {
		log.Log.Error("Config validation failed: ", err)
		return nil, err
	}

	return &config, nil
}

var configPath string

func main() {
	flag.StringVar(&configPath, "config", "../../config/networkSideReceiver-config.yaml", "path to config file")
	flag.Parse()

	if err := run(context.Background(), configPath); err != nil {
		log.Log.Fatalln("Application failed:", err)
	}
}
