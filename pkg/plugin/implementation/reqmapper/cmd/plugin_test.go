package main

import (
	"context"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/jsonatatranslator"
	"github.com/stretchr/testify/require"
)

func testTranslator(t *testing.T) definition.Translator {
	t.Helper()
	translator, _, err := jsonatatranslator.New(context.Background(), nil)
	require.NoError(t, err)
	return translator
}

func TestProvider_SymbolType(t *testing.T) {
	// Verify the exported Provider symbol satisfies PayloadTransformerProvider.
	// This mirrors the type assertion the plugin manager performs at runtime.
	var _ definition.PayloadTransformerProvider = Provider
}

func TestProviderNew_ReturnsStep(t *testing.T) {
	step, closer, err := (provider{}).New(context.Background(), testTranslator(t), map[string]string{
		"role":         "bap",
		"mappingsFile": "../testdata/mappings.yaml",
	})
	require.NoError(t, err)
	require.Nil(t, closer)
	require.NotNil(t, step)
}

func TestProviderNew_MissingRole(t *testing.T) {
	_, _, err := (provider{}).New(context.Background(), testTranslator(t), map[string]string{
		"mappingsFile": "../testdata/mappings.yaml",
	})
	require.Error(t, err)
}

func TestProviderNew_InvalidRole(t *testing.T) {
	_, _, err := (provider{}).New(context.Background(), testTranslator(t), map[string]string{
		"role":         "invalid",
		"mappingsFile": "../testdata/mappings.yaml",
	})
	require.Error(t, err)
}
