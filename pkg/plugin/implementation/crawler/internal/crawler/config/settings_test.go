package config

// settings_test.go — verifies LoadSettings: required-field validation
// (DSN, push endpoint, at-least-one-source), CSV splitting/trimming, env
// overrides, and that defaults are applied when env vars are absent.

import (
	"testing"
	"time"
)

func TestLoadSettings(t *testing.T) {
	env := map[string]string{
		"CRAWLER_DB_DSN":         "postgres://u:p@h/db",
		"CRAWLER_PUSH_ENDPOINT":  "https://d/push",
		"CRAWLER_INDEX_URLS":     "https://a/i , https://b/i",
		"CRAWLER_NETWORK_IDS":    "net1,net2",
		"CRAWLER_INDEX_INTERVAL": "2m",
	}
	s, err := LoadSettings(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if s.DBDSN != "postgres://u:p@h/db" || s.PushEndpoint != "https://d/push" {
		t.Fatalf("required fields = %+v", s)
	}
	if len(s.IndexURLs) != 2 || s.IndexURLs[0] != "https://a/i" || s.IndexURLs[1] != "https://b/i" {
		t.Fatalf("IndexURLs = %v (want trimmed 2)", s.IndexURLs)
	}
	if len(s.NetworkIDs) != 2 {
		t.Fatalf("NetworkIDs = %v", s.NetworkIDs)
	}
	if s.IndexInterval != 2*time.Minute {
		t.Fatalf("IndexInterval = %v, want 2m", s.IndexInterval)
	}
	// defaults
	if s.CatalogInterval == 0 || s.FetchTimeout == 0 || s.MaxArtifactBytes == 0 || s.MaxAttempts == 0 {
		t.Fatalf("defaults not applied: %+v", s)
	}
}

func TestLoadSettings_MissingRequired(t *testing.T) {
	if _, err := LoadSettings(func(string) string { return "" }); err == nil {
		t.Fatal("expected error for missing required config")
	}
	// DSN + endpoint present but no source -> error
	env := map[string]string{"CRAWLER_DB_DSN": "x", "CRAWLER_PUSH_ENDPOINT": "y"}
	if _, err := LoadSettings(func(k string) string { return env[k] }); err == nil {
		t.Fatal("expected error when no source configured")
	}
}

// TestLoadSettings_RegistrySource covers the registry-backed source: a registry
// base URL plus at least one network is now an accepted source on its own (no
// CRAWLER_INDEX_URLS), a bare registry URL with no networks is not, and neither
// source configured still fails.
func TestLoadSettings_RegistrySource(t *testing.T) {
	const base = "https://fabric.nfh.global/registry/dedi"

	// Registry URL + networks, no index URLs -> accepted.
	ok := map[string]string{
		"CRAWLER_DB_DSN":        "postgres://u:p@h/db",
		"CRAWLER_PUSH_ENDPOINT": "https://d/push",
		"CRAWLER_REGISTRY_URL":  base,
		"CRAWLER_NETWORK_IDS":   "beckn.one/testnet",
	}
	s, err := LoadSettings(func(k string) string { return ok[k] })
	if err != nil {
		t.Fatalf("registry source should be accepted: %v", err)
	}
	if s.RegistryURL != base {
		t.Fatalf("RegistryURL = %q, want %q", s.RegistryURL, base)
	}
	if len(s.NetworkIDs) != 1 || s.NetworkIDs[0] != "beckn.one/testnet" {
		t.Fatalf("NetworkIDs = %v", s.NetworkIDs)
	}
	if len(s.IndexURLs) != 0 {
		t.Fatalf("IndexURLs = %v, want none", s.IndexURLs)
	}

	// Registry URL but no networks -> not a usable source.
	noNets := map[string]string{
		"CRAWLER_DB_DSN":        "postgres://u:p@h/db",
		"CRAWLER_PUSH_ENDPOINT": "https://d/push",
		"CRAWLER_REGISTRY_URL":  base,
	}
	if _, err := LoadSettings(func(k string) string { return noNets[k] }); err == nil {
		t.Fatal("expected error: a registry URL with no networks is not a source")
	}

	// Neither source configured -> still an error.
	neither := map[string]string{
		"CRAWLER_DB_DSN":        "postgres://u:p@h/db",
		"CRAWLER_PUSH_ENDPOINT": "https://d/push",
	}
	if _, err := LoadSettings(func(k string) string { return neither[k] }); err == nil {
		t.Fatal("expected error when no source configured")
	}
}
