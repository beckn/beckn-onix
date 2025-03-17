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

	"github.com/beckn/beckn-onix/core/pkg/log"
	"github.com/beckn/beckn-onix/plugin/definition"
)

type Manager struct {
	plugins map[string]*plugin.Plugin
	closers []func()
}

func validateMgrCfg(cfg *ManagerConfig) error {
	return nil
}

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

func plugins(ctx context.Context, cfg *ManagerConfig) (map[string]*plugin.Plugin, error) {
	plugins := make(map[string]*plugin.Plugin)

	err := filepath.WalkDir(cfg.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil // Skip directories
		}

		if strings.HasSuffix(d.Name(), ".so") {
			id := strings.TrimSuffix(d.Name(), ".so") // Extract plugin ID

			log.Debugf(ctx, "Loading plugin: %s", id)
			start := time.Now()
			p, err := plugin.Open(path) // Use the full path
			if err != nil {
				return fmt.Errorf("failed to open plugin %s: %w", id, err)
			}
			elapsed := time.Since(start)
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

func provider[T any](plugins map[string]*plugin.Plugin, id string) (T, error) {
	var zero T
	pgn, ok := plugins[id]
	if !ok {
		return zero, fmt.Errorf("plugin %s not found", id)
	}
	provider, err := pgn.Lookup("Provider")
	if err != nil {
		return zero, fmt.Errorf("failed to lookup Provider for %s: %w", id, err)
	}

	pp, ok := provider.(T)
	if !ok {
		return zero, fmt.Errorf("failed to cast Provider for %s", id)
	}
	return pp, nil
}

func path(root string, id string) string {
	return fmt.Sprintf("%s/%s.so", root, id)
}

// GetPublisher returns a Publisher instance based on the provided configuration.
// It reuses the loaded provider.
func (m *Manager) Publisher(ctx context.Context, cfg *Config) (definition.Publisher, error) {
	pp, err := provider[definition.PublisherProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	p, closer, err := pp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	m.addCloser(closer)
	return p, nil
}

func (m *Manager) addCloser(closer func()) {
	if closer != nil {
		m.closers = append(m.closers, closer)
	}
}

func (m *Manager) Validator(ctx context.Context, cfg *Config) (definition.SchemaValidator, error) {
	vp, err := provider[definition.SchemaValidatorProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	return vp.New(ctx, cfg.Config)
}

func (m *Manager) Router(ctx context.Context, cfg *Config) (definition.Router, error) {
	rp, err := provider[definition.RouterProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	return rp.New(ctx, cfg.Config)

}

func (m *Manager) Middleware(ctx context.Context, cfg *Config) (func(http.Handler) http.Handler, error) {
	mwp, err := provider[definition.MiddlewareProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	return mwp.New(ctx, cfg.Config)
}

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

func (m *Manager) Signer(ctx context.Context, cfg *Config) (definition.Signer, error) {
	sp, err := provider[definition.SignerProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	s, closer, err := sp.New(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	m.addCloser(closer)
	return s, nil
}

func (m *Manager) Encryptor(ctx context.Context, cfg *Config) (definition.Encryptor, error) {
	ep, err := provider[definition.EncryptorProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	return ep.New(ctx, cfg.Config)
}

func (m *Manager) Decryptor(ctx context.Context, cfg *Config) (definition.Decryptor, error) {
	dp, err := provider[definition.DecryptorProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	return dp.New(ctx, cfg.Config)
}

func (m *Manager) SignValidator(ctx context.Context, cfg *Config) (definition.SignValidator, error) {
	svp, err := provider[definition.SignValidatorProvider](m.plugins, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider for %s: %w", cfg.ID, err)
	}
	return svp.New(ctx, cfg.Config)
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
