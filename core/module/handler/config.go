package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
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
	PolicyChecker(ctx context.Context, cfg *plugin.Config) (definition.PolicyChecker, error)
	Cache(ctx context.Context, cfg *plugin.Config) (definition.Cache, error)
	Registry(ctx context.Context, cfg *plugin.Config) (definition.RegistryLookup, error)
	KeyManager(ctx context.Context, cache definition.Cache, rLookup definition.RegistryLookup, cfg *plugin.Config) (definition.KeyManager, error)
	TransportWrapper(ctx context.Context, cfg *plugin.Config) (definition.TransportWrapper, error)
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
	SchemaValidator  *plugin.Config  `yaml:"schemaValidator,omitempty"`
	PolicyChecker    *plugin.Config  `yaml:"checkPolicy,omitempty"`
	SignValidator    *plugin.Config  `yaml:"signValidator,omitempty"`
	Publisher        *plugin.Config  `yaml:"publisher,omitempty"`
	Signer           *plugin.Config  `yaml:"signer,omitempty"`
	Router           *plugin.Config  `yaml:"router,omitempty"`
	Cache            *plugin.Config  `yaml:"cache,omitempty"`
	Registry         *plugin.Config  `yaml:"registry,omitempty"`
	KeyManager       *plugin.Config  `yaml:"keyManager,omitempty"`
	TransportWrapper *plugin.Config  `yaml:"transportWrapper,omitempty"`
	Middleware       []plugin.Config `yaml:"middleware,omitempty"`
	Steps            []plugin.Config
}

// PluginEntries returns a flat list of all configured plugins in this PluginCfg.
// Each named slot contributes one entry; Steps and Middleware contribute one
// entry per item. Update this method whenever a new plugin slot is added to
// PluginCfg so that the onix_plugin_info gauge stays complete.
func (p *PluginCfg) PluginEntries() []telemetry.PluginEntry {
	var entries []telemetry.PluginEntry
	add := func(pluginType string, c *plugin.Config) {
		if c != nil && c.ID != "" {
			entries = append(entries, telemetry.PluginEntry{Type: pluginType, ID: c.ID})
		}
	}
	add("schema_validator", p.SchemaValidator)
	add("sign_validator", p.SignValidator)
	add("router", p.Router)
	add("registry", p.Registry)
	add("publisher", p.Publisher)
	add("signer", p.Signer)
	add("cache", p.Cache)
	add("transport_wrapper", p.TransportWrapper)
	add("policy_checker", p.PolicyChecker)
	add("key_manager", p.KeyManager)
	for i := range p.Steps {
		if p.Steps[i].ID != "" {
			entries = append(entries, telemetry.PluginEntry{Type: "step", ID: p.Steps[i].ID})
		}
	}
	for i := range p.Middleware {
		if p.Middleware[i].ID != "" {
			entries = append(entries, telemetry.PluginEntry{Type: "middleware", ID: p.Middleware[i].ID})
		}
	}
	return entries
}

// HttpClientConfig defines the configuration for the HTTP transport layer.
type HttpClientConfig struct {
	// MaxIdleConns controls the maximum number of idle (keep-alive)
	// connections across all hosts.
	MaxIdleConns int `yaml:"maxIdleConns"`

	// IdleConnTimeout is the maximum amount of time an idle
	// (keep-alive) connection will remain idle before closing itself.
	IdleConnTimeout time.Duration `yaml:"idleConnTimeout"`

	// MaxIdleConnsPerHost, if non-zero, controls the maximum idle
	// (keep-alive) connections to keep per-host.
	MaxIdleConnsPerHost int `yaml:"maxIdleConnsPerHost"`

	// ResponseHeaderTimeout, if non-zero, specifies the amount of time to wait
	// for a server's response headers after fully writing the request.
	ResponseHeaderTimeout time.Duration `yaml:"responseHeaderTimeout"`
}

// Config holds the configuration for request processing handlers.
type Config struct {
	Plugins          PluginCfg `yaml:"plugins"`
	Steps            []string
	Type             Type
	RegistryURL      string `yaml:"registryUrl"`
	Role             model.Role
	SubscriberID     string           `yaml:"subscriberId"`
	HttpClientConfig HttpClientConfig `yaml:"httpClientConfig"`
}
