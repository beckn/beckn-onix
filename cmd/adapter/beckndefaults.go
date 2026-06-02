package main

import (
	"context"
	"fmt"
	"strings"

	becknconfig "github.com/beckn-one/beckn-onix/pkg/config"
	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/plugin"

	"github.com/beckn-one/beckn-onix/core/module/handler"
)

// applyBecknConstants loads the verified beckn constants and merges them into
// every plugin config in cfg. Locked keys that are contradicted by operator
// config cause a startup failure. Overridable keys that differ from the
// canonical value require an explicit becknConstantsOverrides declaration.
func applyBecknConstants(ctx context.Context, cfg *Config) error {
	disableRefresh := cfg.BecknConstants != nil && cfg.BecknConstants.DisableRemoteRefresh
	bc, err := becknconfig.Load(ctx, disableRefresh)
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

func mergeModulePlugins(ctx context.Context, modName string, plugins *handler.PluginCfg, bc *becknconfig.BecknConstants, overrides *BecknConstantsOverrides) error {
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

func mergePlugin(ctx context.Context, cfg *plugin.Config, bc *becknconfig.BecknConstants, overrides *BecknConstantsOverrides) error {
	if cfg == nil || cfg.ID == "" {
		return nil
	}
	if cfg.Config == nil {
		cfg.Config = make(map[string]string)
	}

	pluginID := cfg.ID

	// --- Locked keys: no override permitted, ever ---
	if locked, ok := bc.Locked[pluginID]; ok {
		for key, canonical := range locked {
			if userVal, exists := cfg.Config[key]; exists && userVal != canonical {
				return fmt.Errorf("plugin %q: key %q is a locked beckn constant (value: %q) and cannot be overridden; remove it from your config",
					pluginID, key, canonical)
			}
			cfg.Config[key] = canonical
		}
	}

	// --- Overridable keys: inject if absent, require explicit declaration if different ---
	if overridable, ok := bc.Overridable[pluginID]; ok {
		pluginOverrides := resolvePluginOverrides(overrides, pluginID)

		for key, canonical := range overridable {
			// For schemav2validator.location: only apply when effective type is "url"
			if pluginID == "schemav2validator" && key == "location" {
				if effectiveType(cfg.Config, overridable, pluginOverrides) != "url" {
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

// effectiveType resolves the active value of schemav2validator.type, giving
// priority to declared overrides, then the user config map, then the overridable default.
func effectiveType(userConfig, overridable, pluginOverrides map[string]string) string {
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
