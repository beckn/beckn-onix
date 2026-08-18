// Package store is the one shared, backend-agnostic assembler for
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
package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalog"
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
func CatalogFilePath(catalogID string, version int64, suffix string, compressed bool) string {
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

// CatalogState is one catalog's reconstructed current state: the full
// content last published (baseline with every change file applied),
// enough for a caller to diff a new submission against, plus the
// entry-level metadata (distinct from file-lineage versioning) needed to
// detect a metadata-only change with no new file. BaselineFile/ChangeFiles/
// Dependencies reuse pkg/catalog's own wire types directly (the same ones
// catalog/crawler reads) rather than a second, independently-drifting
// copy -- see index.go.
type CatalogState struct {
	Catalog      json.RawMessage
	BaselineFile *catalog.FileEntry
	ChangeFiles  []catalog.FileEntry

	EntryVersion int64
	CatalogType  string
	NetworkIds   []string
	SchemaTypes  []string
	IsActive     bool
	Dependencies []catalog.MasterDependency
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
	Version       int64
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
type Store struct {
	blobs definition.CatalogBlobStore
	log   *slog.Logger
}

// New constructs a Store over blobs. Plain Go, no onix plugin machinery
// required. Logs via slog.Default() until WithLogger overrides it.
func New(blobs definition.CatalogBlobStore) *Store { return &Store{blobs: blobs, log: slog.Default()} }

// WithLogger sets the logger this Store uses for every subsequent call,
// e.g. to route it through an onix-hosted caller's own structured
// logging instead of slog's default handler. Returns s for chaining.
func (s *Store) WithLogger(logger *slog.Logger) *Store {
	if logger != nil {
		s.log = logger
	}
	return s
}

// indexDoc is the top-level index document's own shape: unlike an entry,
// nobody besides Store needs it as a typed value -- every reader treats
// "catalogs" as opaque per-entry bytes to merge or carry forward, and
// NextUpdate here is optional (omitted entirely when unset, per the file
// spec -- unlike catalog.Index's plain string, which a read-only caller
// like the crawler is content to leave as "" when absent). So this one
// wrapper stays Store's own, not shared with catalog.Index.
type indexDoc struct {
	NodeID     string            `json:"nodeId"`
	NextUpdate *time.Time        `json:"next_update,omitempty"`
	Catalogs   []json.RawMessage `json:"catalogs"`
}

// fileEnvelope unwraps a stored baseline/change file's self-signed
// envelope back to the bare catalog content, plus NextUpdate -- used to
// decide when a compaction's grace period has elapsed (see
// reconstructState). This is a file-content document, not an index entry
// -- index.go covers the latter, not the former, so there is nothing to
// share here yet (see pkg/catalog/publisher for where these get built and
// signed). No signature verification on read -- Store only ever reads
// back its own previously-written output, not externally-fetched content.
type fileEnvelope struct {
	NextUpdate time.Time       `json:"next_update"`
	Catalog    json.RawMessage `json:"catalog"`
}

// --- reads ------------------------------------------------------------

// LoadCatalogs reconstructs current state -- baseline with every change
// file applied -- for each of catalogIDs that has prior publish history.
// A requested catalogId absent from the result has none: the caller
// starts a fresh baseline for it.
func (s *Store) LoadCatalogs(ctx context.Context, catalogIDs []string) (map[string]CatalogState, error) {
	s.log.DebugContext(ctx, "catalogstore: LoadCatalogs", "requested", len(catalogIDs))
	entries, _, err := s.readIndexEntries(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]CatalogState, len(catalogIDs))
	for _, id := range catalogIDs {
		rawEntry, ok := entries[id]
		if !ok {
			s.log.DebugContext(ctx, "catalogstore: catalog not in index, starting fresh", "catalogId", id)
			continue
		}
		var entry catalog.CatalogEntry
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			return nil, fmt.Errorf("catalogstore: parsing entry for %s: %w", id, err)
		}
		if entry.IsRetired() || entry.Baseline.URL == "" {
			s.log.DebugContext(ctx, "catalogstore: no publishable prior state, starting fresh", "catalogId", id, "retired", entry.IsRetired())
			continue // no publishable prior state; caller starts a fresh baseline
		}
		state, err := s.reconstructState(ctx, entry)
		if err != nil {
			return nil, err
		}
		result[id] = *state
	}
	s.log.DebugContext(ctx, "catalogstore: LoadCatalogs done", "requested", len(catalogIDs), "reconstructed", len(result))
	return result, nil
}

// readIndexEntries reads the current index (if any), returning every
// entry keyed by catalogId plus the catalogIds in their original index
// order -- so Publish can preserve stable ordering when it writes the
// merged index back out.
func (s *Store) readIndexEntries(ctx context.Context) (map[string]json.RawMessage, []string, error) {
	raw, err := s.blobs.Get(ctx, IndexPath())
	if errors.Is(err, definition.ErrBlobNotFound) {
		s.log.DebugContext(ctx, "catalogstore: no existing index, starting empty")
		return map[string]json.RawMessage{}, nil, nil
	}
	if err != nil {
		s.log.ErrorContext(ctx, "catalogstore: reading existing index failed", "error", err)
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
			s.log.WarnContext(ctx, "catalogstore: skipping malformed index entry")
			continue // tolerate a malformed stray entry rather than fail the whole read
		}
		entries[probe.CatalogID] = rawEntry
		order = append(order, probe.CatalogID)
	}
	s.log.DebugContext(ctx, "catalogstore: read existing index", "entries", len(entries), "bytes", len(raw))
	return entries, order, nil
}

func (s *Store) reconstructState(ctx context.Context, entry catalog.CatalogEntry) (*CatalogState, error) {
	baselineGzip := isGzipEncoded(entry.Baseline)
	baselinePath := CatalogFilePath(entry.CatalogID, entry.Baseline.Version, "json", baselineGzip)
	baselineBytes, err := s.blobs.Get(ctx, baselinePath)
	if err != nil {
		s.log.ErrorContext(ctx, "catalogstore: reading baseline failed", "catalogId", entry.CatalogID, "path", baselinePath, "error", err)
		return nil, fmt.Errorf("catalogstore: reading %s: %w", baselinePath, err)
	}
	s.log.DebugContext(ctx, "catalogstore: read baseline", "catalogId", entry.CatalogID, "path", baselinePath, "bytes", len(baselineBytes))
	if baselineGzip {
		if baselineBytes, err = gunzip(baselineBytes); err != nil {
			return nil, fmt.Errorf("catalogstore: decompressing %s: %w", baselinePath, err)
		}
	}
	var wrapped fileEnvelope
	if err := json.Unmarshal(baselineBytes, &wrapped); err != nil {
		return nil, fmt.Errorf("catalogstore: parsing %s: %w", baselinePath, err)
	}

	// A change file's EffectiveVersion is only ever <= the current
	// baseline's own Version right after a forced re-baseline
	// (compaction): every other baseline is created once, before any of
	// its changes[], so all of its changes carry strictly higher
	// versions. Such a "superseded" change's content is already folded
	// into the baseline itself -- applying it again is a harmless no-op
	// -- but it only needs to stay *listed* for one full next_update
	// cycle after the compaction, measured against the next_update
	// stamped on the new baseline at compaction time (wrapped.NextUpdate
	// here). Once that's elapsed, dropping it from the returned
	// ChangeFiles is what actually realizes compaction's index-payload
	// benefit -- this is Store's own retention rule, derived purely from
	// data already on disk, not a separate config knob.
	gracePeriodElapsed := time.Now().After(wrapped.NextUpdate)

	effective := wrapped.Catalog
	changeFiles := make([]catalog.FileEntry, 0, len(entry.Changes))
	for _, ch := range entry.Changes {
		chGzip := isGzipEncoded(ch)
		chPath := CatalogFilePath(entry.CatalogID, ch.EffectiveVersion(), "changes.json", chGzip)
		raw, err := s.blobs.Get(ctx, chPath)
		if err != nil {
			s.log.ErrorContext(ctx, "catalogstore: reading change file failed", "catalogId", entry.CatalogID, "path", chPath, "error", err)
			return nil, fmt.Errorf("catalogstore: reading %s: %w", chPath, err)
		}
		if chGzip {
			if raw, err = gunzip(raw); err != nil {
				return nil, fmt.Errorf("catalogstore: decompressing %s: %w", chPath, err)
			}
		}
		effective, err = catalog.Apply(effective, raw)
		if err != nil {
			return nil, fmt.Errorf("catalogstore: applying %s: %w", chPath, err)
		}
		superseded := ch.EffectiveVersion() <= entry.Baseline.Version
		if superseded && gracePeriodElapsed {
			s.log.DebugContext(ctx, "catalogstore: dropping superseded change file (grace period elapsed)",
				"catalogId", entry.CatalogID, "fromVersion", ch.FromVersion, "toVersion", ch.ToVersion)
			continue // grace period over: stop listing this pre-compaction change file
		}
		s.log.DebugContext(ctx, "catalogstore: applied change file", "catalogId", entry.CatalogID,
			"fromVersion", ch.FromVersion, "toVersion", ch.ToVersion, "superseded", superseded)
		changeFiles = append(changeFiles, ch)
	}

	isActive := true
	if entry.IsActive != nil {
		isActive = *entry.IsActive
	}
	baseline := entry.Baseline
	var deps []catalog.MasterDependency
	if entry.Dependencies != nil {
		deps = entry.Dependencies.Masters
	}
	return &CatalogState{
		Catalog:         effective,
		BaselineFile:    &baseline,
		ChangeFiles:     changeFiles,
		EntryVersion:    entry.EntryVersion,
		CatalogType:     entry.CatalogType,
		NetworkIds:      entry.NetworkIDs,
		SchemaTypes:     entry.SchemaTypes,
		IsActive:        isActive,
		Dependencies:    deps,
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
	s.log.DebugContext(ctx, "catalogstore: Publish", "nodeId", req.NodeID, "updates", len(req.Updates), "retirements", len(req.Retirements))
	entries, order, err := s.readIndexEntries(ctx)
	if err != nil {
		return err
	}

	apply := func(u CatalogUpdate) error {
		if err := s.writeCatalogFiles(ctx, u); err != nil {
			return err
		}
		_, existed := entries[u.CatalogID]
		if !existed {
			order = append(order, u.CatalogID)
		}
		entries[u.CatalogID] = u.SignedEntry
		s.log.DebugContext(ctx, "catalogstore: merged entry", "catalogId", u.CatalogID, "replacedExisting", existed)
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
		s.log.ErrorContext(ctx, "catalogstore: writing index failed", "error", err)
		return fmt.Errorf("catalogstore: writing index: %w", err)
	}
	s.log.DebugContext(ctx, "catalogstore: Publish done", "entries", len(ordered), "bytes", len(indexBytes))
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
		s.log.ErrorContext(ctx, "catalogstore: writing file failed", "path", path, "error", err)
		return fmt.Errorf("catalogstore: writing %s: %w", path, err)
	}
	s.log.DebugContext(ctx, "catalogstore: wrote file", "path", path, "bytes", len(served), "compressed", fw.Compressed)
	return nil
}

// --- helpers --------------------------------------------------------------

// isGzipEncoded reports whether fe's content is gzip-compressed: fe.Encoding
// if set, otherwise falling back to the URL's ".gz" suffix -- the same
// fallback convention catalog.FileEntry's own doc comment describes for a
// reader (see pkg/crawler/decode.EncodingFor). Read back from the entry
// itself rather than from any current compression setting, since a
// catalog's files may have been published under a different compression
// setting than whatever produced this call.
func isGzipEncoded(fe catalog.FileEntry) bool {
	if fe.Encoding != "" {
		return fe.Encoding == "gzip"
	}
	return strings.HasSuffix(fe.URL, ".gz")
}

// gunzip decompresses data written by a caller's own gzip.Writer.
func gunzip(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
