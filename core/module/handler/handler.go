package handler

import (
	"fmt"

	"github.com/beckn/beckn-onix/plugin"
)

// HandlerType defines the type of handler.
type HandlerType string

// HandlerTypeStd represents the standard handler type.
const (
	HandlerTypeStd HandlerType = "std"
)

// pluginCfg holds configuration for various plugins.
type pluginCfg struct {
	SchemaValidator *plugin.Config  `yaml:"schemaValidator,omitempty"`
	SignValidator   *plugin.Config  `yaml:"signValidator,omitempty"`
	Publisher       *plugin.Config  `yaml:"publisher,omitempty"`
	Signer          *plugin.Config  `yaml:"signer,omitempty"`
	Router          *plugin.Config  `yaml:"router,omitempty"`
	Middleware      []plugin.Config `yaml:"middleware,omitempty"`
	Steps           []plugin.Config
}

// Config represents the handler configuration.
type Config struct {
	Plugins pluginCfg `yaml:"plugin"`
	Steps   []string
	Type    HandlerType
}

// Step represents a named step
type Step string

const (
	// StepInitialize represents the initialization step.
	StepInitialize Step = "initialize"
	// StepValidate represents the validation step.
	StepValidate Step = "validate"
	// StepProcess represents the processing step.
	StepProcess Step = "process"
	// StepFinalize represents the finalization step.
	StepFinalize Step = "finalize"
)

// ValidSteps ensures only allowed values are accepted
var ValidSteps = map[Step]bool{
	StepInitialize: true,
	StepValidate:   true,
	StepProcess:    true,
	StepFinalize:   true,
}

// UnmarshalYAML is a custom YAML unmarshalling method to validate step names.
func (s *Step) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var stepName string
	if err := unmarshal(&stepName); err != nil {
		return err
	}

	step := Step(stepName)
	if !ValidSteps[step] {
		return fmt.Errorf("invalid step: %s", stepName)
	}
	*s = step
	return nil
}
