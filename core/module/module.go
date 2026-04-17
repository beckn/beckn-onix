package module

import (
	"context"
	"fmt"
	"net/http"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
)

// Config represents the configuration for a module.
type Config struct {
	Name    string `yaml:"name"`
	Path    string `yaml:"path"`
	Handler handler.Config
}

// Provider represents a function that initializes an HTTP handler using a PluginManager.
type Provider func(ctx context.Context, mgr handler.PluginManager, cfg *handler.Config, moduleName string) (http.Handler, error)

// handlerProviders maintains a mapping of handler types to their respective providers.
var handlerProviders = map[handler.Type]Provider{
	handler.HandlerTypeStd: handler.NewStdHandler,
}

// Register initializes and registers handlers based on the provided configuration.
// It iterates over the module configurations, retrieves appropriate handler providers,
// and registers the handlers with the HTTP multiplexer.
func Register(ctx context.Context, mCfgs []Config, mux *http.ServeMux, mgr handler.PluginManager) error {

	mux.Handle("/health", http.HandlerFunc(handler.HealthHandler))

	log.Debugf(ctx, "Registering modules with config: %#v", mCfgs)
	// Iterate over the handlers in the configuration.
	for _, c := range mCfgs {
		rmp, ok := handlerProviders[c.Handler.Type]
		if !ok {
			return fmt.Errorf("invalid module : %s", c.Name)
		}
		h, err := rmp(ctx, mgr, &c.Handler, c.Name)
		if err != nil {
			return fmt.Errorf("%s : %w", c.Name, err)
		}
		h, err = addMiddleware(ctx, mgr, h, &c.Handler)
		if err != nil {
			return fmt.Errorf("failed to add middleware: %w", err)

		}
		h = moduleCtxMiddleware(c.Name, h)
		if err := telemetry.RegisterPluginInfo(ctx, c.Name, c.Handler.SubscriberID, pluginEntries(&c.Handler)); err != nil {
			log.Warnf(ctx, "Failed to register plugin info for module %s: %v", c.Name, err)
		}
		log.Debugf(ctx, "Registering handler %s, of type %s @ %s", c.Name, c.Handler.Type, c.Path)
		mux.Handle(c.Path, h)
	}
	return nil
}

// addMiddleware applies middleware plugins to the provided handler in reverse order.
// It retrieves middleware instances from the plugin manager and chains them to the handler.
func addMiddleware(ctx context.Context, mgr handler.PluginManager, handler http.Handler, hCfg *handler.Config) (http.Handler, error) {
	mws := hCfg.Plugins.Middleware
	log.Debugf(ctx, "Applying %d middleware(s) to the handler", len(mws))
	// Apply the middleware in reverse order.
	for i := len(mws) - 1; i >= 0; i-- {
		log.Debugf(ctx, "Loading middleware: %s", mws[i].ID)
		mw, err := mgr.Middleware(ctx, &mws[i])
		if err != nil {
			log.Errorf(ctx, err, "Failed to load middleware %s: %v", mws[i].ID, err)
			return nil, fmt.Errorf("failed to load middleware %s: %w", mws[i].ID, err)
		}
		// Apply the middleware to the handler.
		handler = mw(handler)
		log.Debugf(ctx, "Applied middleware: %s", mws[i].ID)
	}

	log.Debugf(ctx, "Middleware chain setup completed")
	return handler, nil
}

// pluginEntries builds the list of loaded plugins for a handler config, one
// entry per configured plugin slot and one per step/middleware item.
func pluginEntries(cfg *handler.Config) []telemetry.PluginEntry {
	pc := &cfg.Plugins
	var entries []telemetry.PluginEntry
	add := func(pluginType string, c *plugin.Config) {
		if c != nil && c.ID != "" {
			entries = append(entries, telemetry.PluginEntry{Type: pluginType, ID: c.ID})
		}
	}
	add("schema_validator", pc.SchemaValidator)
	add("sign_validator", pc.SignValidator)
	add("router", pc.Router)
	add("registry", pc.Registry)
	add("publisher", pc.Publisher)
	add("signer", pc.Signer)
	add("cache", pc.Cache)
	add("transport_wrapper", pc.TransportWrapper)
	add("policy_checker", pc.PolicyChecker)
	add("key_manager", pc.KeyManager)
	for i := range pc.Steps {
		if pc.Steps[i].ID != "" {
			entries = append(entries, telemetry.PluginEntry{Type: "step", ID: pc.Steps[i].ID})
		}
	}
	for i := range pc.Middleware {
		if pc.Middleware[i].ID != "" {
			entries = append(entries, telemetry.PluginEntry{Type: "middleware", ID: pc.Middleware[i].ID})
		}
	}
	return entries
}

func moduleCtxMiddleware(moduleName string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), model.ContextKeyModuleID, moduleName)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
