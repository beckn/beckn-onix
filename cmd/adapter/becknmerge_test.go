package main

import (
	"context"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/beckndefaults"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpers

func makeConstants(locked, overridable map[string]map[string]string) *beckndefaults.BecknConstants {
	return &beckndefaults.BecknConstants{
		Version:     "v1",
		Locked:      locked,
		Overridable: overridable,
	}
}

func pluginCfg(id string, cfg map[string]string) *plugin.Config {
	return &plugin.Config{ID: id, Config: cfg}
}

// --- mergePlugin: locked keys ---

func TestMergePlugin_LockedKey_Injected(t *testing.T) {
	cfg := pluginCfg("dediregistry", map[string]string{})
	bc := makeConstants(
		map[string]map[string]string{"dediregistry": {"url": "https://locked.example.com"}},
		nil,
	)
	require.NoError(t, mergePlugin(context.Background(), cfg, bc, nil))
	assert.Equal(t, "https://locked.example.com", cfg.Config["url"])
}

func TestMergePlugin_LockedKey_MatchingValue_NoError(t *testing.T) {
	cfg := pluginCfg("dediregistry", map[string]string{"url": "https://locked.example.com"})
	bc := makeConstants(
		map[string]map[string]string{"dediregistry": {"url": "https://locked.example.com"}},
		nil,
	)
	require.NoError(t, mergePlugin(context.Background(), cfg, bc, nil))
	assert.Equal(t, "https://locked.example.com", cfg.Config["url"])
}

func TestMergePlugin_LockedKey_ConflictingValue_Fails(t *testing.T) {
	cfg := pluginCfg("dediregistry", map[string]string{"url": "https://operator-supplied.example.com"})
	bc := makeConstants(
		map[string]map[string]string{"dediregistry": {"url": "https://locked.example.com"}},
		nil,
	)
	err := mergePlugin(context.Background(), cfg, bc, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "locked beckn constant")
	assert.Contains(t, err.Error(), "url")
}

func TestMergePlugin_UnknownPlugin_NoChange(t *testing.T) {
	cfg := pluginCfg("cache", map[string]string{"addr": "redis:6379"})
	bc := makeConstants(
		map[string]map[string]string{"dediregistry": {"url": "https://example.com"}},
		nil,
	)
	require.NoError(t, mergePlugin(context.Background(), cfg, bc, nil))
	assert.Equal(t, map[string]string{"addr": "redis:6379"}, cfg.Config)
}

// --- mergePlugin: overridable keys ---

func TestMergePlugin_OverridableKey_Injected(t *testing.T) {
	cfg := pluginCfg("schemav2validator", map[string]string{})
	bc := makeConstants(nil, map[string]map[string]string{
		"schemav2validator": {"type": "url", "location": "https://spec.example.com"},
	})
	require.NoError(t, mergePlugin(context.Background(), cfg, bc, nil))
	assert.Equal(t, "url", cfg.Config["type"])
	assert.Equal(t, "https://spec.example.com", cfg.Config["location"])
}

func TestMergePlugin_OverridableKey_DifferentWithoutDeclaration_Fails(t *testing.T) {
	cfg := pluginCfg("schemav2validator", map[string]string{"type": "url", "location": "https://other.example.com"})
	bc := makeConstants(nil, map[string]map[string]string{
		"schemav2validator": {"type": "url", "location": "https://spec.example.com"},
	})
	err := mergePlugin(context.Background(), cfg, bc, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location")
	assert.Contains(t, err.Error(), "becknConstantsOverrides")
}

func TestMergePlugin_OverridableKey_DeclaredOverride_Accepted(t *testing.T) {
	cfg := pluginCfg("schemav2validator", map[string]string{})
	bc := makeConstants(nil, map[string]map[string]string{
		"schemav2validator": {"type": "url", "location": "https://spec.example.com"},
	})
	overrides := &BecknConstantsOverrides{
		Reason: "test override",
		Plugins: map[string]map[string]string{
			"schemav2validator": {"location": "https://mirror.example.com"},
		},
	}
	require.NoError(t, mergePlugin(context.Background(), cfg, bc, overrides))
	assert.Equal(t, "https://mirror.example.com", cfg.Config["location"])
}

// --- location conditional on type ---

func TestMergePlugin_LocationNotInjected_WhenTypeIsFile(t *testing.T) {
	cfg := pluginCfg("schemav2validator", map[string]string{})
	bc := makeConstants(nil, map[string]map[string]string{
		"schemav2validator": {"type": "url", "location": "https://spec.example.com"},
	})
	overrides := &BecknConstantsOverrides{
		Reason: "local schema",
		Plugins: map[string]map[string]string{
			"schemav2validator": {"type": "file"},
		},
	}
	require.NoError(t, mergePlugin(context.Background(), cfg, bc, overrides))
	assert.Equal(t, "file", cfg.Config["type"])
	assert.Empty(t, cfg.Config["location"], "location must not be injected when type is file")
}

func TestMergePlugin_LocationNotGoverned_WhenUserSetTypeFile(t *testing.T) {
	cfg := pluginCfg("schemav2validator", map[string]string{"type": "file", "location": "/app/schema.yaml"})
	bc := makeConstants(nil, map[string]map[string]string{
		"schemav2validator": {"type": "url", "location": "https://spec.example.com"},
	})
	// type differs from canonical — needs an override declaration
	overrides := &BecknConstantsOverrides{
		Reason: "local schema",
		Plugins: map[string]map[string]string{
			"schemav2validator": {"type": "file"},
		},
	}
	require.NoError(t, mergePlugin(context.Background(), cfg, bc, overrides))
	assert.Equal(t, "file", cfg.Config["type"])
	assert.Equal(t, "/app/schema.yaml", cfg.Config["location"])
}

// --- resolveEffectiveType ---

func TestResolveEffectiveType_OverrideTakesPrecedence(t *testing.T) {
	assert.Equal(t, "file",
		resolveEffectiveType(
			map[string]string{"type": "url"},
			map[string]string{"type": "url"},
			map[string]string{"type": "file"},
		))
}

func TestResolveEffectiveType_UserConfigSecond(t *testing.T) {
	assert.Equal(t, "file",
		resolveEffectiveType(
			map[string]string{"type": "file"},
			map[string]string{"type": "url"},
			map[string]string{},
		))
}

func TestResolveEffectiveType_OverridableDefault(t *testing.T) {
	assert.Equal(t, "url",
		resolveEffectiveType(
			map[string]string{},
			map[string]string{"type": "url"},
			map[string]string{},
		))
}

func TestResolveEffectiveType_FallbackToURL(t *testing.T) {
	assert.Equal(t, "url",
		resolveEffectiveType(
			map[string]string{},
			map[string]string{},
			map[string]string{},
		))
}

// --- applyBecknConstants: override reason validation ---

func TestApplyBecknConstants_OverrideWithoutReason_Fails(t *testing.T) {
	cfg := &Config{
		AppName: "test",
		HTTP:    httpConfig{Port: "8080"},
		BecknConstantsOverrides: &BecknConstantsOverrides{
			Reason: "", // empty — should fail
		},
	}
	err := applyBecknConstants(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reason")
}

func TestApplyBecknConstants_NoModules_NoError(t *testing.T) {
	cfg := &Config{
		AppName: "test",
		HTTP:    httpConfig{Port: "8080"},
		BecknConstants: &BecknConstantsConfig{DisableRemoteRefresh: true},
	}
	require.NoError(t, applyBecknConstants(context.Background(), cfg))
}
