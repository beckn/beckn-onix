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
//	    dedi.index.json           # the manifest
//	  dedi/
//	    becknCatalogs.index.json  # the catalog index
//	  catalogs/
//	    <localName>.v<version>.json           # a baseline
//	    <localName>.v<version>.changes.json   # a change file
//
// Where these files end up being served from (a real domain, a CDN) is a
// separate, later concern -- this package only ever reads/writes <root>
// on local disk; "moving them to a certain location" is left to whatever
// deployment step runs after a publish.
package localstore

import (
	"encoding/json"
	"errors"
	"fmt"
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

// ManifestPath, IndexPath, and the catalogs directory, all relative to
// root.
func ManifestPath(root string) string { return filepath.Join(root, ".well-known", ManifestFilename) }
func IndexPath(root string) string    { return filepath.Join(root, "dedi", IndexFilename) }
func CatalogsDir(root string) string  { return filepath.Join(root, CatalogsDirName) }

// EnsureDirs creates every directory Write will need under root.
func EnsureDirs(root string) error {
	for _, dir := range []string{filepath.Dir(ManifestPath(root)), filepath.Dir(IndexPath(root)), CatalogsDir(root)} {
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

// CatalogFilePath returns the local path for one catalog file (a baseline
// or a change file).
func CatalogFilePath(root, catalogID string, version int, suffix string) string {
	return filepath.Join(CatalogsDir(root), fmt.Sprintf("%s.v%d.%s", LocalName(catalogID), version, suffix))
}

// Write persists a PublishResult under root: the manifest, the index, and
// every catalog outcome's new content (baseline or change file; a no-op
// outcome writes nothing).
func Write(root string, result definition.PublishResult) error {
	if err := EnsureDirs(root); err != nil {
		return err
	}
	if err := os.WriteFile(ManifestPath(root), result.Manifest, 0o644); err != nil {
		return fmt.Errorf("localstore: writing manifest: %w", err)
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
		path := CatalogFilePath(root, outcome.CatalogID, outcome.Version, suffix)
		if err := os.WriteFile(path, outcome.Content, 0o644); err != nil {
			return fmt.Errorf("localstore: writing %s: %w", path, err)
		}
	}
	return nil
}

// State is what Load reconstructs from root, ready to drop straight into
// a definition.PublishRequest.
type State struct {
	PriorState        map[string]definition.PriorCatalogState
	CarryForward      []json.RawMessage
	PriorIndexVersion int
}

// wire types mirror the subset of the catalog index's shape this package
// needs to read back (duplicated rather than imported from
// catalogpublisher: this is a wire-format contract, not Go code the two
// should share, matching the convention already used elsewhere in this
// project).
type indexDoc struct {
	Version  int               `json:"version"`
	Catalogs []json.RawMessage `json:"catalogs"`
}

type indexEntry struct {
	CatalogID string          `json:"catalogId"`
	Status    string          `json:"status"`
	Baseline  *wireFileEntry  `json:"baseline"`
	Changes   []wireFileEntry `json:"changes"`
}

type wireFileEntry struct {
	Version   int    `json:"version"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Digest    string `json:"digest"`
	Signature struct {
		KeyID      string    `json:"keyId"`
		Value      string    `json:"value"`
		ValidUntil time.Time `json:"validUntil"`
	} `json:"signature"`
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
	state.PriorIndexVersion = doc.Version

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
			if entry.Status == "RETIRED" || entry.Baseline == nil {
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
	baselinePath := CatalogFilePath(root, entry.CatalogID, entry.Baseline.Version, "json")
	baselineBytes, err := os.ReadFile(baselinePath)
	if err != nil {
		return nil, fmt.Errorf("localstore: reading %s: %w", baselinePath, err)
	}

	effective := json.RawMessage(baselineBytes)
	changeFiles := make([]definition.FileRef, 0, len(entry.Changes))
	for _, ch := range entry.Changes {
		path := CatalogFilePath(root, entry.CatalogID, ch.Version, "changes.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("localstore: reading %s: %w", path, err)
		}
		effective, err = catalogfile.Apply(effective, raw)
		if err != nil {
			return nil, fmt.Errorf("localstore: applying %s: %w", path, err)
		}
		changeFiles = append(changeFiles, toFileRef(ch))
	}

	baselineRef := toFileRef(*entry.Baseline)
	return &definition.PriorCatalogState{
		Catalog:      effective,
		BaselineFile: &baselineRef,
		ChangeFiles:  changeFiles,
	}, nil
}

func toFileRef(fe wireFileEntry) definition.FileRef {
	return definition.FileRef{
		Version:             fe.Version,
		URL:                 fe.URL,
		Size:                fe.Size,
		Digest:              fe.Digest,
		SignatureKeyID:      fe.Signature.KeyID,
		SignatureValue:      fe.Signature.Value,
		SignatureValidUntil: fe.Signature.ValidUntil,
	}
}
