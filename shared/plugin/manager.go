package plugin

import (
	"context"
	"fmt"
	"path/filepath"
	"plugin"
	"strings"

	"github.com/beckn/beckn-onix/shared/plugin/definition"
)

// Config represents the plugin manager configuration.
type Config struct {
	Root      string       `yaml:"root"`
	Signer    PluginConfig `yaml:"signer"`
	Verifier  PluginConfig `yaml:"verifier"`
	Encrypter PluginConfig `yaml:"encrypter"`
}

// PluginConfig represents configuration details for a plugin.
type PluginConfig struct {
	ID     string            `yaml:"id"`
	Config map[string]string `yaml:"config"`
}

// Manager handles dynamic plugin loading and management.
type Manager struct {
	sp  definition.SignerProvider
	vp  definition.VerifierProvider
	enc definition.EncrypterProvider
	cfg *Config
}

// NewManager initializes a new Manager with the given configuration file.
func NewManager(ctx context.Context, cfg *Config) (*Manager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration cannot be nil")
	}

	// Load signer plugin
	sp, err := provider[definition.SignerProvider](cfg.Root, cfg.Signer.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load signer plugin: %w", err)
	}

	// Load verifier plugin
	vp, err := provider[definition.VerifierProvider](cfg.Root, cfg.Verifier.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load Verifier plugin: %w", err)
	}

	// Load encryption plugin
	enc, err := provider[definition.EncrypterProvider](cfg.Root, cfg.Encrypter.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load encryption plugin: %w", err)
	}

	return &Manager{sp: sp, vp: vp, enc: enc, cfg: cfg}, nil
}

// provider loads a plugin dynamically and retrieves its provider instance.
func provider[T any](root, id string) (T, error) {
	var zero T
	if len(strings.TrimSpace(id)) == 0 {
		return zero, nil
	}

	p, err := plugin.Open(pluginPath(root, id))
	if err != nil {
		return zero, fmt.Errorf("failed to open plugin %s: %w", id, err)
	}

	symbol, err := p.Lookup("Provider")
	if err != nil {
		return zero, fmt.Errorf("failed to find Provider symbol in plugin %s: %w", id, err)
	}

	prov, ok := symbol.(*T)
	if !ok {
		return zero, fmt.Errorf("failed to cast Provider for %s", id)
	}

	return *prov, nil
}

// pluginPath constructs the path to the plugin shared object file.
func pluginPath(root, id string) string {
	return filepath.Join(root, id+".so")
}

// Signer retrieves the signing plugin instance.
func (m *Manager) Signer(ctx context.Context) (definition.Signer, func() error, error) {
	if m.sp == nil {
		return nil, nil, fmt.Errorf("signing plugin provider not loaded")
	}

	signer, close, err := m.sp.New(ctx, m.cfg.Signer.Config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize signer: %w", err)
	}
	return signer, close, nil
}

// Verifier retrieves the verification plugin instance.
func (m *Manager) Verifier(ctx context.Context) (definition.Verifier, func() error, error) {
	if m.vp == nil {
		return nil, nil, fmt.Errorf("Verifier plugin provider not loaded")
	}

	Verifier, close, err := m.vp.New(ctx, m.cfg.Verifier.Config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize Verifier: %w", err)
	}
	return Verifier, close, nil
}

// Encrypter retrieves the encryption plugin instance.
func (m *Manager) Encrypter(ctx context.Context) (definition.Encrypter, func() error, error) {
	if m.enc == nil {
		return nil, nil, fmt.Errorf("encryption plugin provider not loaded")
	}

	encrypter, close, err := m.enc.New(ctx, m.cfg.Encrypter.Config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize encrypter: %w", err)
	}
	return encrypter, close, nil
}
