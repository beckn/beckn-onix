package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
)

// PluginManager defines the methods required for managing plugins in stdHandler.
type PluginManager interface {
	Middleware(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error)
	SignValidator(ctx context.Context, cfg *plugin.Config) (definition.Verifier, error)
	Validator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error)
	Router(ctx context.Context, cfg *plugin.Config) (definition.Router, error)
	Publisher(ctx context.Context, cfg *plugin.Config) (definition.Publisher, error)
	Signer(ctx context.Context, cfg *plugin.Config) (definition.Signer, error)
	Step(ctx context.Context, cfg *plugin.Config) (definition.Step, error)
	Cache(ctx context.Context, cfg *plugin.Config) (definition.Cache, error)
	KeyManager(ctx context.Context, cache definition.Cache, rLookup definition.RegistryLookup, cfg *plugin.Config) (definition.KeyManager, error)
	SchemaValidator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error)
}

// Type defines different handler types for processing requests.
type Type string

const (
	// HandlerTypeStd represents the standard handler type used for general request processing.
	HandlerTypeStd Type = "std"
)

// PluginCfg holds the configuration for various plugins.
type PluginCfg struct {
	SchemaValidator *plugin.Config  `yaml:"schemaValidator,omitempty"`
	SignValidator   *plugin.Config  `yaml:"signValidator,omitempty"`
	Publisher       *plugin.Config  `yaml:"publisher,omitempty"`
	Signer          *plugin.Config  `yaml:"signer,omitempty"`
	Router          *plugin.Config  `yaml:"router,omitempty"`
	Cache           *plugin.Config  `yaml:"cache,omitempty"`
	KeyManager      *plugin.Config  `yaml:"keyManager,omitempty"`
	Middleware      []plugin.Config `yaml:"middleware,omitempty"`
	Steps           []plugin.Config
}

// Config holds the configuration for request processing handlers.
type Config struct {
	Plugins      PluginCfg `yaml:"plugins"`
	Steps        []string
	Type         Type
	RegistryURL  string `yaml:"registryUrl"`
	Role         model.Role
	SubscriberID string `yaml:"subscriberId"`
	Trace        map[string]bool
}

// Step represents a named processing step.
type Step string

const (
	// StepInitialize represents the initialization phase of the request processing pipeline.
	StepInitialize Step = "initialize"

	// StepValidate represents the validation phase, where input data is checked for correctness.
	StepValidate Step = "validate"

	// StepProcess represents the core processing phase of the request.
	StepProcess Step = "process"

	// StepFinalize represents the finalization phase, where the response is prepared and sent.
	StepFinalize Step = "finalize"
)

// validSteps ensures only allowed step values are used.
var validSteps = map[Step]bool{
	StepInitialize: true,
	StepValidate:   true,
	StepProcess:    true,
	StepFinalize:   true,
}

// UnmarshalYAML customizes YAML unmarshalling for Step to enforce valid values.
func (s *Step) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var stepName string
	if err := unmarshal(&stepName); err != nil {
		return err
	}

	step := Step(stepName)
	if !validSteps[step] {
		return fmt.Errorf("invalid step: %s", stepName)
	}
	*s = step
	return nil
}
