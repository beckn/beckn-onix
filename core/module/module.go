package module

import (
	"context"
	"fmt"
	"net/http"

	"github.com/beckn/beckn-onix/core/module/handler"
	"github.com/beckn/beckn-onix/core/pkg/log"
	"github.com/beckn/beckn-onix/plugin"
)

// Config represents the configuration for a module.
type Config struct {
	Name    string `yaml:"name"`
	Path    string `yaml:"path"`
	Handler handler.Config
}

// handlerProviders maps handler types to their respective provider functions.
var handlerProviders = map[handler.HandlerType]handler.Provider{
	handler.HandlerTypeStd: handler.NewStdHandler,
}

// GetDummyHandlerProviders returns a map of dummy providers for testing.
func GetDummyHandlerProviders() map[handler.HandlerType]handler.Provider {
	return map[handler.HandlerType]handler.Provider{
		handler.HandlerTypeStd: handler.DummyHandler,
	}
}

// Function to get handler providers, can be overridden for testing
var getHandlerProviders = func() map[handler.HandlerType]handler.Provider {
	return handlerProviders
}

// Register registers the handlers for the application.
func Register(ctx context.Context, mCfgs []Config, mux *http.ServeMux, mgr handler.PluginManager) error {
	log.Debugf(ctx, "Registering modules with config: %#v", mCfgs)
	// Iterate over the handlers in the configuration.
	providers := getHandlerProviders()

	for _, c := range mCfgs {
		// rmp, ok := handlerProviders[c.Handler.Type]
		rmp, ok := providers[c.Handler.Type]
		if !ok {
			return fmt.Errorf("invalid module : %s", c.Name)
		}
		h, err := rmp(ctx, mgr, &c.Handler)
		if err != nil {
			return err
		}

		h, err = chain(ctx, mgr, h, c.Handler.Plugins.Middleware)
		if err != nil {
			return fmt.Errorf("failed to add post processors: %w", err)

		}
		mux.Handle(c.Path, h)
	}
	return nil
}

// chain applies middleware to a handler in reverse order.
func chain(ctx context.Context, mgr handler.PluginManager, handler http.Handler, mws []plugin.Config) (http.Handler, error) {
	// Apply the middleware in reverse order.
	for i := len(mws) - 1; i >= 0; i-- {
		mw, err := mgr.Middleware(ctx, &mws[i])
		if err != nil {
			// Get the middleware from the plugin manager.hared/ ./shared
			return nil, err
		}
		// Apply the middleware to the handler.
		handler = mw(handler)
	}
	// Return the modified handler.
	return handler, nil
}
