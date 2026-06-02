package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"gopkg.in/yaml.v2"

	"github.com/beckn-one/beckn-onix/core/module"
	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/beckndefaults"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	_ "go.uber.org/automaxprocs"
)

// ApplicationPlugins holds application-level plugin configurations.
type ApplicationPlugins struct {
	OtelSetup *plugin.Config `yaml:"otelsetup,omitempty"`
}

// Config struct holds all configurations.
type Config struct {
	AppName                 string                   `yaml:"appName"`
	Log                     log.Config               `yaml:"log"`
	Plugins                 ApplicationPlugins       `yaml:"plugins,omitempty"`
	PluginManager           *plugin.ManagerConfig    `yaml:"pluginManager"`
	Modules                 []module.Config          `yaml:"modules"`
	HTTP                    httpConfig               `yaml:"http"`
	BecknConstants          *BecknConstantsConfig    `yaml:"becknConstants,omitempty"`
	BecknConstantsOverrides *BecknConstantsOverrides `yaml:"becknConstantsOverrides,omitempty"`
}

// BecknConstantsConfig controls behaviour of the beckn constants loader.
type BecknConstantsConfig struct {
	DisableRemoteRefresh bool `yaml:"disableRemoteRefresh"`
}

// BecknConstantsOverrides allows an operator to intentionally deviate from
// overridable beckn constants. A non-empty Reason is required.
type BecknConstantsOverrides struct {
	Reason  string                       `yaml:"reason"`
	Plugins map[string]map[string]string `yaml:"plugins,omitempty"`
}

type httpConfig struct {
	Port     string        `yaml:"port"`
	Timeouts timeoutConfig `yaml:"timeout"`
}

type timeoutConfig struct {
	Read  time.Duration `yaml:"read"`
	Write time.Duration `yaml:"write"`
	Idle  time.Duration `yaml:"idle"`
}

var configPath string
var runFunc = run

func main() {
	// Define and parse command-line flags.
	flag.StringVar(&configPath, "config", "../../config/onix/adapter.yaml", "Path to the configuration file")
	flag.Parse()

	// Use custom log for initial setup messages.
	log.Infof(context.Background(), "Starting application with config: %s", configPath)

	// Run the application within a context.
	if err := runFunc(context.Background(), configPath); err != nil {
		log.Fatalf(context.Background(), err, "Application failed: %v", err)
	}
	log.Infof(context.Background(), "Application finished")
}

// initConfig loads and validates the configuration.
func initConfig(ctx context.Context, path string) (*Config, error) {
	// Open the configuration file.
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open config file: %w", err)
	}
	defer file.Close()

	// Decode the YAML configuration.
	var cfg Config
	if err := yaml.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("could not decode config: %w", err)
	}
	log.Debugf(ctx, "Read config: %#v", cfg)

	// Inject and enforce beckn constants before validation.
	if err := applyBecknConstants(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("beckn constants: %w", err)
	}

	// Validate the configuration.
	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// validateConfig validates the configuration.
func validateConfig(cfg *Config) error {
	if strings.TrimSpace(cfg.AppName) == "" {
		return fmt.Errorf("missing app name")
	}
	if strings.TrimSpace(cfg.HTTP.Port) == "" {
		return fmt.Errorf("missing port")
	}
	return nil
}

// loadAppPlugin is a generic function to load and validate application-level plugins.
func loadAppPlugin[T any](ctx context.Context, name string, cfg *plugin.Config, mgrFunc func(context.Context, *plugin.Config) (T, error)) error {
	if cfg == nil {
		log.Debugf(ctx, "Skipping %s plugin: not configured", name)
		return nil
	}

	_, err := mgrFunc(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to load %s plugin (%s): %w", name, cfg.ID, err)
	}

	log.Debugf(ctx, "Loaded %s plugin: %s", name, cfg.ID)
	return nil
}

// initAppPlugins initializes application-level plugins including telemetry.
// This function is designed to be extensible for future plugin types.
func initAppPlugins(ctx context.Context, mgr *plugin.Manager, cfg ApplicationPlugins) error {
	if err := loadAppPlugin(ctx, "OtelSetup", cfg.OtelSetup, func(ctx context.Context, cfg *plugin.Config) (*telemetry.Provider, error) {
		return mgr.OtelSetup(ctx, cfg)
	}); err != nil {
		return fmt.Errorf("failed to initialize application plugins: %w", err)
	}

	return nil
}

// newServer creates and initializes the HTTP server.
func newServer(ctx context.Context, mgr handler.PluginManager, cfg *Config) (http.Handler, error) {
	mux := http.NewServeMux()

	if err := module.Register(ctx, cfg.Modules, mux, mgr); err != nil {
		return nil, fmt.Errorf("failed to register modules: %w", err)
	}
	return mux, nil
}

var newManagerFunc = plugin.NewManager
var newServerFunc = newServer

// run encapsulates the application logic.
func run(ctx context.Context, configPath string) error {
	closers := []func(){}
	// Initialize configuration and logger.
	cfg, err := initConfig(ctx, configPath)
	if err != nil {
		return fmt.Errorf("failed to initialize config: %w", err)
	}
	log.Infof(ctx, "Initializing logger with config: %+v", cfg.Log)
	if err := log.InitLogger(cfg.Log); err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}

	//to add the parent_id in the context value so it get passed to the logs
	ctx = addParentIdCtx(ctx, cfg)

	// Initialize plugin manager.
	log.Infof(ctx, "Initializing plugin manager")
	mgr, closer, err := newManagerFunc(ctx, cfg.PluginManager)
	if err != nil {
		return fmt.Errorf("failed to create plugin manager: %w", err)
	}
	closers = append(closers, closer)
	log.Debug(ctx, "Plugin manager loaded.")

	// Initialize plugins including telemetry.
	if err := initAppPlugins(ctx, mgr, cfg.Plugins); err != nil {
		return fmt.Errorf("failed to initialize plugins: %w", err)
	}

	// Initialize HTTP server.
	log.Infof(ctx, "Initializing HTTP server")
	srv, err := newServerFunc(ctx, mgr, cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize server: %w", err)
	}

	// Configure HTTP server.
	httpServer := &http.Server{
		Addr:         net.JoinHostPort("", cfg.HTTP.Port),
		Handler:      srv,
		ReadTimeout:  cfg.HTTP.Timeouts.Read * time.Second,
		WriteTimeout: cfg.HTTP.Timeouts.Write * time.Second,
		IdleTimeout:  cfg.HTTP.Timeouts.Idle * time.Second,
	}

	// Start HTTP server.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Infof(ctx, "Server listening on %s", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf(ctx, fmt.Errorf("http server ListenAndServe: %w", err), "error listening and serving")
		}
	}()

	// Handle shutdown.
	shutdown(ctx, httpServer, &wg, closers)
	wg.Wait()
	log.Infof(ctx, "Server shutdown complete")
	return nil
}

// shutdown handles server shutdown.
func shutdown(ctx context.Context, httpServer *http.Server, wg *sync.WaitGroup, closers []func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		log.Infof(ctx, "Shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Errorf(ctx, fmt.Errorf("http server Shutdown: %w", err), "error shutting down http server")
		}

		// Call all closer functions.
		for _, closer := range closers {
			closer()
		}
	}()
}

func addParentIdCtx(ctx context.Context, config *Config) context.Context {
	var parentID string
	var podName string

	if p := os.Getenv("POD_NAME"); p != "" {
		log.Infof(ctx, "Adding POD name: %s", p)
		podName = p
	} else {
		log.Info(ctx, "POD_NAME environment variable not set, falling back to hostname")
		if hostname, err := os.Hostname(); err == nil {
			log.Infof(ctx, "Setting POD name as hostname: %s", hostname)
			podName = hostname
		} else {
			log.Info(ctx, "failed to get POD name")
		}
	}

	for _, m := range config.Modules {
		if m.Handler.Role == "" || m.Handler.SubscriberID == "" {
			continue
		}
		candidate := string(m.Handler.Role) + ":" + m.Handler.SubscriberID + ":" + podName
		if parentID == "" {
			parentID = candidate
		} else if candidate != parentID {
			log.Warnf(ctx, "Multiple distinct role:subscriberID pairs found in modules (using %q, also saw %q); consider explicit parent_id config", parentID, candidate)
		}
	}

	if parentID != "" {
		ctx = context.WithValue(ctx, model.ContextKeyParentID, parentID)
	} else {
		log.Warnf(ctx, "Failed to find parent ID in config; add role and subscriber_id to the handler config")
	}
	return ctx
}

// applyBecknConstants loads the verified beckn constants and merges them into
// every plugin config in cfg. Locked keys that are contradicted by operator
// config cause a startup failure. Overridable keys that differ from the
// canonical value require an explicit becknConstantsOverrides declaration.
func applyBecknConstants(ctx context.Context, cfg *Config) error {
	disableRefresh := cfg.BecknConstants != nil && cfg.BecknConstants.DisableRemoteRefresh
	bc, err := beckndefaults.Load(ctx, disableRefresh)
	if err != nil {
		return err
	}

	overrides := cfg.BecknConstantsOverrides
	if overrides != nil && strings.TrimSpace(overrides.Reason) == "" {
		return fmt.Errorf("becknConstantsOverrides.reason must not be empty when overrides are declared")
	}

	for i := range cfg.Modules {
		modName := cfg.Modules[i].Name
		if err := mergeModulePlugins(ctx, modName, &cfg.Modules[i].Handler.Plugins, bc, overrides); err != nil {
			return fmt.Errorf("module %q: %w", modName, err)
		}
	}
	return nil
}

func mergeModulePlugins(ctx context.Context, modName string, plugins *handler.PluginCfg, bc *beckndefaults.BecknConstants, overrides *BecknConstantsOverrides) error {
	named := []*plugin.Config{
		plugins.Registry,
		plugins.SchemaValidator,
		plugins.SignValidator,
		plugins.Signer,
		plugins.Router,
		plugins.Cache,
		plugins.Publisher,
		plugins.KeyManager,
		plugins.ManifestLoader,
		plugins.TransportWrapper,
		plugins.PayloadStore,
		plugins.PolicyChecker,
		plugins.PayloadTransformer,
	}
	for _, cfg := range named {
		if cfg == nil {
			continue
		}
		if err := mergePlugin(ctx, cfg, bc, overrides); err != nil {
			return err
		}
	}
	for i := range plugins.Middleware {
		if err := mergePlugin(ctx, &plugins.Middleware[i], bc, overrides); err != nil {
			return err
		}
	}
	for i := range plugins.Steps {
		if err := mergePlugin(ctx, &plugins.Steps[i], bc, overrides); err != nil {
			return err
		}
	}
	return nil
}

func mergePlugin(ctx context.Context, cfg *plugin.Config, bc *beckndefaults.BecknConstants, overrides *BecknConstantsOverrides) error {
	if cfg == nil || cfg.ID == "" {
		return nil
	}
	if cfg.Config == nil {
		cfg.Config = make(map[string]string)
	}

	pluginID := cfg.ID

	// Locked keys: no override permitted, ever.
	if locked, ok := bc.Locked[pluginID]; ok {
		for key, canonical := range locked {
			if userVal, exists := cfg.Config[key]; exists && userVal != canonical {
				return fmt.Errorf("plugin %q: key %q is a locked beckn constant (value: %q) and cannot be overridden; remove it from your config",
					pluginID, key, canonical)
			}
			cfg.Config[key] = canonical
		}
	}

	// Overridable keys: inject if absent, require explicit declaration if different.
	if overridable, ok := bc.Overridable[pluginID]; ok {
		pluginOverrides := resolvePluginOverrides(overrides, pluginID)

		for key, canonical := range overridable {
			// schemav2validator.location is only governed when effective type is "url".
			if pluginID == "schemav2validator" && key == "location" {
				if resolveEffectiveType(cfg.Config, overridable, pluginOverrides) != "url" {
					continue
				}
			}

			if overrideVal, declared := pluginOverrides[key]; declared {
				cfg.Config[key] = overrideVal
				log.Warnf(ctx, "BecknConstants: constant overridden plugin=%q key=%q canonical=%q override=%q reason=%q",
					pluginID, key, canonical, overrideVal, overrides.Reason)
			} else if userVal, exists := cfg.Config[key]; exists && userVal != canonical {
				return fmt.Errorf("plugin %q: key %q differs from beckn canonical value %q; "+
					"add a becknConstantsOverrides block with a reason to declare this intentional",
					pluginID, key, canonical)
			} else {
				cfg.Config[key] = canonical
			}
		}
	}

	return nil
}

// resolveEffectiveType returns the active value of schemav2validator.type,
// giving priority to declared overrides, then user config, then the overridable default.
func resolveEffectiveType(userConfig, overridable, pluginOverrides map[string]string) string {
	if t, ok := pluginOverrides["type"]; ok {
		return t
	}
	if t, ok := userConfig["type"]; ok {
		return t
	}
	if t, ok := overridable["type"]; ok {
		return t
	}
	return "url"
}

// resolvePluginOverrides returns the per-plugin override map, or an empty map if none declared.
func resolvePluginOverrides(overrides *BecknConstantsOverrides, pluginID string) map[string]string {
	if overrides == nil || overrides.Plugins == nil {
		return map[string]string{}
	}
	if m, ok := overrides.Plugins[pluginID]; ok {
		return m
	}
	return map[string]string{}
}
