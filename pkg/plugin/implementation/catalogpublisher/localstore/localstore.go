// Package localstore is the shared "write publish results to a local
// directory, read them back as prior state" logic both
// cmd/catalogpublisherctl and the catalogPublish HTTP handler
// (core/module/handler/catalogPublishHandler.go) use. catalogpublisher.
// Publish itself holds no storage-backed state (see
// definition.PriorCatalogState's doc comment) -- this package is one
// concrete, filesystem-backed way to supply and persist that state, not
// part of the core plugin's own logic.
//
// Layout, matching the file spec's well-known path and this project's own
// established conventions:
//
//	<root>/
//	  .well-known/
//	    dedi.index.json           # the manifest -- NOT written by this package right now, see Write
//	  index/
//	    becknCatalogs.index.json  # the catalog index
//	  catalogs/
//	    <localName>.v<version>.json            # a baseline
//	    changes/
//	      <localName>.v<version>.changes.json  # a change file
//
// Where these files end up being served from (a real domain, a CDN) is a
// separate, later concern -- this package only ever reads/writes <root>
// on local disk; "moving them to a certain location" is left to whatever
// deployment step runs after a publish.
package localstore

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogfile"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// ManifestFilename is the manifest's real filename per the decentralized-
// catalog file spec ("The manifest at /.well-known/dedi.index.json").
// Despite the filename, this is NOT the catalog index (IndexFilename,
// below) -- a naming coincidence, not a hint that they're the same file.
const ManifestFilename = "dedi.index.json"

// IndexFilename is the catalog index's filename, matching the manifest's
// files[].name value ("becknCatalogs" -- catalogIndexFileName in
// catalogpublisher.go).
const IndexFilename = "becknCatalogs.index.json"

// CatalogsDirName is the root subdirectory holding every catalog's
// versioned files, flat -- not one subdirectory per catalogId, matching
// the file spec's own example URLs (all catalog files for a domain sit
// under one shared path).
const CatalogsDirName = "catalogs"

// IndexDirName is the root subdirectory holding the catalog index.
const IndexDirName = "index"

// ChangesDirName is the catalogs subdirectory holding change files,
// separate from baseline files which sit directly under catalogs/.
const ChangesDirName = "changes"

// ManifestPath, IndexPath, and the catalogs directory, all relative to
// root.
func ManifestPath(root string) string { return filepath.Join(root, ".well-known", ManifestFilename) }
func IndexPath(root string) string    { return filepath.Join(root, IndexDirName, IndexFilename) }
func CatalogsDir(root string) string  { return filepath.Join(root, CatalogsDirName) }
func ChangesDir(root string) string   { return filepath.Join(CatalogsDir(root), ChangesDirName) }

// EnsureDirs creates every directory Write will need under root.
//
// .well-known/ (ManifestPath's directory) is deliberately not created here:
// this package does not touch the manifest file at all for now (see Write).
func EnsureDirs(root string) error {
	for _, dir := range []string{filepath.Dir(IndexPath(root)), CatalogsDir(root), ChangesDir(root)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("localstore: creating %s: %w", dir, err)
		}
	}
	return nil
}

// LocalName returns catalogID with any "domain/" prefix stripped, matching
// catalogpublisher's own filename convention (catalogId
// "open-economy.nfh.global/electronics-2026" -> "electronics-2026").
func LocalName(catalogID string) string {
	if i := strings.LastIndex(catalogID, "/"); i != -1 {
		return catalogID[i+1:]
	}
	return catalogID
}

// CatalogFilePath returns the local path for one catalog file: a baseline
// (suffix "json", or gzip's "json.gz") sits directly under catalogs/, a
// change file (suffix "changes.json"/"changes.json.gz") under
// catalogs/changes/ -- the suffix must match the compression this file was
// actually written/served with (NFH-014 §10.1), same as its declared URL.
func CatalogFilePath(root, catalogID string, version int, suffix string) string {
	filename := fmt.Sprintf("%s.v%d.%s", LocalName(catalogID), version, suffix)
	if strings.HasPrefix(suffix, "changes.json") {
		return filepath.Join(ChangesDir(root), filename)
	}
	return filepath.Join(CatalogsDir(root), filename)
}

// LatestFilePath returns the local path for a catalog's "latest" pointer
// (NFH-014 §Schema Changes): unlike CatalogFilePath, its filename carries
// no version number -- Write overwrites this same path in place on every
// publish, matching latest's explicit exemption from the immutable-URL
// rule every other catalog file here follows. suffix is "json" or gzip's
// "json.gz", matching the compression this file was actually served with.
func LatestFilePath(root, catalogID, suffix string) string {
	return filepath.Join(CatalogsDir(root), fmt.Sprintf("%s.latest.%s", LocalName(catalogID), suffix))
}

// Write persists a PublishResult under root: the index and every catalog
// outcome's new content (baseline or change file; a no-op outcome writes
// nothing).
//
// The manifest is deliberately NOT written here for now -- we do not want
// this package touching .well-known/dedi.index.json at all, read or write,
// until there's a real ArtifactStore/manifest-ownership story. result.
// Manifest is still computed by catalogpublisher.Publish (it's part of
// PublishResult), it's just not persisted by this package.
func Write(root string, result definition.PublishResult) error {
	if err := EnsureDirs(root); err != nil {
		return err
	}
	if err := os.WriteFile(IndexPath(root), result.Index, 0o644); err != nil {
		return fmt.Errorf("localstore: writing index: %w", err)
	}
	for _, outcome := range result.Catalogs {
		if outcome.Content == nil {
			continue
		}
		suffix := "json"
		if outcome.Mode == "change" {
			suffix = "changes.json"
		}
		if outcome.Compressed {
			suffix += ".gz"
		}
		served := outcome.ServedContent
		if served == nil {
			served = outcome.Content // Compressed is false: ServedContent and Content are identical
		}
		path := CatalogFilePath(root, outcome.CatalogID, outcome.Version, suffix)
		if err := os.WriteFile(path, served, 0o644); err != nil {
			return fmt.Errorf("localstore: writing %s: %w", path, err)
		}
	}
	for _, outcome := range result.Catalogs {
		if outcome.LatestContent == nil {
			continue
		}
		suffix := "json"
		if outcome.Compressed {
			suffix += ".gz"
		}
		served := outcome.LatestServedContent
		if served == nil {
			served = outcome.LatestContent
		}
		path := LatestFilePath(root, outcome.CatalogID, suffix)
		if err := os.WriteFile(path, served, 0o644); err != nil {
			return fmt.Errorf("localstore: writing %s: %w", path, err)
		}
	}
	for _, rl := range result.RetiredLatest {
		suffix := "json"
		if rl.Compressed {
			suffix += ".gz"
		}
		served := rl.ServedContent
		if served == nil {
			served = rl.Content
		}
		path := LatestFilePath(root, rl.CatalogID, suffix)
		if err := os.WriteFile(path, served, 0o644); err != nil {
			return fmt.Errorf("localstore: writing %s: %w", path, err)
		}
	}
	return nil
}

// State is what Load reconstructs from root, ready to drop straight into
// a definition.PublishRequest.
type State struct {
	PriorState   map[string]definition.PriorCatalogState
	CarryForward []json.RawMessage
}

// wire types mirror the subset of the catalog index's shape this package
// needs to read back (duplicated rather than imported from
// catalogpublisher: this is a wire-format contract, not Go code the two
// should share, matching the convention already used elsewhere in this
// project). No top-level index version (NFH-014, "There is no whole-index
// version field").
type indexDoc struct {
	Catalogs []json.RawMessage `json:"catalogs"`
}

type indexEntry struct {
	CatalogID    string                `json:"catalogId"`
	EntryVersion int                   `json:"entryVersion"`
	CatalogType  string                `json:"catalogType"`
	Dependencies *wireDependencies     `json:"dependencies"`
	NetworkIds   []string              `json:"networkIds"`
	SchemaTypes  []string              `json:"schemaTypes"`
	IsActive     *bool                 `json:"isActive"`
	Baseline     *wireFileEntry        `json:"baseline"`
	Changes      []wireChangeFileEntry `json:"changes"`
	Latest       *wireFileEntry        `json:"latest"`
	RetiredAt    *string               `json:"retiredAt"`
	CrawlHint    string                `json:"crawlHint"`
}

type wireDependencies struct {
	Masters []wireMasterDependency `json:"masters"`
}

type wireMasterDependency struct {
	CatalogID string `json:"catalogId"`
	Version   int    `json:"version"`
	IndexURL  string `json:"indexUrl"`
}

// wireFileEntry is baseline/latest's shape -- one file-lineage version.
type wireFileEntry struct {
	Version int    `json:"version"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	Digest  string `json:"digest"`
}

// wireChangeFileEntry is a changes[] entry's shape -- a fromVersion/
// toVersion range, mirroring CatalogChangeFile's own fields (NFH-014
// §Versioning), not a single version.
type wireChangeFileEntry struct {
	FromVersion int    `json:"fromVersion"`
	ToVersion   int    `json:"toVersion"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	Digest      string `json:"digest"`
}

// wireCatalogFile unwraps a stored baseline file's self-signed envelope
// (catalogpublisher's catalogFileDoc: {catalogId, version, next_update,
// catalog, signature}) back to the bare catalog content Apply/diffing
// expect, plus NextUpdate -- used to decide when a compaction's grace
// period (NFH-014 CON-TBD-32) has elapsed, see reconstructState. No
// signature verification on read for now -- this package only ever reads
// back its own previously-written output, not externally-fetched content.
type wireCatalogFile struct {
	NextUpdate time.Time       `json:"next_update"`
	Catalog    json.RawMessage `json:"catalog"`
}

// Load reads the previously-written catalog index (if any) under root and
// reconstructs: PriorCatalogState for each of catalogIDs that has
// publishable prior state (absent from the map means "new, start a fresh
// baseline"), every other catalog's raw entry to carry forward unmodified,
// and the index's own last-published version. Reads the index once
// regardless of how many catalogIDs are requested.
func Load(root string, catalogIDs []string) (*State, error) {
	state := &State{PriorState: map[string]definition.PriorCatalogState{}}

	wanted := make(map[string]bool, len(catalogIDs))
	for _, id := range catalogIDs {
		wanted[id] = true
	}

	raw, err := os.ReadFile(IndexPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	} else if err != nil {
		return nil, fmt.Errorf("localstore: reading existing index: %w", err)
	}

	var doc indexDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("localstore: parsing existing index: %w", err)
	}

	for _, rawEntry := range doc.Catalogs {
		var probe struct {
			CatalogID string `json:"catalogId"`
		}
		if json.Unmarshal(rawEntry, &probe) != nil {
			continue
		}
		if wanted[probe.CatalogID] {
			var entry indexEntry
			if err := json.Unmarshal(rawEntry, &entry); err != nil {
				return nil, fmt.Errorf("localstore: parsing entry for %s: %w", probe.CatalogID, err)
			}
			if entry.RetiredAt != nil || entry.Baseline == nil {
				continue // no publishable prior state; this run starts a fresh baseline
			}
			prior, err := reconstructState(root, entry)
			if err != nil {
				return nil, err
			}
			state.PriorState[probe.CatalogID] = *prior
			continue
		}
		state.CarryForward = append(state.CarryForward, rawEntry)
	}
	return state, nil
}

func reconstructState(root string, entry indexEntry) (*definition.PriorCatalogState, error) {
	baselineSuffix := "json"
	if isGzipURL(entry.Baseline.URL) {
		baselineSuffix = "json.gz"
	}
	baselinePath := CatalogFilePath(root, entry.CatalogID, entry.Baseline.Version, baselineSuffix)
	baselineBytes, err := os.ReadFile(baselinePath)
	if err != nil {
		return nil, fmt.Errorf("localstore: reading %s: %w", baselinePath, err)
	}
	if baselineSuffix == "json.gz" {
		if baselineBytes, err = gunzip(baselineBytes); err != nil {
			return nil, fmt.Errorf("localstore: decompressing %s: %w", baselinePath, err)
		}
	}
	var wrapped wireCatalogFile
	if err := json.Unmarshal(baselineBytes, &wrapped); err != nil {
		return nil, fmt.Errorf("localstore: parsing %s: %w", baselinePath, err)
	}

	// A change file's Version is only ever <= the current baseline's own
	// Version right after a forced re-baseline (compaction): every other
	// baseline is created once, before any of its changes[], so all of
	// its changes carry strictly higher versions. Such a "superseded"
	// change's content is already folded into the baseline itself --
	// applying it again is a harmless no-op (upserts/removals/attribute
	// overlays are all idempotent, id-keyed replacements, not diffs
	// relative to a specific prior state) -- but NFH-014 CON-TBD-32 only
	// requires it to stay *listed* for one full next_update cycle after
	// the compaction, measured against the next_update stamped on the new
	// baseline at compaction time (wrapped.NextUpdate here). Once that's
	// elapsed, dropping it from the returned ChangeFiles is what actually
	// realizes compaction's index-payload benefit -- Publish itself never
	// does this trimming on its own (PriorCatalogState's doc comment).
	gracePeriodElapsed := time.Now().After(wrapped.NextUpdate)

	effective := wrapped.Catalog
	changeFiles := make([]definition.FileRef, 0, len(entry.Changes))
	for _, ch := range entry.Changes {
		chSuffix := "changes.json"
		if isGzipURL(ch.URL) {
			chSuffix = "changes.json.gz"
		}
		path := CatalogFilePath(root, entry.CatalogID, ch.ToVersion, chSuffix)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("localstore: reading %s: %w", path, err)
		}
		if chSuffix == "changes.json.gz" {
			if raw, err = gunzip(raw); err != nil {
				return nil, fmt.Errorf("localstore: decompressing %s: %w", path, err)
			}
		}
		effective, err = catalogfile.Apply(effective, raw)
		if err != nil {
			return nil, fmt.Errorf("localstore: applying %s: %w", path, err)
		}
		superseded := ch.ToVersion <= entry.Baseline.Version
		if superseded && gracePeriodElapsed {
			continue // grace period over: stop listing this pre-compaction change file
		}
		changeFiles = append(changeFiles, toChangeFileRef(ch))
	}

	isActive := true
	if entry.IsActive != nil {
		isActive = *entry.IsActive
	}
	baselineRef := toFileRef(*entry.Baseline)
	return &definition.PriorCatalogState{
		Catalog:         effective,
		BaselineFile:    &baselineRef,
		ChangeFiles:     changeFiles,
		EntryVersion:    entry.EntryVersion,
		CatalogType:     entry.CatalogType,
		NetworkIds:      entry.NetworkIds,
		SchemaTypes:     entry.SchemaTypes,
		IsActive:        isActive,
		Dependencies:    toMasterDependencies(entry.Dependencies),
		CrawlHint:       entry.CrawlHint,
		LatestPublished: entry.Latest != nil,
	}, nil
}

func toMasterDependencies(deps *wireDependencies) []definition.MasterDependency {
	if deps == nil || len(deps.Masters) == 0 {
		return nil
	}
	out := make([]definition.MasterDependency, len(deps.Masters))
	for i, m := range deps.Masters {
		out[i] = definition.MasterDependency{CatalogID: m.CatalogID, Version: m.Version, IndexURL: m.IndexURL}
	}
	return out
}

// isGzipURL reports whether a stored file reference's declared URL is
// gzip-compressed (NFH-014 §10.1: signaled purely by a ".gz" URL
// extension) -- read back from the entry itself rather than from the
// current Config.Gzip, since a catalog's files may have been published
// under a different compression setting than whatever is running now.
func isGzipURL(url string) bool { return strings.HasSuffix(url, ".gz") }

// gunzip decompresses data written by catalogpublisher's own gzip.Writer.
func gunzip(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func toFileRef(fe wireFileEntry) definition.FileRef {
	return definition.FileRef{
		Version: fe.Version,
		URL:     fe.URL,
		Size:    fe.Size,
		Digest:  fe.Digest,
	}
}

// toChangeFileRef converts a changes[] entry back to a definition.FileRef:
// only ToVersion is kept (matching FileRef's single Version field) --
// FromVersion is always reconstructible from the sequence itself (see
// catalogpublisher.changeFileRefsToWire), so it never needs to round-trip.
func toChangeFileRef(ch wireChangeFileEntry) definition.FileRef {
	return definition.FileRef{
		Version: ch.ToVersion,
		URL:     ch.URL,
		Size:    ch.Size,
		Digest:  ch.Digest,
	}
}
