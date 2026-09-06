package main

import (
	"context"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/stretchr/testify/require"
)

func TestProvider_SymbolType(t *testing.T) {
	// Verify the exported Provider symbol satisfies TranslatorProvider. This
	// mirrors the type assertion the plugin manager performs at runtime.
	var _ definition.TranslatorProvider = Provider
}

func TestProvider_New_ReturnsTranslator(t *testing.T) {
	translator, closer, err := Provider.New(context.Background(), map[string]string{})
	require.NoError(t, err)
	require.NotNil(t, translator)
	require.NotNil(t, closer)
	require.NoError(t, closer())
}
