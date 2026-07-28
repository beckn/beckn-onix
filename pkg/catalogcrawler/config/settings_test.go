package config

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
