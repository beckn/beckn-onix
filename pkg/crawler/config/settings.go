// Package config resolves the crawler's Settings from environment variables.
// Both the standalone driver and the onix plugin build a Settings; the
// composition root turns it into an engine.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Settings is the fully-resolved crawler configuration.
type Settings struct {
	DBDSN                string
	PushEndpoint         string
	IndexURLs            []string
	NetworkIDs           []string
	RegistryURL          string
	IndexInterval        time.Duration
	CatalogInterval      time.Duration
	FetchTimeout         time.Duration
	MaxArtifactBytes     int64
	MaxDecompressedBytes int64
	MaxAttempts          int
	MaxPushBytes         int64
	MergeOnly            bool
	BppURI               string
}

// LoadSettings resolves settings from a getenv function (injected so it is
// testable). Required: CRAWLER_DB_DSN, CRAWLER_PUSH_ENDPOINT, and a source
// (CRAWLER_INDEX_URLS or CRAWLER_REGISTRY_URL). Everything else has a default —
// nothing is hardcoded at a call site.
func LoadSettings(getenv func(string) string) (Settings, error) {
	s := Settings{
		DBDSN:                getenv("CRAWLER_DB_DSN"),
		PushEndpoint:         getenv("CRAWLER_PUSH_ENDPOINT"),
		IndexURLs:            splitCSV(getenv("CRAWLER_INDEX_URLS")),
		NetworkIDs:           splitCSV(getenv("CRAWLER_NETWORK_IDS")),
		RegistryURL:          strings.TrimSpace(getenv("CRAWLER_REGISTRY_URL")),
		BppURI:               strings.TrimSpace(getenv("CRAWLER_BPP_URI")),
		IndexInterval:        durOr(getenv("CRAWLER_INDEX_INTERVAL"), 5*time.Minute),
		CatalogInterval:      durOr(getenv("CRAWLER_CATALOG_INTERVAL"), 30*time.Second),
		FetchTimeout:         durOr(getenv("CRAWLER_FETCH_TIMEOUT"), 30*time.Second),
		MaxArtifactBytes:     int64Or(getenv("CRAWLER_MAX_ARTIFACT_BYTES"), 10<<20),
		MaxDecompressedBytes: int64Or(getenv("CRAWLER_MAX_DECOMPRESSED_BYTES"), 100<<20),
		MaxAttempts:          intOr(getenv("CRAWLER_MAX_ATTEMPTS"), 5),
		MaxPushBytes:         int64Or(getenv("CRAWLER_MAX_PUSH_BYTES"), 10<<20),
		// This version: always MERGE + build the delta from change files (FULL /
		// removals are deferred). Set CRAWLER_MERGE_ONLY=false to re-enable the
		// (dormant) mode-by-changeset FULL path.
		MergeOnly: getenv("CRAWLER_MERGE_ONLY") != "false",
	}

	// Clamp <= 0 to the default: a literal "0" parses fine (not a parse error),
	// so without this a "0" cap would silently reject everything.
	if s.MaxArtifactBytes <= 0 {
		s.MaxArtifactBytes = 10 << 20
	}
	if s.MaxDecompressedBytes <= 0 {
		s.MaxDecompressedBytes = 100 << 20
	}
	if s.MaxPushBytes <= 0 {
		s.MaxPushBytes = 10 << 20
	}
	// Durations: a literal "0s" parses fine, so clamp <= 0 to the default here (the
	// boundary). A 0 FetchTimeout means no HTTP deadline — dangerous when fetching
	// untrusted publisher URLs (slowloris); a 0 interval is a hot loop.
	if s.FetchTimeout <= 0 {
		s.FetchTimeout = 30 * time.Second
	}
	if s.IndexInterval <= 0 {
		s.IndexInterval = 5 * time.Minute
	}
	if s.CatalogInterval <= 0 {
		s.CatalogInterval = 30 * time.Second
	}
	if s.MaxAttempts <= 0 {
		s.MaxAttempts = 5
	}

	var missing []string
	if s.DBDSN == "" {
		missing = append(missing, "CRAWLER_DB_DSN")
	}
	if s.PushEndpoint == "" {
		missing = append(missing, "CRAWLER_PUSH_ENDPOINT")
	}
	if len(missing) > 0 {
		return Settings{}, fmt.Errorf("crawler: missing required config: %s", strings.Join(missing, ", "))
	}
	if len(s.IndexURLs) == 0 && s.RegistryURL == "" {
		return Settings{}, fmt.Errorf("crawler: a source is required (CRAWLER_INDEX_URLS or CRAWLER_REGISTRY_URL)")
	}
	return s, nil
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func durOr(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(s)); err == nil {
		return d
	}
	return def
}

func intOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

func int64Or(s string, def int64) int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
		return n
	}
	return def
}
