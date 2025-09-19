package handler

import (
	"context"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// PluginManager defines an interface for managing plugins dynamically.
type PluginManager interface {
	Middleware(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error)
	SignValidator(ctx context.Context, cfg *plugin.Config) (definition.SignValidator, error)
	Validator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error)
	Router(ctx context.Context, cfg *plugin.Config) (definition.Router, error)
	Publisher(ctx context.Context, cfg *plugin.Config) (definition.Publisher, error)
	Signer(ctx context.Context, cfg *plugin.Config) (definition.Signer, error)
	Step(ctx context.Context, cfg *plugin.Config) (definition.Step, error)
	Cache(ctx context.Context, cfg *plugin.Config) (definition.Cache, error)
	Registry(ctx context.Context, cfg *plugin.Config) (definition.RegistryLookup, error)

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
	Registry        *plugin.Config  `yaml:"registry,omitempty"`

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
}
