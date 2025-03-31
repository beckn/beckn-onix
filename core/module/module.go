package module

import (
	"context"
	"fmt"
	"net/http"

	"github.com/beckn/beckn-onix/core/module/handler"
	"github.com/beckn/beckn-onix/pkg/log"
)

// Config represents the configuration for a module.
type Config struct {
	Name    string `yaml:"name"`
	Path    string `yaml:"path"`
	Handler handler.Config
}

// Provider represents a function that initializes an HTTP handler using a PluginManager.
type Provider func(ctx context.Context, mgr handler.PluginManager, cfg *handler.Config) (http.Handler, error)

// handlerProviders maintains a mapping of handler types to their respective providers.
var handlerProviders = map[handler.Type]Provider{
	handler.HandlerTypeStd:    handler.NewStdHandler,
	handler.HandlerTypeRegSub: handler.NewRegSubscibeHandler,
	handler.HandlerTypeNPSub:  handler.NewNPSubscibeHandler,
	handler.HandlerTypeLookup: handler.NewLookHandler,
}

// Register initializes and registers handlers based on the provided configuration.
// It iterates over the module configurations, retrieves appropriate handler providers,
// and registers the handlers with the HTTP multiplexer.
func Register(ctx context.Context, mCfgs []Config, mux *http.ServeMux, mgr handler.PluginManager) error {
	log.Debugf(ctx, "Registering modules with config: %#v", mCfgs)
	// Iterate over the handlers in the configuration.
	for _, c := range mCfgs {
		rmp, ok := handlerProviders[c.Handler.Type]
		if !ok {
			return fmt.Errorf("invalid module : %s", c.Name)
		}
		h, err := rmp(ctx, mgr, &c.Handler)
		if err != nil {
			return fmt.Errorf("%s : %w", c.Name, err)
		}
		h, err = addMiddleware(ctx, mgr, h, &c.Handler)
		if err != nil {
			return fmt.Errorf("failed to add middleware: %w", err)

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
