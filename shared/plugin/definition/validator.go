package definition

import (
	logger "beckn-onix/shared/log"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ValidationResult represents the structure of a validation result
type ValidationResult struct {
	ID        string      `json:"id"`
	Timestamp string      `json:"timestamp"`
	Message   string      `json:"message"`
	IsValid   bool        `json:"is_valid"`
	Details   interface{} `json:"details,omitempty"`
}

// Validator interface extends Plugin
type Validator interface {
	PluginValidatorInterface
	Validate(message string) error
}

// Plugin interface that all plugins must implement
type PluginValidatorInterface interface {
	Handle(message string) error
	Configure(config map[string]interface{}) error
}

// ValidatorPlugin implements the Validator interface
type ValidatorPlugin struct {
	id      string
	config  map[string]interface{}
	results []ValidationResult
}

var (
	ErrIDMissing     = errors.New("missing required field 'id'")
	ErrConfigMissing = errors.New("missing required field 'config'")
)

// Handle processes the incoming message for validation
func (v *ValidatorPlugin) Handle(message string) error {
	if v.id == "" || v.config == nil {
		return errors.New("validator not properly configured")
	}

	result := ValidationResult{
		ID:        fmt.Sprintf("val-%d", time.Now().UnixNano()),
		Timestamp: time.Now().Format(time.RFC3339),
		Message:   message,
		IsValid:   true,
	}

	jsonResult, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		logger.Log.Error("Failed to marshal validation result:", err)
		return fmt.Errorf("failed to marshal validation result: %w", err)
	}

	logger.Log.Info("Validation result:")
	logger.Log.Info(string(jsonResult))

	v.results = append(v.results, result)
	return nil
}

// Validate implements the Validator interface
func (v *ValidatorPlugin) Validate(message string) error {
	if message == "" {
		return errors.New("empty message")
	}

	var js json.RawMessage
	if err := json.Unmarshal([]byte(message), &js); err != nil {
		logger.Log.Error("Invalid JSON message:", err)
		return fmt.Errorf("invalid JSON message: %w", err)
	}

	return v.Handle(message)
}

// Configure sets up the validator plugin with the provided configuration
func (v *ValidatorPlugin) Configure(config map[string]interface{}) error {
	if id, ok := config["id"]; ok {
		v.id = id.(string)
	} else {
		return ErrIDMissing
	}

	if cfg, ok := config["config"]; ok {
		if configMap, ok := cfg.(map[string]interface{}); ok {
			v.config = configMap
		} else {
			return ErrConfigMissing
		}
	} else {
		return ErrConfigMissing
	}

	if strings.TrimSpace(v.id) == "" {
		return ErrIDMissing
	}

	if len(v.config) == 0 {
		return ErrConfigMissing
	}

	logger.Log.Info("Validator plugin configured with id=", v.id)
	return nil
}

// GetResults returns all validation results
func (v *ValidatorPlugin) GetResults() []ValidationResult {
	return v.results
}

// Export the plugin
var PluginValidator ValidatorPlugin
