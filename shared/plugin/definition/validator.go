package definition

import (
	logger "beckn-onix/shared/log"
	"errors"
	"strings"
)

// ValidatorPlugin implements the Plugin interface for validation
type ValidatorPlugin struct {
	id     string
	config map[string]interface{}
}

var (
	ErrIDMissing     = errors.New("missing required field 'id'")
	ErrConfigMissing = errors.New("missing required field 'config'")
)

// Handle processes the incoming message for validation
func (v *ValidatorPlugin) Handle(message string) error {
	logger.Log.Info("Validating message with validator ID:", v.id)
	logger.Log.Info("Message to validate:", message)
	logger.Log.Info("Validation config:", v.config)

	// Add validation logic here
	return nil
}

// Configure sets up the validator plugin with the provided configuration
func (v *ValidatorPlugin) Configure(config map[string]interface{}) error {
	// Validate required fields
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

	// Validate non-empty values
	if strings.TrimSpace(v.id) == "" {
		return ErrIDMissing
	}

	if len(v.config) == 0 {
		return ErrConfigMissing
	}

	logger.Log.Info("Validator plugin configured with id=", v.id)

	return nil
}
