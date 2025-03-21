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

	"github.com/beckn/beckn-onix/core/module"
	"github.com/beckn/beckn-onix/core/pkg/log"
	"github.com/beckn/beckn-onix/plugin"
	"github.com/beckn/beckn-onix/plugin/definition"
	"gopkg.in/yaml.v2"
)

// config struct holds all configurations.
type config struct {
	AppName       string                `yaml:"appName"`
	Log           log.Config            `yaml:"log"`
	PluginManager *plugin.ManagerConfig `yaml:"pluginManager"`
	Modules       []module.Config       `yaml:"modules"`
	HTTP          httpConfig            `yaml:"http"`
}

type httpConfig struct {
	Port    string        `yaml:"port"`
	Timeout timeoutConfig `yaml:"timeout"`
}

type timeoutConfig struct {
	Read  time.Duration `yaml:"read"`
	Write time.Duration `yaml:"write"`
	Idle  time.Duration `yaml:"idle"`
}

type pluginManager interface {
	Middleware(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error)
	SignValidator(ctx context.Context, cfg *plugin.Config) (definition.SignValidator, error)
	Validator(ctx context.Context, cfg *plugin.Config) (definition.SchemaValidator, error)
	Router(ctx context.Context, cfg *plugin.Config) (definition.Router, error)
	Publisher(ctx context.Context, cfg *plugin.Config) (definition.Publisher, error)
	Signer(ctx context.Context, cfg *plugin.Config) (definition.Signer, error)
	Step(ctx context.Context, cfg *plugin.Config) (definition.Step, error)
}

var configPath string

func main() {
	// Define and parse command-line flags.
	flag.StringVar(&configPath, "config", "../../config/clientSideHandler-config.yaml", "Path to the configuration file")
	flag.Parse()

	// Use custom log for initial setup messages.
	log.Infof(context.Background(), "Starting application with config: %s", configPath)
	// Run the application within a context.
	if err := run(context.Background(), configPath); err != nil {
		log.Fatalf(context.Background(), err, "Application failed: %v", err)
	}
	log.Infof(context.Background(), "Application finished")
}

// initConfig loads and validates the configuration.
func initConfig(ctx context.Context, path string) (*config, error) {
	// Open the configuration file.
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open config file: %w", err)
	}
	defer file.Close()

	// Decode the YAML configuration.
	var cfg config
	if err := yaml.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("could not decode config: %w", err)
	}
	log.Debugf(ctx, "Read config: %#v", cfg)
	// Validate the configuration.
	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)

	}

	return &cfg, nil
}

// validateConfig validates the configuration.
func validateConfig(cfg *config) error {
	if strings.TrimSpace(cfg.AppName) == "" {
		return fmt.Errorf("missing app name")
	}
	if strings.TrimSpace(cfg.HTTP.Port) == "" {
		return fmt.Errorf("missing port")
	}
	return nil
}

// newServer creates and initializes the HTTP server.
func newServer(ctx context.Context, mgr pluginManager, cfg *config) (http.Handler, error) {
	mux := http.NewServeMux()
	err := module.Register(ctx, cfg.Modules, mux, mgr)
	if err != nil {
		return nil, fmt.Errorf("failed to register modules: %w", err)
	}
	return mux, nil
}

// run encapsulates the application logic.
func run(ctx context.Context, configPath string) error {
	closers := []func(){}
	// Initialize configuration and logger.
	cfg, err := initConfig(ctx, configPath)
	if err != nil {
		return fmt.Errorf("failed to initialize config: %w", err)
	}
	log.Infof(ctx, "Initializing logger with config: %+v", cfg.Log)
	log.InitLogger(cfg.Log)

	// Initialize plugin manager.
	log.Infof(ctx, "Initializing plugin manager")
	var mgr pluginManager
	mgr, closer, err := plugin.NewManager(ctx, cfg.PluginManager)
	if err != nil {
		return fmt.Errorf("failed to create plugin manager: %w", err)
	}
	closers = append(closers, closer)
	log.Debugf(ctx, "Got manage: %#v", mgr)
	// Initialize HTTP server.
	log.Infof(ctx, "Initializing HTTP server")
	srv, err := newServer(ctx, mgr, cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize server: %w", err)
	}
	// Configure HTTP server.
	httpServer := &http.Server{
		Addr:         net.JoinHostPort("", cfg.HTTP.Port),
		Handler:      srv,
		ReadTimeout:  cfg.HTTP.Timeout.Read * time.Second, // Use timeouts from config
		WriteTimeout: cfg.HTTP.Timeout.Write * time.Second,
		IdleTimeout:  cfg.HTTP.Timeout.Idle * time.Second,
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
