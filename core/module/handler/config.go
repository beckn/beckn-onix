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
	PolicyChecker(ctx context.Context, manifestLoader definition.ManifestLoader, cfg *plugin.Config) (definition.PolicyChecker, error)
	SchemaVersionMediator(ctx context.Context, manifestLoader definition.ManifestLoader, cfg *plugin.Config) (definition.SchemaVersionMediator, error)
	Cache(ctx context.Context, cfg *plugin.Config) (definition.Cache, error)
	Registry(ctx context.Context, cache definition.Cache, cfg *plugin.Config) (definition.RegistryLookup, error)
	KeyManager(ctx context.Context, rLookup definition.RegistryLookup, cfg *plugin.Config) (definition.KeyManager, error)
	ManifestLoader(ctx context.Context, cache definition.Cache, lookup definition.RegistryMetadataLookup, cfg *plugin.Config) (definition.ManifestLoader, error)
	TransportWrapper(ctx context.Context, cfg *plugin.Config) (definition.TransportWrapper, error)
	SchemaValidator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error)
	PayloadStore(ctx context.Context, cache definition.Cache, namespace string, cfg *plugin.Config) (definition.PayloadStore, error)
	CatalogPublisher(ctx context.Context, km definition.KeyManager, blobStore definition.CatalogBlobStore, registry definition.RegistryLookup, cfg *plugin.Config) (definition.CatalogPublisher, error)
	CatalogBlobStore(ctx context.Context, cfg *plugin.Config) (definition.CatalogBlobStore, error)
	Mapper(ctx context.Context, cfg *plugin.Config) (definition.Mapper, error)
	ProviderStep(ctx context.Context, registry definition.ProviderRecordLookup, mapper definition.Mapper, cfg *plugin.Config) (definition.Step, error)
}

// Type defines different handler types for processing requests.
type Type string

const (
	// HandlerTypeStd represents the standard handler type used for general request processing.
	HandlerTypeStd Type = "std"
	// HandlerTypeCatalogPublish handles DS-internal, unsigned catalog/publish
	// triggers: it invokes a CatalogPublisher synchronously with the
	// catalogs in the request body and writes the result to a local output
	// root, bypassing validateSign/signAck since the caller is the
	// operator's own tooling, not another network participant.
	HandlerTypeCatalogPublish Type = "catalogPublish"
	// HandlerTypeCatalogCrawl handles a DS-internal, unsigned on-demand
	// crawl trigger: it invokes the already-running Crawler plugin's
	// CrawlRegistry against caller-supplied networkIds and returns a run
	// ID. Unlike other handler types, its provider is registered at
	// runtime (see module.RegisterProvider, called from
	// catalogcrawler.RegisterHandler) rather than statically in
	// handlerProviders, because it closes over the single Crawler instance
	// main.go starts as a background job -- CrawlRegistry requires that
	// instance to already be running.
	// HandlerTypeCatalogCrawl handles the /crawl/* endpoint family: an
	// on-demand crawl trigger (invokes the already-running Crawler
	// plugin's CrawlRegistry) and a crawl/sync status query, both
	// sub-routed internally by catalogcrawler.NewHandler rather than
	// registered as separate handler types -- see that function's own doc
	// comment. Its provider is registered at runtime (see
	// module.RegisterProvider, called from catalogcrawler.RegisterHandler)
	// rather than statically in handlerProviders, because it closes over
	// the single Crawler instance main.go starts as a background job --
	// CrawlRegistry requires that instance to already be running.
	HandlerTypeCatalogCrawl Type = "catalogCrawl"
)

// PluginCfg holds the configuration for various plugins.
type PluginCfg struct {
	SchemaValidator       *plugin.Config  `yaml:"schemaValidator,omitempty"`
	PolicyChecker         *plugin.Config  `yaml:"checkPolicy,omitempty"`
	PayloadTransformer    *plugin.Config  `yaml:"payloadTransformer,omitempty"`
	SignValidator         *plugin.Config  `yaml:"signValidator,omitempty"`
	Publisher             *plugin.Config  `yaml:"publisher,omitempty"`
	Signer                *plugin.Config  `yaml:"signer,omitempty"`
	Router                *plugin.Config  `yaml:"router,omitempty"`
	Cache                 *plugin.Config  `yaml:"cache,omitempty"`
	Registry              *plugin.Config  `yaml:"registry,omitempty"`
	KeyManager            *plugin.Config  `yaml:"keyManager,omitempty"`
	ManifestLoader        *plugin.Config  `yaml:"manifestLoader,omitempty"`
	SchemaVersionMediator *plugin.Config  `yaml:"schemaVersionMediator,omitempty"`
	TransportWrapper      *plugin.Config  `yaml:"transportWrapper,omitempty"`
	PayloadStore          *plugin.Config  `yaml:"payloadStore,omitempty"`
	CatalogPublisher      *plugin.Config  `yaml:"catalogPublisher,omitempty"`
	CatalogBlobStore      *plugin.Config  `yaml:"catalogBlobStore,omitempty"`
	Mapper                *plugin.Config  `yaml:"mapper,omitempty"`
	Middleware            []plugin.Config `yaml:"middleware,omitempty"`
	Steps                 []plugin.Config
	// ProviderSteps are steps that serve one provider capability end to end.
	// Separate from Steps because they are handed a registry and a mapper, which
	// the plain StepProvider contract cannot do. They resolve by id in a step
	// list exactly as Steps entries do.
	ProviderSteps []plugin.Config `yaml:"providerSteps,omitempty"`
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
	add("schema_version_mediator", p.SchemaVersionMediator)
	add("payload_transformer", p.PayloadTransformer)
	add("key_manager", p.KeyManager)
	add("payload_store", p.PayloadStore)
	add("catalog_publisher", p.CatalogPublisher)
	add("catalog_blob_store", p.CatalogBlobStore)
	add("mapper", p.Mapper)
	for i := range p.Steps {
		if p.Steps[i].ID != "" {
			entries = append(entries, telemetry.PluginEntry{Type: "step", ID: p.Steps[i].ID})
		}
	}
	for i := range p.ProviderSteps {
		if p.ProviderSteps[i].ID != "" {
			entries = append(entries, telemetry.PluginEntry{Type: "provider_step", ID: p.ProviderSteps[i].ID})
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
	// BasePath is the HTTP path prefix at which this module is mounted (e.g.
	// "/bap/receiver/"). Set by the module layer from module.Config.Path; not
	// read from YAML. Steps use it to strip the prefix before calling plugins.
	BasePath string `yaml:"-"`
	// OutputRoot previously named the catalogPublish handler's local output
	// directory directly. Storage is now supplied to the catalogPublisher
	// plugin via its own CatalogBlobStore plugin config
	// (Plugins.CatalogBlobStore, e.g. localcatalogblobstore's "root" key)
	// instead, so NewCatalogPublishHandler no longer reads this field. Kept
	// on Config only for backward-compat parsing of existing YAML that
	// still sets outputRoot -- YAML decoding in this package is not strict
	// (unknown keys are simply ignored), so removing the field entirely
	// would not break parsing, but that's a config-migration decision
	// better made deliberately, not as a side effect of this handler
	// change.
	OutputRoot string `yaml:"outputRoot,omitempty"`
	// AuthDisabled, when true, skips signature verification on handlers that
	// would otherwise require it (currently only catalogCrawlStatus). LOCAL
	// DEV / TESTING ONLY -- with this set, the caller's identity comes from
	// an unauthenticated subscriberId query param instead of a verified
	// Authorization header, so any caller can query any subscriber's crawl
	// status. Must stay false/unset for any network-facing deployment.
	AuthDisabled bool `yaml:"authDisabled,omitempty"`
}
