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

// DefaultStoreProvider is the persistence backend used when
// CRAWLER_STORE_PROVIDER is unset. It must name a backend registered in the
// store package; the factory rejects an unknown name and lists what it has.
const DefaultStoreProvider = "postgres"

// Settings is the fully-resolved crawler configuration. A disabled crawler
// resolves to the zero value with Enabled false: nothing else is required,
// because nothing else is used.
type Settings struct {
	Enabled       bool
	StoreProvider string
	DBDSN         string
	PushEndpoint  string
	IndexURLs     []string
	NetworkIDs    []string
	// RegistryURL, when set, is the DeDi registry base URL that enables the
	// registry-backed index-discovery source: the crawler asks the registry which
	// providers publish a catalog index for each of NetworkIDs, instead of reading
	// a fixed IndexURLs list. It is an alternative source, not an addition.
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

// Enabled reports whether the operator has turned the crawler on
// (CRAWLER_ENABLED, default false). It is the gate a driver checks before
// anything else: a crawler that is off requires no DSN, no endpoint and no
// source, so an operator who does not run it is unaffected by its config.
func Enabled(getenv func(string) string) bool {
	return boolOr(getenv("CRAWLER_ENABLED"), false)
}

// Load is the driver entry point: it applies the enable gate first and only
// resolves (and demands) the run-time config when the crawler is enabled. A
// disabled crawler yields Settings{Enabled: false} and no error.
func Load(getenv func(string) string) (Settings, error) {
	if !Enabled(getenv) {
		return Settings{Enabled: false}, nil
	}
	s, err := LoadSettings(getenv)
	if err != nil {
		return Settings{}, err
	}
	s.Enabled = true
	return s, nil
}

// LoadSettings resolves the settings an enabled crawler needs to run, from a
// getenv function (injected so it is testable). Required: CRAWLER_DB_DSN,
// CRAWLER_PUSH_ENDPOINT, and a source (CRAWLER_INDEX_URLS). Everything else has
// a default — nothing is hardcoded at a call site. Drivers call Load, which
// gates on Enabled first.
//
// There is deliberately NO signing-key configuration. Catalog file signatures
// are verified against the publisher's public key in the network registry, the
// same channel the transport signature path uses, so key distribution is a
// network concern and never a per-deployment one.
func LoadSettings(getenv func(string) string) (Settings, error) {
	// This version: always MERGE + build the delta from change files (FULL /
	// removals are deferred). Set CRAWLER_MERGE_ONLY=false to re-enable the
	// (dormant) mode-by-changeset FULL path.
	mergeOnly, err := boolOrErr(getenv("CRAWLER_MERGE_ONLY"), true)
	if err != nil {
		return Settings{}, fmt.Errorf("crawler: CRAWLER_MERGE_ONLY: %w", err)
	}

	s := Settings{
		StoreProvider:        strOr(getenv("CRAWLER_STORE_PROVIDER"), DefaultStoreProvider),
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
		MergeOnly:            mergeOnly,
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
	// A source is required, and there are two: a static index-URL list, or a
	// registry base URL with at least one network to query. A bare
	// CRAWLER_REGISTRY_URL with no CRAWLER_NETWORK_IDS is not a usable source (it
	// names a registry but nothing to look up in it), so it does not satisfy this
	// on its own.
	hasConfigSource := len(s.IndexURLs) > 0
	hasRegistrySource := s.RegistryURL != "" && len(s.NetworkIDs) > 0
	if !hasConfigSource && !hasRegistrySource {
		return Settings{}, fmt.Errorf("crawler: a source is required (set CRAWLER_INDEX_URLS, or CRAWLER_REGISTRY_URL with CRAWLER_NETWORK_IDS)")
	}
	return s, nil
}

func strOr(s, def string) string {
	if v := strings.TrimSpace(s); v != "" {
		return v
	}
	return def
}

func boolOr(s string, def bool) bool {
	if b, err := strconv.ParseBool(strings.TrimSpace(s)); err == nil {
		return b
	}
	return def
}

// boolOrErr parses a boolean setting strictly: absent or blank means def, a
// value strconv.ParseBool accepts means that value, and anything else is an
// error naming what was given.
//
// It exists because CRAWLER_MERGE_ONLY used to be `getenv(...) != "false"`, so
// "0", "no", "off" and "False" all silently meant TRUE while every other
// boolean in this file went through ParseBool. An operator who typed
// CRAWLER_MERGE_ONLY=0 to turn merge-only off got merge-only on, with no
// warning. Refusing garbage at startup is the only way that mistake is visible.
func boolOrErr(s string, def bool) (bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false, fmt.Errorf("%q is not a boolean (use true/false, 1/0, t/f, T/F, TRUE/FALSE)", s)
	}
	return b, nil
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
