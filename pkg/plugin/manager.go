package plugin

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"plugin"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/beckndefaults"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
)

type onixPlugin interface {
	Lookup(string) (plugin.Symbol, error)
}

// Manager is responsible for managing dynamically loaded plugins.
type Manager struct {
	plugins        map[string]onixPlugin        // plugins holds the dynamically loaded plugins.
	closers        []func()                     // closers contains functions to release resources when the manager is closed.
	constants      *beckndefaults.BecknConstants // loaded and verified at init; nil if not configured.
	overridesByKey map[string]telemetry.ConstantsOverride // keyed by "pluginID:key"; populated lazily at plugin creation time.
}

func validateMgrCfg(cfg *ManagerConfig) error {
	if cfg.Root == "" {
		return fmt.Errorf("root path cannot be empty")
	}
	return nil
}

// NewManager initializes a new Manager instance by loading plugins from the specified configuration.
func NewManager(ctx context.Context, cfg *ManagerConfig) (*Manager, func(), error) {
	if err := validateMgrCfg(cfg); err != nil {
		return nil, nil, fmt.Errorf("Invalid config: %w", err)
	}
	log.Debugf(ctx, "RemoteRoot : %s", cfg.RemoteRoot)
	if len(cfg.RemoteRoot) != 0 {
		log.Debugf(ctx, "Unzipping files from  : %s to : %s", cfg.RemoteRoot, cfg.Root)
		if err := unzip(cfg.RemoteRoot, cfg.Root); err != nil {
			return nil, nil, err
		}
	}
	plugins, err := plugins(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	disableRefresh := cfg.BecknConstants != nil && cfg.BecknConstants.DisableRemoteRefresh
	constants, err := beckndefaults.Load(ctx, disableRefresh)
	if err != nil {
		return nil, nil, fmt.Errorf("beckn constants: %w", err)
	}

	closers := []func(){}
	return &Manager{
		plugins:        plugins,
		closers:        closers,
		constants:      constants,
		overridesByKey: make(map[string]telemetry.ConstantsOverride),
	}, func() {
		for _, closer := range closers {
			closer()
		}
	}, nil
}

// applyConstants enforces beckn constants for the given plugin config.
// Locked keys: injected; startup fails if user config contradicts.
// Overridable keys: injected if absent; accepted with WARN if user set a different value.
// The schemav2validator.location key is only governed when effective type is "url".
func (m *Manager) applyConstants(ctx context.Context, cfg *Config) error {
	if m.constants == nil || cfg == nil || cfg.ID == "" {
		return nil
	}
	if cfg.Config == nil {
		cfg.Config = make(map[string]string)
	}
	pluginID := cfg.ID

	if locked, ok := m.constants.Locked[pluginID]; ok {
		for key, canonical := range locked {
			if userVal, exists := cfg.Config[key]; exists && userVal != canonical {
				return fmt.Errorf("plugin %q: key %q is a locked beckn constant (canonical: %q); remove it from your config",
					pluginID, key, canonical)
			}
			cfg.Config[key] = canonical
		}
	}

	if overridable, ok := m.constants.Overridable[pluginID]; ok {
		effectiveType := resolveEffectiveType(pluginID, cfg.Config, overridable)
		for key, canonical := range overridable {
			if pluginID == "schemav2validator" && key == "location" && effectiveType != "url" {
				continue
			}
			if userVal, exists := cfg.Config[key]; exists && userVal != canonical {
				log.Warnf(ctx, "BecknConstants: plugin=%q key=%q running non-canonical value (canonical=%q actual=%q)",
					pluginID, key, canonical, userVal)
				m.overridesByKey[pluginID+":"+key] = telemetry.ConstantsOverride{
					PluginID: pluginID, Key: key, Canonical: canonical, Actual: userVal,
				}
			} else if !exists {
				cfg.Config[key] = canonical
			}
		}
	}
	return nil
}

// resolveEffectiveType returns the active value of schemav2validator.type,
// used to decide whether to govern the location key.
func resolveEffectiveType(pluginID string, userConfig, overridable map[string]string) string {
	if pluginID != "schemav2validator" {
		return ""
	}
	if t, ok := userConfig["type"]; ok && t != "" {
		return t
	}
	if t, ok := overridable["type"]; ok && t != "" {
		return t
	}
	return "url"
}

// RegisterBecknConstantsGauge registers the beckn_constants_info observable gauge
// using all non-canonical constant values detected so far.
// Call once after all modules are initialised.
func (m *Manager) RegisterBecknConstantsGauge(ctx context.Context) error {
	overrides := make([]telemetry.ConstantsOverride, 0, len(m.overridesByKey))
	for _, o := range m.overridesByKey {
		overrides = append(overrides, o)
	}
	return telemetry.RegisterBecknConstantsInfo(ctx, overrides)
}

func plugins(ctx context.Context, cfg *ManagerConfig) (map[string]onixPlugin, error) {
	plugins := make(map[string]onixPlugin)

	err := filepath.WalkDir(cfg.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil // Skip directories
		}

		if strings.HasSuffix(d.Name(), ".so") {
			id := strings.TrimSuffix(d.Name(), ".so") // Extract plugin ID
			p, elapsed, err := loadPlugin(ctx, path, id)
			if err != nil {
				return err
			}
			plugins[id] = p
			log.Debugf(ctx, "Loaded plugin: %s in %s", id, elapsed)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return plugins, nil
}

// loadPlugin attempts to load a plugin from the given path and logs the execution time.
func loadPlugin(ctx context.Context, path, id string) (onixPlugin, time.Duration, error) {
	log.Debugf(ctx, "Loading plugin: %s", id)
	start := time.Now()

	p, err := plugin.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open plugin %s: %w", id, err)
	}

	elapsed := time.Since(start)
	return p, elapsed, nil
}

func provider[T any](plugins map[string]onixPlugin, id string) (T, error) {
	var zero T
	pgn, ok := plugins[id]
	if !ok {
		return zero, fmt.Errorf("plugin %s not found", id)
	}
	provider, err := pgn.Lookup("Provider")
	if err != nil {
		return zero, fmt.Errorf("failed to lookup Provider for %s: %w", id, err)
	}
	log.Debugf(context.Background(), "Provider type: %T\n", provider)

	pp, ok := provider.(T)
	if !ok {
		return zero, fmt.Errorf("failed to cast Provider for %s", id)
	}
	log.Debugf(context.Background(), "Casting successful for: %s", provider)
	return pp, nil
}

// Publisher returns a Publisher instance based on the provided configuration.
// It reuses the loaded provider and registers a cleanup function.
func (m *Manager) Publisher(ctx context.Context, cfg *Config) (definition.Publisher, error) {
	pp, err := provider[definition.PublisherProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	p, closer, err := pp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return p, nil
}

// SchemaValidator returns a SchemaValidator instance based on the provided configuration.
// It registers a cleanup function for resource management.
func (m *Manager) SchemaValidator(ctx context.Context, cfg *Config) (definition.SchemaValidator, error) {
	if err := m.applyConstants(ctx, cfg); err != nil {
		return nil, err
	}
	vp, err := provider[definition.SchemaValidatorProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	v, closer, err := vp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return v, nil
}

// Router returns a Router instance based on the provided configuration.
// It registers a cleanup function for resource management.
func (m *Manager) Router(ctx context.Context, cfg *Config) (definition.Router, error) {
	rp, err := provider[definition.RouterProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	router, closer, err := rp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return router, nil
}

// Middleware returns an HTTP middleware function based on the provided configuration.
func (m *Manager) Middleware(ctx context.Context, cfg *Config) (func(http.Handler) http.Handler, error) {
	mwp, err := provider[definition.MiddlewareProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	return mwp.New(ctx, cfg.Config)
}

// OtelSetup initializes OpenTelemetry via a dedicated plugin. The plugin is expected to return a telemetry Provider that the core application can use for instrumentation.
func (m *Manager) OtelSetup(ctx context.Context, cfg *Config) (*telemetry.Provider, error) {
	if cfg == nil {
		log.Info(ctx, "Telemetry config not provided; skipping OpenTelemetry setup")
		return nil, nil
	}

	otp, err := provider[definition.OtelSetupMetricsProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	provider, closer, err := otp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				log.Errorf(context.Background(), err, "Failed to shutdown telemetry provider")
			}
		})
	}
	return provider, nil
}

// TransportWrapper returns a TransportWrapper instance based on the provided configuration.
func (m *Manager) TransportWrapper(ctx context.Context, cfg *Config) (definition.TransportWrapper, error) {
	twp, err := provider[definition.TransportWrapperProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}

	config := make(map[string]any, len(cfg.Config))
	for k, v := range cfg.Config {
		config[k] = v
	}

	wrapper, closer, err := twp.New(ctx, config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, closer)
	}
	return wrapper, nil
}

// Step returns a Step instance based on the provided configuration.
func (m *Manager) Step(ctx context.Context, cfg *Config) (definition.Step, error) {
	sp, err := provider[definition.StepProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	step, closer, error := sp.New(ctx, cfg.Config)
	if closer != nil {
		m.closers = append(m.closers, closer)
	}
	return step, error
}

// PolicyChecker returns a PolicyChecker instance based on the provided configuration.
// It registers a cleanup function for resource management.
func (m *Manager) PolicyChecker(ctx context.Context, manifestLoader definition.ManifestLoader, cfg *Config) (definition.PolicyChecker, error) {
	pp, err := provider[definition.PolicyCheckerProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	checker, closer, err := pp.New(ctx, manifestLoader, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, closer)
	}
	return checker, nil
}

// Cache returns a Cache instance based on the provided configuration.
// It registers a cleanup function for resource management.
func (m *Manager) Cache(ctx context.Context, cfg *Config) (definition.Cache, error) {
	cp, err := provider[definition.CacheProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	c, closer, err := cp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return c, nil
}

// Signer returns a Signer instance based on the provided configuration.
// It registers a cleanup function for resource management.
func (m *Manager) Signer(ctx context.Context, cfg *Config) (definition.Signer, error) {
	sp, err := provider[definition.SignerProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	s, closer, err := sp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return s, nil
}

// Encryptor returns an Encrypter instance based on the provided configuration.
// It registers a cleanup function for resource management.
func (m *Manager) Encryptor(ctx context.Context, cfg *Config) (definition.Encrypter, error) {
	ep, err := provider[definition.EncrypterProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	encrypter, closer, err := ep.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return encrypter, nil
}

// Decryptor returns a Decrypter instance based on the provided configuration.
// It registers a cleanup function for resource management.
func (m *Manager) Decryptor(ctx context.Context, cfg *Config) (definition.Decrypter, error) {
	dp, err := provider[definition.DecrypterProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}

	decrypter, closer, err := dp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}

	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}

	return decrypter, nil
}

// SignValidator returns a SignValidator instance based on the provided configuration.
// It registers a cleanup function for resource management.
func (m *Manager) SignValidator(ctx context.Context, cfg *Config) (definition.SignValidator, error) {
	svp, err := provider[definition.SignValidatorProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	v, closer, err := svp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return v, nil
}

// KeyManager returns a KeyManager instance based on the provided configuration.
// It reuses the loaded provider.
func (m *Manager) KeyManager(ctx context.Context, cache definition.Cache, rClient definition.RegistryLookup, cfg *Config) (definition.KeyManager, error) {

	kmp, err := provider[definition.KeyManagerProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	km, closer, err := kmp.New(ctx, cache, rClient, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return km, nil
}

// KeyManager returns a KeyManager instance based on the provided configuration.
// It reuses the loaded provider.
func (m *Manager) SimpleKeyManager(ctx context.Context, cache definition.Cache, rClient definition.RegistryLookup, cfg *Config) (definition.KeyManager, error) {

	kmp, err := provider[definition.KeyManagerProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	km, closer, err := kmp.New(ctx, cache, rClient, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return km, nil
}

// PayloadStore returns a PayloadStore instance backed by the provided cache.
// namespace should be the module name of the owning handler to scope all cache keys.
func (m *Manager) PayloadStore(ctx context.Context, cache definition.Cache, namespace string, cfg *Config) (definition.PayloadStore, error) {
	pp, err := provider[definition.PayloadStoreProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	ps, closer, err := pp.New(ctx, cache, namespace, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return ps, nil
}

// ManifestLoader returns a ManifestLoader instance based on the provided configuration.
func (m *Manager) ManifestLoader(ctx context.Context, cache definition.Cache, lookup definition.RegistryMetadataLookup, cfg *Config) (definition.ManifestLoader, error) {
	mlp, err := provider[definition.ManifestLoaderProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	loader, closer, err := mlp.New(ctx, cache, lookup, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return loader, nil
}

// Registry returns a RegistryLookup instance based on the provided configuration.
// It registers a cleanup function for resource management.
func (m *Manager) Registry(ctx context.Context, cfg *Config) (definition.RegistryLookup, error) {
	if err := m.applyConstants(ctx, cfg); err != nil {
		return nil, err
	}
	rp, err := provider[definition.RegistryLookupProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	registry, closer, err := rp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return registry, nil
}

// DeDiRegistry returns a RegistryLookup instance based on the provided configuration.
// It reuses the loaded provider.
func (m *Manager) DeDiRegistry(ctx context.Context, cfg *Config) (definition.RegistryLookup, error) {
	rp, err := provider[definition.RegistryLookupProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	registry, closer, err := rp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		m.closers = append(m.closers, func() {
			if err := closer(); err != nil {
				panic(err)
			}
		})
	}
	return registry, nil
}

// Validator implements handler.PluginManager.
func (m *Manager) Validator(ctx context.Context, cfg *Config) (definition.SchemaValidator, error) {
	panic("unimplemented")
}

// Unzip extracts a ZIP file to the specified destination
func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	// Ensure the destination directory exists
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}

	for _, f := range r.File {

		fpath := filepath.Join(dest, f.Name)
		// Ensure directory exists
		log.Debugf(context.Background(), "Pain : fpath: %s,filepath.Dir(fpath): %s", fpath, filepath.Dir(fpath))
		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}
		// Open the file inside the zip
		srcFile, err := f.Open()
		if err != nil {
			return err
		}
		defer srcFile.Close()

		// Create the destination file
		dstFile, err := os.Create(fpath)
		if err != nil {
			return err
		}
		defer dstFile.Close()

		// Copy file contents
		if _, err := io.Copy(dstFile, srcFile); err != nil {
			return err
		}
	}

	return nil
}
