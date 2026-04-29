package handler

import (
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"github.com/stretchr/testify/assert"
)

func cfg(id string) *plugin.Config { return &plugin.Config{ID: id} }

func TestPluginEntries_AllNil(t *testing.T) {
	p := PluginCfg{}
	assert.Empty(t, p.PluginEntries(), "all-nil config should return no entries")
}

func TestPluginEntries_EmptyIDSkipped(t *testing.T) {
	p := PluginCfg{
		Router:  &plugin.Config{ID: ""},
		Signer:  &plugin.Config{ID: ""},
		Steps:   []plugin.Config{{ID: ""}},
		Middleware: []plugin.Config{{ID: ""}},
	}
	assert.Empty(t, p.PluginEntries(), "configs with empty ID should be skipped")
}

func TestPluginEntries_AllNamedSlots(t *testing.T) {
	p := PluginCfg{
		SchemaValidator:  cfg("schemav2validator"),
		SignValidator:    cfg("beckn_sign_validator"),
		Router:           cfg("static_router"),
		Registry:         cfg("dediregistry"),
		Publisher:        cfg("kafka"),
		Signer:           cfg("ed25519_signer"),
		Cache:            cfg("redis"),
		TransportWrapper: cfg("http_wrapper"),
		PolicyChecker:    cfg("opa_policy"),
		KeyManager:       cfg("beckn_key_mgr"),
	}
	entries := p.PluginEntries()
	assert.Len(t, entries, 10)

	byType := make(map[string]string)
	for _, e := range entries {
		byType[e.Type] = e.ID
	}
	assert.Equal(t, "schemav2validator", byType["schema_validator"])
	assert.Equal(t, "beckn_sign_validator", byType["sign_validator"])
	assert.Equal(t, "static_router", byType["router"])
	assert.Equal(t, "dediregistry", byType["registry"])
	assert.Equal(t, "kafka", byType["publisher"])
	assert.Equal(t, "ed25519_signer", byType["signer"])
	assert.Equal(t, "redis", byType["cache"])
	assert.Equal(t, "http_wrapper", byType["transport_wrapper"])
	assert.Equal(t, "opa_policy", byType["policy_checker"])
	assert.Equal(t, "beckn_key_mgr", byType["key_manager"])
}

func TestPluginEntries_StepsAndMiddleware(t *testing.T) {
	p := PluginCfg{
		Steps: []plugin.Config{
			{ID: "step_one"},
			{ID: ""},
			{ID: "step_two"},
		},
		Middleware: []plugin.Config{
			{ID: "mw_one"},
			{ID: "mw_two"},
		},
	}
	entries := p.PluginEntries()
	assert.Len(t, entries, 4, "empty-ID step should be omitted; 2 steps + 2 middleware = 4")

	var steps, mws []telemetry.PluginEntry
	for _, e := range entries {
		switch e.Type {
		case "step":
			steps = append(steps, e)
		case "middleware":
			mws = append(mws, e)
		}
	}
	assert.Len(t, steps, 2)
	assert.Len(t, mws, 2)
	assert.Equal(t, "step_one", steps[0].ID)
	assert.Equal(t, "step_two", steps[1].ID)
	assert.Equal(t, "mw_one", mws[0].ID)
	assert.Equal(t, "mw_two", mws[1].ID)
}
