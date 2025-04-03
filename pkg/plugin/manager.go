package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"plugin"
	"strings"

	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"gopkg.in/yaml.v2"
)

// Config represents the plugin manager configuration.
type Config struct {
	Root      string       `yaml:"root"`
	Signer    PluginConfig `yaml:"signer"`
	Verifier  PluginConfig `yaml:"verifier"`
	Decrypter PluginConfig `yaml:"decrypter"`
	Encrypter PluginConfig `yaml:"encrypter"`
	Publisher PluginConfig `yaml:"publisher"`
	Registery PluginConfig `yaml:"registery"`
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
	dp  definition.DecrypterProvider
	ep  definition.EncrypterProvider
	pb  definition.PublisherProvider
	rp  definition.RegistryLookupProvider
	cfg *Config
}

// NewManager initializes a new Manager with the given configuration file.
func NewManager(ctx context.Context, cfg *Config) (*Manager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration cannot be nil")
	}

	// Load signer plugin.
	sp, err := provider[definition.SignerProvider](cfg.Root, cfg.Signer.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load signer plugin: %w", err)
	}

	// Load publisher plugin.
	pb, err := provider[definition.PublisherProvider](cfg.Root, cfg.Publisher.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load publisher plugin: %w", err)
	}

	// Load verifier plugin.
	vp, err := provider[definition.VerifierProvider](cfg.Root, cfg.Verifier.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load Verifier plugin: %w", err)
	}

	// Load decrypter plugin.
	dp, err := provider[definition.DecrypterProvider](cfg.Root, cfg.Decrypter.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load Decrypter plugin: %w", err)
	}

	// Load encryption plugin.
	ep, err := provider[definition.EncrypterProvider](cfg.Root, cfg.Encrypter.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load encryption plugin: %w", err)
	}
	// Load encryption plugin.
	rp, err := provider[definition.RegistryLookupProvider](cfg.Root, cfg.Registery.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load registery plugin: %w", err)
	}

	return &Manager{sp: sp, vp: vp, pb: pb, ep: ep, dp: dp, rp: rp, cfg: cfg}, nil
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

// Decrypter retrieves the decryption plugin instance.
func (m *Manager) Decrypter(ctx context.Context) (definition.Decrypter, func() error, error) {
	if m.dp == nil {
		return nil, nil, fmt.Errorf("decrypter plugin provider not loaded")
	}

	decrypter, close, err := m.dp.New(ctx, m.cfg.Decrypter.Config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize Decrypter: %w", err)
	}
	return decrypter, close, nil
}

// Encrypter retrieves the encryption plugin instance.
func (m *Manager) Encrypter(ctx context.Context) (definition.Encrypter, func() error, error) {
	if m.ep == nil {
		return nil, nil, fmt.Errorf("encryption plugin provider not loaded")
	}

	encrypter, close, err := m.ep.New(ctx, m.cfg.Encrypter.Config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize encrypter: %w", err)
	}
	return encrypter, close, nil
}

// Publisher retrieves the publisher plugin instance.
func (m *Manager) Publisher(ctx context.Context) (definition.Publisher, error) {
	if m.pb == nil {
		return nil, fmt.Errorf("publisher plugin provider not loaded")
	}

	publisher, err := m.pb.New(ctx, m.cfg.Publisher.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize publisher: %w", err)
	}
	return publisher, nil
}

// RegistryLookup retrieves the registryLookup plugin instance.
func (m *Manager) RegistryLookup(ctx context.Context) (definition.RegistryLookup, func() error, error) {
	if m.rp == nil {
		return nil, nil, fmt.Errorf("registry lookup plugin provider not loaded")
	}
	registry, close, err := m.rp.New(ctx, m.cfg.Registery.Config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize registry lookup: %w", err)
	}
	return registry, close, nil
}

// LoadConfig loads the configuration from a YAML file.
func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var cfg Config
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to decode config file: %w", err)
	}

	return &cfg, nil
}
