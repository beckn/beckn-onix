package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/beckn/beckn-onix/plugin"
	"github.com/beckn/beckn-onix/plugin/definition"
)

// PluginManager defines the methods required for the stdHandler.
type PluginManager interface {
	Middleware(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error)
	SignValidator(ctx context.Context, cfg *plugin.Config) (definition.SignValidator, error)
	Validator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error)
	Router(ctx context.Context, cfg *plugin.Config) (definition.Router, error)
	Publisher(ctx context.Context, cfg *plugin.Config) (definition.Publisher, error)
	Signer(ctx context.Context, cfg *plugin.Config) (definition.Signer, error)
	Step(ctx context.Context, cfg *plugin.Config) (definition.Step, error)
}

// Provider is a function type that provides an HTTP handler.
type Provider func(ctx context.Context, mgr PluginManager, cfg *Config) (http.Handler, error)

// HandlerType defines the type of handler.
type HandlerType string

// HandlerTypeStd represents the standard handler type.
const (
	HandlerTypeStd HandlerType = "std"
)

// PluginCfg holds configuration for various plugins.
type PluginCfg struct {
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
	Plugins PluginCfg `yaml:"plugin"`
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

// DummyHandler is a test HTTP handler that returns a simple response.
func DummyHandler(ctx context.Context, mgr PluginManager, cfg *Config) (http.Handler, error) {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Dummy Handler Response"))
	}), nil
}
