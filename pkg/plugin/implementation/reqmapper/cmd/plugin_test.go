package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProviderNew_ReturnsStep(t *testing.T) {
	step, closer, err := (provider{}).New(context.Background(), map[string]string{
		"role":         "bap",
		"mappingsFile": "../testdata/mappings.yaml",
	})
	require.NoError(t, err)
	require.Nil(t, closer)
	require.NotNil(t, step)
}

func TestProviderNew_MissingRole(t *testing.T) {
	_, _, err := (provider{}).New(context.Background(), map[string]string{
		"mappingsFile": "../testdata/mappings.yaml",
	})
	require.Error(t, err)
}

func TestProviderNew_InvalidRole(t *testing.T) {
	_, _, err := (provider{}).New(context.Background(), map[string]string{
		"role":         "invalid",
		"mappingsFile": "../testdata/mappings.yaml",
	})
	require.Error(t, err)
}
