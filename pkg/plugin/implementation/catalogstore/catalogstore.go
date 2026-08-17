// Package catalogstore is the one shared, backend-agnostic assembler for
// a publisher's catalog index: it understands how an index, its
// catalogs' baselines, change files, and "latest" pointers fit together
// per the decentralized-catalog file spec, built on top of whichever
// definition.CatalogBlobStore is configured (local disk, S3, GCS, git,
// an authenticated CDN write root, ...).
//
// This is deliberately NOT an onix plugin: there is exactly one correct
// way to assemble/reconstruct a catalog's file set per the spec, so
// nothing here should be swappable -- only the CatalogBlobStore
// underneath it is. A publisher tool (an HTTP handler, a CLI) talks to
// Store, never to a CatalogBlobStore directly.
//
// Store holds no signing key material and signs nothing: every entry it
// writes (CatalogUpdate.SignedEntry) arrives already self-signed by the
// caller. Store's only job is deciding where bytes live and merging new
// entries into the index that's already there.
package catalogstore

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogfile"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// IndexFilename is the catalog index's filename.
const IndexFilename = "becknCatalogs.index.json"

// IndexDirName, CatalogsDirName, and ChangesDirName are the blob-key
// "directories" every path here is built under -- fixed by convention,
// matching the file spec's own example layout, not configurable per
// deployment (only where the blob store's root itself points is).
const (
	IndexDirName    = "index"
	CatalogsDirName = "catalogs"
	ChangesDirName  = "changes"
)

// IndexPath, CatalogFilePath, and LatestFilePath return "/"-separated blob
// keys -- CatalogBlobStore implementations translate these to whatever
// their backend needs (a filesystem path, an object key, ...).
func IndexPath() string { return path.Join(IndexDirName, IndexFilename) }

// LocalName returns catalogID with any "domain/" prefix stripped, matching
// the file spec's own example filenames (catalogId
// "open-economy.nfh.global/electronics-2026" -> "electronics-2026").
func LocalName(catalogID string) string {
	if i := strings.LastIndex(catalogID, "/"); i != -1 {
		return catalogID[i+1:]
	}
	return catalogID
}

// CatalogFilePath returns the blob key for one catalog file: a baseline
// (suffix "json") sits directly under catalogs/, a change file (suffix
// "changes.json") under catalogs/changes/. compressed appends ".gz".
func CatalogFilePath(catalogID string, version int, suffix string, compressed bool) string {
	if compressed {
		suffix += ".gz"
	}
	filename := fmt.Sprintf("%s.v%d.%s", LocalName(catalogID), version, suffix)
	if strings.HasPrefix(suffix, "changes.json") {
		return path.Join(CatalogsDirName, ChangesDirName, filename)
	}
	return path.Join(CatalogsDirName, filename)
}

// LatestFilePath returns the blob key for a catalog's "latest" pointer:
// unlike CatalogFilePath, its filename carries no version number -- every
// Publish call maintaining "latest" overwrites this same key in place.
func LatestFilePath(catalogID string, compressed bool) string {
	suffix := "json"
	if compressed {
		suffix += ".gz"
	}
	return path.Join(CatalogsDirName, fmt.Sprintf("%s.latest.%s", LocalName(catalogID), suffix))
}

// CatalogFileRef points at one previously published catalog file (a
// baseline, a change file, or a "latest" pointer): its own version, where
// it lives, and enough to verify it without re-fetching (size/digest).
// FromVersion is meaningful for a change file only -- zero for a
// baseline/latest ref. It is carried explicitly rather than reconstructed
// from sequence order: once a catalog has been compacted, retained
// pre-compaction entries no longer form a contiguous chain.
type CatalogFileRef struct {
	FromVersion int
	Version     int
	URL         string
	Size        int64
	Digest      string
}

// MasterDependency is one MASTER catalog a REGULAR catalog's resources
// currently extend.
type MasterDependency struct {
	CatalogID string
	Version   int
	IndexURL  string
}

// CatalogState is one catalog's reconstructed current state: the full
// content last published (baseline with every change file applied),
// enough for a caller to diff a new submission against, plus the
// entry-level metadata (distinct from file-lineage versioning) needed to
// detect a metadata-only change with no new file.
type CatalogState struct {
	Catalog      json.RawMessage
	BaselineFile *CatalogFileRef
	ChangeFiles  []CatalogFileRef

	EntryVersion int
	CatalogType  string
	NetworkIds   []string
	SchemaTypes  []string
	IsActive     bool
	Dependencies []MasterDependency
	CrawlHint    string

	// LatestPublished reports whether a "latest" full-catalog pointer was
	// previously published for this catalog -- independent of whether the
	// caller currently has that turned on, since retiring a catalog that
	// had one still needs a final write to that same stable URL.
	LatestPublished bool
}

// FileWrite is one file's content to persist. Version drives the
// versioned baseline/change path (LatestFilePath ignores it -- a fixed,
// unversioned key). Content is canonical (uncompressed) bytes;
// ServedContent is what's actually written when Compressed is true.
type FileWrite struct {
	Version       int
	Content       json.RawMessage
	ServedContent []byte
	Compressed    bool
}

// CatalogUpdate is one catalog's new index entry -- already self-signed
// by the caller; Store never signs anything, it holds no key material --
// plus whichever new file content that entry references. Used for both
// an ordinary update (Baseline and/or Change, optionally Latest) and a
// retirement (SignedEntry is a tombstone; Latest set only when this
// catalog previously had one, for its one-time final tombstone write;
// Baseline/Change nil).
type CatalogUpdate struct {
	CatalogID   string
	SignedEntry json.RawMessage

	Baseline *FileWrite
	Change   *FileWrite
	Latest   *FileWrite
}

// PublishRequest is one publish call's outcome to persist. NodeID/
// NextUpdate are the index's top-level fields -- policy the caller owns
// (signing domain, freshness window), just data by the time it reaches
// Store.
type PublishRequest struct {
	NodeID      string
	NextUpdate  *time.Time
	Updates     []CatalogUpdate
	Retirements []CatalogUpdate
}

// Store is the one canonical assembler over any definition.CatalogBlobStore.
type Store struct{ blobs definition.CatalogBlobStore }

// New constructs a Store over blobs. Plain Go, no onix plugin machinery
// required.
func New(blobs definition.CatalogBlobStore) *Store { return &Store{blobs: blobs} }

// --- wire types -----------------------------------------------------------
//
// These mirror the subset of the catalog index's shape Store needs to
// read/write -- a wire-format contract, not Go code shared with any
// publisher implementation.

type indexDoc struct {
	NodeID     string            `json:"nodeId"`
	NextUpdate *time.Time        `json:"next_update,omitempty"`
	Catalogs   []json.RawMessage `json:"catalogs"`
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
// toVersion range, not a single version.
type wireChangeFileEntry struct {
	FromVersion int    `json:"fromVersion"`
	ToVersion   int    `json:"toVersion"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	Digest      string `json:"digest"`
}

// wireCatalogFile unwraps a stored baseline/change file's self-signed
// envelope back to the bare catalog content, plus NextUpdate -- used to
// decide when a compaction's grace period has elapsed (see
// reconstructState). No signature verification on read -- Store only
// ever reads back its own previously-written output, not
// externally-fetched content.
type wireCatalogFile struct {
	NextUpdate time.Time       `json:"next_update"`
	Catalog    json.RawMessage `json:"catalog"`
}

// --- reads ------------------------------------------------------------

// LoadCatalogs reconstructs current state -- baseline with every change
// file applied -- for each of catalogIDs that has prior publish history.
// A requested catalogId absent from the result has none: the caller
// starts a fresh baseline for it.
func (s *Store) LoadCatalogs(ctx context.Context, catalogIDs []string) (map[string]CatalogState, error) {
	entries, _, err := s.readIndexEntries(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]CatalogState, len(catalogIDs))
	for _, id := range catalogIDs {
		rawEntry, ok := entries[id]
		if !ok {
			continue
		}
		var entry indexEntry
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			return nil, fmt.Errorf("catalogstore: parsing entry for %s: %w", id, err)
		}
		if entry.RetiredAt != nil || entry.Baseline == nil {
			continue // no publishable prior state; caller starts a fresh baseline
		}
		state, err := s.reconstructState(ctx, entry)
		if err != nil {
			return nil, err
		}
		result[id] = *state
	}
	return result, nil
}

// readIndexEntries reads the current index (if any), returning every
// entry keyed by catalogId plus the catalogIds in their original index
// order -- so Publish can preserve stable ordering when it writes the
// merged index back out.
func (s *Store) readIndexEntries(ctx context.Context) (map[string]json.RawMessage, []string, error) {
	raw, err := s.blobs.Get(ctx, IndexPath())
	if errors.Is(err, definition.ErrBlobNotFound) {
		return map[string]json.RawMessage{}, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("catalogstore: reading existing index: %w", err)
	}

	var doc indexDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, fmt.Errorf("catalogstore: parsing existing index: %w", err)
	}

	entries := make(map[string]json.RawMessage, len(doc.Catalogs))
	order := make([]string, 0, len(doc.Catalogs))
	for _, rawEntry := range doc.Catalogs {
		var probe struct {
			CatalogID string `json:"catalogId"`
		}
		if json.Unmarshal(rawEntry, &probe) != nil || probe.CatalogID == "" {
			continue // tolerate a malformed stray entry rather than fail the whole read
		}
		entries[probe.CatalogID] = rawEntry
		order = append(order, probe.CatalogID)
	}
	return entries, order, nil
}

func (s *Store) reconstructState(ctx context.Context, entry indexEntry) (*CatalogState, error) {
	baselineGzip := isGzipURL(entry.Baseline.URL)
	baselinePath := CatalogFilePath(entry.CatalogID, entry.Baseline.Version, "json", baselineGzip)
	baselineBytes, err := s.blobs.Get(ctx, baselinePath)
	if err != nil {
		return nil, fmt.Errorf("catalogstore: reading %s: %w", baselinePath, err)
	}
	if baselineGzip {
		if baselineBytes, err = gunzip(baselineBytes); err != nil {
			return nil, fmt.Errorf("catalogstore: decompressing %s: %w", baselinePath, err)
		}
	}
	var wrapped wireCatalogFile
	if err := json.Unmarshal(baselineBytes, &wrapped); err != nil {
		return nil, fmt.Errorf("catalogstore: parsing %s: %w", baselinePath, err)
	}

	// A change file's ToVersion is only ever <= the current baseline's own
	// Version right after a forced re-baseline (compaction): every other
	// baseline is created once, before any of its changes[], so all of its
	// changes carry strictly higher versions. Such a "superseded" change's
	// content is already folded into the baseline itself -- applying it
	// again is a harmless no-op -- but it only needs to stay *listed* for
	// one full next_update cycle after the compaction, measured against
	// the next_update stamped on the new baseline at compaction time
	// (wrapped.NextUpdate here). Once that's elapsed, dropping it from the
	// returned ChangeFiles is what actually realizes compaction's
	// index-payload benefit -- this is Store's own retention rule, derived
	// purely from data already on disk, not a separate config knob.
	gracePeriodElapsed := time.Now().After(wrapped.NextUpdate)

	effective := wrapped.Catalog
	changeFiles := make([]CatalogFileRef, 0, len(entry.Changes))
	for _, ch := range entry.Changes {
		chGzip := isGzipURL(ch.URL)
		chPath := CatalogFilePath(entry.CatalogID, ch.ToVersion, "changes.json", chGzip)
		raw, err := s.blobs.Get(ctx, chPath)
		if err != nil {
			return nil, fmt.Errorf("catalogstore: reading %s: %w", chPath, err)
		}
		if chGzip {
			if raw, err = gunzip(raw); err != nil {
				return nil, fmt.Errorf("catalogstore: decompressing %s: %w", chPath, err)
			}
		}
		effective, err = catalogfile.Apply(effective, raw)
		if err != nil {
			return nil, fmt.Errorf("catalogstore: applying %s: %w", chPath, err)
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
	return &CatalogState{
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

// --- writes -------------------------------------------------------------

// Publish merges req's catalog updates and retirements into the existing
// index -- read fresh via readIndexEntries, keyed by catalogId, replacing
// or appending as needed -- and persists everything: every new file
// content, then the merged index last (so a failure partway through never
// leaves the index pointing at a file that was never written).
func (s *Store) Publish(ctx context.Context, req PublishRequest) error {
	entries, order, err := s.readIndexEntries(ctx)
	if err != nil {
		return err
	}

	apply := func(u CatalogUpdate) error {
		if err := s.writeCatalogFiles(ctx, u); err != nil {
			return err
		}
		if _, exists := entries[u.CatalogID]; !exists {
			order = append(order, u.CatalogID)
		}
		entries[u.CatalogID] = u.SignedEntry
		return nil
	}
	for _, u := range req.Updates {
		if err := apply(u); err != nil {
			return err
		}
	}
	for _, r := range req.Retirements {
		if err := apply(r); err != nil {
			return err
		}
	}

	ordered := make([]json.RawMessage, 0, len(order))
	for _, id := range order {
		ordered = append(ordered, entries[id])
	}
	indexBytes, err := json.Marshal(indexDoc{NodeID: req.NodeID, NextUpdate: req.NextUpdate, Catalogs: ordered})
	if err != nil {
		return fmt.Errorf("catalogstore: marshaling index: %w", err)
	}
	if err := s.blobs.Put(ctx, IndexPath(), indexBytes); err != nil {
		return fmt.Errorf("catalogstore: writing index: %w", err)
	}
	return nil
}

func (s *Store) writeCatalogFiles(ctx context.Context, u CatalogUpdate) error {
	if u.Baseline != nil {
		if err := s.writeFile(ctx, CatalogFilePath(u.CatalogID, u.Baseline.Version, "json", u.Baseline.Compressed), u.Baseline); err != nil {
			return err
		}
	}
	if u.Change != nil {
		if err := s.writeFile(ctx, CatalogFilePath(u.CatalogID, u.Change.Version, "changes.json", u.Change.Compressed), u.Change); err != nil {
			return err
		}
	}
	if u.Latest != nil {
		if err := s.writeFile(ctx, LatestFilePath(u.CatalogID, u.Latest.Compressed), u.Latest); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) writeFile(ctx context.Context, path string, fw *FileWrite) error {
	served := fw.ServedContent
	if served == nil {
		served = fw.Content // Compressed is false: ServedContent and Content are identical
	}
	if err := s.blobs.Put(ctx, path, served); err != nil {
		return fmt.Errorf("catalogstore: writing %s: %w", path, err)
	}
	return nil
}

// --- helpers --------------------------------------------------------------

func toMasterDependencies(deps *wireDependencies) []MasterDependency {
	if deps == nil || len(deps.Masters) == 0 {
		return nil
	}
	out := make([]MasterDependency, len(deps.Masters))
	for i, m := range deps.Masters {
		out[i] = MasterDependency{CatalogID: m.CatalogID, Version: m.Version, IndexURL: m.IndexURL}
	}
	return out
}

// isGzipURL reports whether a stored file reference's declared URL is
// gzip-compressed (signaled purely by a ".gz" URL extension) -- read back
// from the entry itself rather than from any current compression setting,
// since a catalog's files may have been published under a different
// compression setting than whatever produced this call.
func isGzipURL(url string) bool { return strings.HasSuffix(url, ".gz") }

// gunzip decompresses data written by a caller's own gzip.Writer.
func gunzip(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func toFileRef(fe wireFileEntry) CatalogFileRef {
	return CatalogFileRef{
		Version: fe.Version,
		URL:     fe.URL,
		Size:    fe.Size,
		Digest:  fe.Digest,
	}
}

// toChangeFileRef converts a changes[] entry back to a CatalogFileRef,
// carrying FromVersion through explicitly -- it is NOT safe to reconstruct
// from sequence order once a compaction has happened, since a superseded
// (pre-compaction) entry retained for the grace period predates the
// current baseline and breaks the "contiguous chain" assumption that
// reconstruction relied on.
func toChangeFileRef(ch wireChangeFileEntry) CatalogFileRef {
	return CatalogFileRef{
		FromVersion: ch.FromVersion,
		Version:     ch.ToVersion,
		URL:         ch.URL,
		Size:        ch.Size,
		Digest:      ch.Digest,
	}
}
