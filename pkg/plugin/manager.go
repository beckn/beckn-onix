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

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

type onixPlugin interface {
	Lookup(string) (plugin.Symbol, error)
}

// Manager is responsible for managing dynamically loaded plugins.
type Manager struct {
	plugins map[string]onixPlugin // plugins holds the dynamically loaded plugins.
	closers []func()              // closers contains functions to release resources when the manager is closed.
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

	closers := []func(){}
	return &Manager{plugins: plugins, closers: closers}, func() {
		for _, closer := range closers {
			closer()
		}
	}, nil
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
