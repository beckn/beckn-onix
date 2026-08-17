// Package publisher is the decentralized-catalog file spec's publish-side
// orchestration: given a publisher's catalog submissions and their prior
// state (as pkg/catalog/store already reconstructs it), diff, decide
// baseline/change/metadata/unchanged, and self-sign the resulting
// catalog-index entries and files -- RFC NFH-014's signing/diffing/
// versioning rules, all of it.
//
// Publish never touches storage: every entry and file it signs comes back
// in Result.Publish, ready to hand directly to a pkg/catalog/store.Store's
// own Publish call, which merges them into the index and persists
// everything. Publish also holds no key material between calls -- Params
// carries the signing key, key id, and domain a caller resolved (e.g. via
// an onix KeyManager) fresh each call, the same way it carries every other
// per-call value (compaction thresholds, gzip, publish-latest, the
// next-update window). Nothing here is loaded once and reused; nothing
// here is onix-specific.
package publisher

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalog"
	"github.com/beckn-one/beckn-onix/pkg/catalog/store"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactsigner"
)

// defaultFileValidity is the fallback used for a catalog file's own
// required next_update when Params.NextUpdateIn is unset (zero) -- unlike
// the index's next_update (optional, omitted when unset), the file spec
// requires next_update on every CatalogFileDoc/ChangeFileDoc.
const defaultFileValidity = 24 * time.Hour

// Submission is one catalog's input to a publish call: the plain Beckn
// Catalog object (no context/message envelope) plus the publisher-
// declared metadata that only the publisher can know -- NetworkIds is
// never derived from the catalog content itself. Catalogs are public,
// unconditionally (RFC NFH-014, "Catalog access is public") -- there is
// no restricted-catalog or per-catalog-auth concept; NetworkIds is a
// Discovery-service relevance filter only, never an access control.
//
// IsActive is deliberately absent here: the index entry's isActive
// mirrors Catalog's own pre-existing isActive field, so Publish reads it
// out of Catalog itself rather than duplicating it as a second input
// that could disagree.
type Submission struct {
	// CatalogID is the full participant-scoped id, e.g.
	// "open-economy.nfh.global/electronics-2026".
	CatalogID string

	// CatalogType defaults to "REGULAR" when empty.
	CatalogType string

	SchemaTypes []string

	// NetworkIds scopes this catalog to specific networks; empty/nil
	// means public.
	NetworkIds []string

	// Dependencies lists, for a REGULAR catalog, every MASTER catalog any
	// of its resources currently extend (NFH-014 §10.3, CON-TBD-30).
	// Publish does not derive this from Catalog's own resource content --
	// a masterResourceId names a resource, not the catalog it lives in,
	// so resolving which catalog (and which index) owns it needs
	// cross-catalog knowledge only the caller has.
	Dependencies []catalog.MasterDependency

	// CrawlHint is an optional suggested crawl frequency a crawler MAY
	// honor. Empty means no hint is published.
	CrawlHint string

	Catalog json.RawMessage
}

// Params is everything one Publish call needs, all as explicit values --
// no config is loaded or stored here, and no key material is held between
// calls. The caller resolves SigningKey/KeyID/Domain (e.g. via an onix
// KeyManager) and the compaction/gzip/publish-latest/next-update knobs
// (e.g. from its own onix plugin config) fresh each call.
type Params struct {
	Catalogs []Submission

	// PriorState supplies, per catalogId, what pkg/catalog/store's own
	// Store.LoadCatalogs already reconstructed for the catalogs actually
	// submitted in Catalogs -- the only way Publish can produce a change
	// file instead of a fresh baseline. A submitted catalogId absent from
	// this map is always published as a new baseline, same as when
	// ForceBaseline is set.
	PriorState map[string]store.CatalogState

	// Retire marks these catalogIds RETIRED this call: a tombstone entry
	// (retiredAt, no isActive/baseline/changes) replaces whatever was
	// there. A retired catalogId's prior CatalogType/NetworkIds/
	// SchemaTypes and EntryVersion still come from PriorState[id] -- a
	// tombstone keeps those, it only drops isActive/baseline/changes. A
	// catalogId present in both Retire and Catalogs is published
	// normally; Retire is ignored for it.
	Retire []string

	// ForceBaseline bypasses diffing against PriorState and always emits
	// a fresh baseline. For a catalog with no prior state this is a no-op
	// (already the default); for one with prior state, this is how a
	// caller triggers compaction -- a fresh baseline at the next version,
	// discarding the accumulated change list.
	ForceBaseline bool

	// CompactionChangeCountThreshold and CompactionSizeRatioThreshold
	// opt into automatically compacting a catalog's baseline instead of
	// publishing yet another change file. Both default to 0 (disabled).
	// Checked against prior.ChangeFiles only, before the new change file
	// would be created:
	//   - CompactionChangeCountThreshold: compact once the catalog
	//     already has this many pending change files.
	//   - CompactionSizeRatioThreshold: compact once the combined size of
	//     pending change files, divided by the baseline's own size, is at
	//     least this fraction (e.g. 0.5 for 50%).
	// Either threshold alone can trigger compaction; ForceBaseline still
	// always triggers it regardless of these.
	CompactionChangeCountThreshold int
	CompactionSizeRatioThreshold   float64

	// Gzip opts into serving every file gzip-compressed, signaled by a
	// ".json.gz" URL extension and FileEntry.Encoding. Digest/signature
	// are always computed over the canonical, decompressed content
	// regardless of this setting.
	Gzip bool

	// PublishLatest opts into publishing/maintaining each catalog's
	// "latest" entry: a full CatalogFileDoc overwritten in place at a
	// stable URL, for consumers who want fully-current content without
	// ever applying changes[].
	PublishLatest bool

	// NextUpdateIn sets how far in the future the index's "next_update"
	// freshness window extends from the moment of publishing. Zero omits
	// the index's next_update entirely (a catalog file's own next_update
	// still gets defaultFileValidity, since the schema requires it).
	NextUpdateIn time.Duration

	// PublicBaseURL, if set, is the one public URL prefix everything this
	// publish writes gets addressed under -- the same root a
	// pkg/catalog/store-backed CatalogBlobStore serves its layout from. A
	// placeholder ("pending-artifact-store://...") is used when unset.
	PublicBaseURL string

	// SigningKey/KeyID/Domain are this call's resolved signing identity:
	// SigningKey signs every file/entry; KeyID is embedded as each
	// signature's keyId; Domain is stamped as the index's own nodeId.
	// Resolved by the caller (e.g. via KeyManager.Keyset) fresh every
	// call -- Publish holds none of this between calls.
	SigningKey ed25519.PrivateKey
	KeyID      string
	Domain     string
}

// Outcome reports what happened to one submitted catalog -- reporting
// only. Content/LatestContent are the canonical (uncompressed) bytes for
// programmatic inspection; the actual bytes to persist (served,
// compressed) travel via Result.Publish.Updates instead.
type Outcome struct {
	CatalogID string

	// Version is this catalog's new current file-lineage version after
	// this call, distinct from EntryVersion.
	Version int64

	// EntryVersion is this catalog's new entry-level version -- bumped
	// whenever Changed is true, content or metadata.
	EntryVersion int64

	Changed bool // false = no-op: diffed against PriorState and found no changes at all
	Digest  string

	// Mode is "baseline", "change", "metadata" (no file republished, but
	// entry-level metadata changed), or "unchanged".
	Mode    string
	Content json.RawMessage

	// LatestContent/LatestDigest are set whenever PublishLatest is on.
	LatestContent json.RawMessage
	LatestDigest  string
}

// PublishError is a non-fatal, per-catalog failure -- one bad submission
// must not fail the whole publish call.
type PublishError struct {
	CatalogID string
	Stage     string // "validate" | "diff" | "retire"
	Reason    string
	Fatal     bool
}

// Result is one Publish call's outcome. Publish is ready to hand directly
// to a pkg/catalog/store.Store's own Publish call -- this package never
// touches storage itself. Reports is per-catalog human-readable detail
// for a caller's own display/response purposes; Errors are non-fatal
// per-catalog failures.
type Result struct {
	PublishedAt time.Time
	Publish     store.PublishRequest
	Reports     []Outcome
	Errors      []PublishError
}

// outcome is publishOne/finishEntry's full internal result -- Publish
// splits it into the public, reporting-only Outcome and the store-ready
// CatalogUpdate.
type outcome struct {
	CatalogID     string
	Version       int64
	EntryVersion  int64
	Changed       bool
	Digest        string
	Mode          string
	Content       json.RawMessage
	ServedContent []byte

	LatestContent       json.RawMessage
	LatestServedContent []byte
	LatestDigest        string
}

// IndexURL returns the configured index location under publicBaseURL, or
// a placeholder when no ArtifactStore-assigned location exists yet.
func IndexURL(publicBaseURL string) string {
	if publicBaseURL != "" {
		return joinURL(publicBaseURL, store.IndexDirName, store.IndexFilename)
	}
	return "pending-artifact-store://catalog-index.json"
}

// Publish validates each submission, diffs it against any PriorState for
// its catalogId, and signs the resulting catalog-index entry (baseline,
// change file, retirement tombstone, or a metadata-only/no-op update). A
// submission that fails validation or diffing is reported as a non-fatal
// PublishError and skipped; it does not fail the rest of the batch.
func Publish(ctx context.Context, p Params) (Result, error) {
	if len(p.SigningKey) != ed25519.PrivateKeySize {
		return Result{}, fmt.Errorf("catalog/publisher: invalid signing key (want %d bytes, got %d)", ed25519.PrivateKeySize, len(p.SigningKey))
	}
	if p.KeyID == "" {
		return Result{}, fmt.Errorf("catalog/publisher: KeyID is required")
	}

	now := time.Now()
	result := Result{PublishedAt: now, Publish: store.PublishRequest{NodeID: p.Domain}}

	fileValidityIn := p.NextUpdateIn
	if fileValidityIn <= 0 {
		fileValidityIn = defaultFileValidity
	}
	fileNextUpdate := now.Add(fileValidityIn)
	if p.NextUpdateIn > 0 {
		nextUpdate := fileNextUpdate
		result.Publish.NextUpdate = &nextUpdate
	}

	submitted := make(map[string]bool, len(p.Catalogs))

	for _, sub := range p.Catalogs {
		submitted[sub.CatalogID] = true
		if err := validateSubmission(sub); err != nil {
			result.Errors = append(result.Errors, PublishError{CatalogID: sub.CatalogID, Stage: "validate", Reason: err.Error()})
			continue
		}

		oc, entry, err := p.publishOne(sub, p.PriorState[sub.CatalogID], fileNextUpdate)
		if err != nil {
			result.Errors = append(result.Errors, PublishError{CatalogID: sub.CatalogID, Stage: "diff", Reason: err.Error()})
			continue
		}

		raw, err := p.signEntry(entry)
		if err != nil {
			return result, fmt.Errorf("catalog/publisher: signing catalog entry %q: %w", sub.CatalogID, err)
		}

		update := store.CatalogUpdate{CatalogID: sub.CatalogID, SignedEntry: raw}
		if oc.Content != nil {
			fw := &store.FileWrite{Version: oc.Version, Content: oc.Content, ServedContent: oc.ServedContent, Compressed: p.Gzip}
			switch oc.Mode {
			case "baseline":
				update.Baseline = fw
			case "change":
				update.Change = fw
			}
		}
		if oc.LatestContent != nil {
			update.Latest = &store.FileWrite{Version: oc.Version, Content: oc.LatestContent, ServedContent: oc.LatestServedContent, Compressed: p.Gzip}
		}
		result.Publish.Updates = append(result.Publish.Updates, update)
		result.Reports = append(result.Reports, Outcome{
			CatalogID: oc.CatalogID, Version: oc.Version, EntryVersion: oc.EntryVersion,
			Changed: oc.Changed, Digest: oc.Digest, Mode: oc.Mode, Content: oc.Content,
			LatestContent: oc.LatestContent, LatestDigest: oc.LatestDigest,
		})
	}

	tombstoned := make(map[string]bool, len(p.Retire))
	for _, id := range p.Retire {
		if submitted[id] || tombstoned[id] {
			continue // submitting and retiring the same catalogId in one call: submission wins; duplicate retire ids collapse to one tombstone
		}
		tombstoned[id] = true
		prior := p.PriorState[id]
		raw, err := p.signEntry(catalog.CatalogEntry{
			CatalogID:    id,
			EntryVersion: prior.EntryVersion + 1,
			CatalogType:  prior.CatalogType,
			NetworkIDs:   prior.NetworkIds,
			SchemaTypes:  prior.SchemaTypes,
			RetiredAt:    now.Format(time.RFC3339Nano),
		})
		if err != nil {
			return result, fmt.Errorf("catalog/publisher: signing tombstone %q: %w", id, err)
		}
		update := store.CatalogUpdate{CatalogID: id, SignedEntry: raw}

		// CON-TBD-38: a catalog that had "latest" published needs one
		// final write to that same stable URL, populating
		// CatalogFileDoc.retiredAt -- otherwise a consumer that only ever
		// fetches "latest" directly, never revisiting the index, has no
		// way to learn the catalog is gone. Independent of today's
		// PublishLatest: this is cleaning up a file that already exists,
		// not deciding whether to start publishing a new one.
		if prior.LatestPublished {
			if prior.Catalog == nil {
				result.Errors = append(result.Errors, PublishError{
					CatalogID: id, Stage: "retire",
					Reason: "LatestPublished is set but PriorState.Catalog is empty; cannot write the final \"latest\" tombstone",
				})
			} else {
				content, err := p.signCatalogFile(id, currentVersion(prior), fileNextUpdate, prior.Catalog, &now)
				if err != nil {
					return result, fmt.Errorf("catalog/publisher: signing final \"latest\" tombstone %q: %w", id, err)
				}
				served, _, _, err := maybeCompress(content, "json", p.Gzip)
				if err != nil {
					return result, fmt.Errorf("catalog/publisher: compressing final \"latest\" tombstone %q: %w", id, err)
				}
				update.Latest = &store.FileWrite{Content: content, ServedContent: served, Compressed: p.Gzip}
			}
		}
		result.Publish.Retirements = append(result.Publish.Retirements, update)
	}

	return result, nil
}

// publishOne decides baseline vs. change-file vs. metadata-only vs. no-op
// for one submission and builds both its internal outcome and its
// catalog.CatalogEntry. The returned entry is not yet signed -- Publish
// signs every entry uniformly via signEntry.
func (p Params) publishOne(sub Submission, prior store.CatalogState, fileNextUpdate time.Time) (outcome, catalog.CatalogEntry, error) {
	hasPrior := prior.Catalog != nil
	catalogType := sub.CatalogType
	if catalogType == "" {
		catalogType = "REGULAR"
	}
	isActive := catalogIsActive(sub.Catalog)

	entry := catalog.CatalogEntry{
		CatalogID:    sub.CatalogID,
		CatalogType:  catalogType,
		Dependencies: dependenciesToWire(sub.Dependencies),
		NetworkIDs:   sub.NetworkIds,
		SchemaTypes:  sub.SchemaTypes,
		IsActive:     &isActive,
		CrawlHint:    sub.CrawlHint,
	}

	metadataChanged := !hasPrior ||
		prior.CatalogType != catalogType ||
		prior.IsActive != isActive ||
		!stringSlicesEqual(prior.NetworkIds, sub.NetworkIds) ||
		!stringSlicesEqual(prior.SchemaTypes, sub.SchemaTypes) ||
		!masterDependenciesEqual(prior.Dependencies, sub.Dependencies) ||
		prior.CrawlHint != sub.CrawlHint

	if hasPrior {
		if prior.BaselineFile != nil {
			entry.Baseline = *prior.BaselineFile
		}
		entry.Changes = prior.ChangeFiles
	}

	// diff/changeCatalog are only meaningful once hasPrior && !ForceBaseline
	// (an unconditional baseline republish below never needs them);
	// computed here, ahead of the baseline-vs-change decision, so an
	// automatic compaction trigger (below) can be gated on
	// contentChanged -- it must never force a baseline republish on a
	// call that changed nothing at all content-wise.
	var diff catalog.CatalogDiff
	var changeCatalog json.RawMessage
	contentChanged := true
	if hasPrior && !p.ForceBaseline {
		var err error
		diff, changeCatalog, err = catalog.Diff(prior.Catalog, sub.Catalog)
		if err != nil {
			return outcome{}, catalog.CatalogEntry{}, err
		}
		contentChanged = !diff.Resources.IsEmpty() || !diff.Offers.IsEmpty() || changeCatalog != nil
	}

	if !hasPrior || p.ForceBaseline || (contentChanged && p.compactionDue(prior)) {
		version := currentVersion(prior) + 1
		content, err := p.signCatalogFile(sub.CatalogID, version, fileNextUpdate, sub.Catalog, nil)
		if err != nil {
			return outcome{}, catalog.CatalogEntry{}, err
		}
		fe, served, err := p.buildFileEntry(sub.CatalogID, version, "json", content)
		if err != nil {
			return outcome{}, catalog.CatalogEntry{}, err
		}
		entry.Baseline = fe
		// entry.Changes is deliberately left as whatever prior.ChangeFiles
		// carried (set above), not reset to nil: on a forced re-baseline
		// (compaction), NFH-014 CON-TBD-32 requires the change files that
		// led up to the new baseline to stay *listed* -- not merely
		// hosted -- for a grace period, so a DS mid-lineage can still
		// reach equivalent content by applying them instead of fetching
		// the baseline. Publish holds no timer of its own; how long to
		// keep passing them here is pkg/catalog/store's own retention
		// policy, applied when it reconstructed PriorState.
		entry.EntryVersion = prior.EntryVersion + 1

		oc := outcome{
			CatalogID: sub.CatalogID, Version: version, EntryVersion: entry.EntryVersion, Changed: true, Digest: fe.Digest, Mode: "baseline",
			Content: content, ServedContent: served,
		}
		return p.finishEntry(sub, entry, oc, fileNextUpdate)
	}

	if !contentChanged {
		version := currentVersion(prior)
		if !metadataChanged {
			entry.EntryVersion = prior.EntryVersion
			oc := outcome{CatalogID: sub.CatalogID, Version: version, EntryVersion: entry.EntryVersion, Changed: false, Mode: "unchanged"}
			return p.finishEntry(sub, entry, oc, fileNextUpdate)
		}
		entry.EntryVersion = prior.EntryVersion + 1
		oc := outcome{CatalogID: sub.CatalogID, Version: version, EntryVersion: entry.EntryVersion, Changed: true, Mode: "metadata"}
		return p.finishEntry(sub, entry, oc, fileNextUpdate)
	}

	fromVersion := currentVersion(prior)
	toVersion := fromVersion + 1
	changeDoc := catalog.ChangeFileDoc{
		CatalogID: sub.CatalogID, FromVersion: fromVersion, ToVersion: toVersion, NextUpdate: fileNextUpdate,
		Resources: diff.Resources, Offers: diff.Offers, Catalog: changeCatalog,
	}
	content, err := p.signChangeFile(changeDoc)
	if err != nil {
		return outcome{}, catalog.CatalogEntry{}, err
	}

	fe, served, err := p.buildFileEntry(sub.CatalogID, toVersion, "changes.json", content)
	if err != nil {
		return outcome{}, catalog.CatalogEntry{}, err
	}
	entry.Changes = append(entry.Changes, catalog.FileEntry{
		FromVersion: fromVersion, ToVersion: toVersion, URL: fe.URL, Size: fe.Size, Digest: fe.Digest, Encoding: fe.Encoding,
	})
	entry.EntryVersion = prior.EntryVersion + 1

	oc := outcome{
		CatalogID: sub.CatalogID, Version: toVersion, EntryVersion: entry.EntryVersion, Changed: true, Digest: fe.Digest, Mode: "change",
		Content: content, ServedContent: served,
	}
	return p.finishEntry(sub, entry, oc, fileNextUpdate)
}

// finishEntry adds a "latest" full-CatalogFileDoc pointer when
// PublishLatest is on -- regenerated from sub.Catalog on every call
// regardless of Mode, since "latest" always mirrors current content, not
// a specific lineage step. A no-op when PublishLatest is off, which every
// publishOne return path funnels through so "latest" (or its absence) is
// applied uniformly rather than duplicated at each call site.
func (p Params) finishEntry(sub Submission, entry catalog.CatalogEntry, oc outcome, fileNextUpdate time.Time) (outcome, catalog.CatalogEntry, error) {
	if !p.PublishLatest {
		return oc, entry, nil
	}
	content, err := p.signCatalogFile(sub.CatalogID, oc.Version, fileNextUpdate, sub.Catalog, nil)
	if err != nil {
		return outcome{}, catalog.CatalogEntry{}, err
	}
	fe, served, err := p.buildLatestFileEntry(sub.CatalogID, oc.Version, content)
	if err != nil {
		return outcome{}, catalog.CatalogEntry{}, err
	}
	entry.Latest = &fe
	oc.LatestContent = content
	oc.LatestServedContent = served
	oc.LatestDigest = fe.Digest
	return oc, entry, nil
}

// catalogIsActive reads the submitted Catalog's own pre-existing
// "isActive" field -- the entry's isActive mirrors catalog.isActive, it
// is never a second, independently-supplied input -- defaulting to true
// when absent, matching Catalog's public-by-default posture.
func catalogIsActive(cat json.RawMessage) bool {
	var probe struct {
		IsActive *bool `json:"isActive"`
	}
	if json.Unmarshal(cat, &probe) != nil || probe.IsActive == nil {
		return true
	}
	return *probe.IsActive
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func masterDependenciesEqual(a, b []catalog.MasterDependency) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func dependenciesToWire(deps []catalog.MasterDependency) *catalog.Dependencies {
	if len(deps) == 0 {
		return nil
	}
	return &catalog.Dependencies{Masters: deps}
}

// signCatalogFile builds the file spec's self-signed CatalogFileDoc
// (baseline/latest): {catalog, signature}. "Avoiding circular signing":
// the signing input is the JCS canonicalization of this document with
// "signature" itself removed, so the pre-signing marshal's placeholder
// Signature value never matters -- it's stripped before signing anyway.
// retiredAt is nil for an ordinary baseline or "latest" refresh; it is set
// only for the one-time final write to a retired catalog's "latest" URL.
func (p Params) signCatalogFile(catalogID string, version int64, nextUpdate time.Time, cat json.RawMessage, retiredAt *time.Time) ([]byte, error) {
	doc := catalog.CatalogFileDoc{CatalogID: catalogID, Version: version, NextUpdate: nextUpdate, Catalog: cat, RetiredAt: retiredAt}
	unsigned, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("catalog/publisher: marshaling catalog file: %w", err)
	}
	sigValue, err := artifactsigner.SignJSON(unsigned, "signature", p.SigningKey)
	if err != nil {
		return nil, fmt.Errorf("catalog/publisher: signing catalog file: %w", err)
	}
	doc.Signature = catalog.FileSignature{KeyID: p.KeyID, Canonicalization: "JCS", Value: sigValue}
	return json.Marshal(doc)
}

// signChangeFile is signCatalogFile's counterpart for a ChangeFileDoc:
// doc's own fields are signed with "signature" removed, then the
// signature is attached and the final document re-marshaled.
func (p Params) signChangeFile(doc catalog.ChangeFileDoc) ([]byte, error) {
	unsigned, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("catalog/publisher: marshaling change file: %w", err)
	}
	sigValue, err := artifactsigner.SignJSON(unsigned, "signature", p.SigningKey)
	if err != nil {
		return nil, fmt.Errorf("catalog/publisher: signing change file: %w", err)
	}
	doc.Signature = catalog.FileSignature{KeyID: p.KeyID, Canonicalization: "JCS", Value: sigValue}
	return json.Marshal(doc)
}

// signEntry self-signs a catalog-index entry as a whole: every other
// field of entry, JCS-canonicalized with "signature" removed. Applies
// uniformly to ACTIVE and RETIRED (tombstone) entries alike.
func (p Params) signEntry(entry catalog.CatalogEntry) (json.RawMessage, error) {
	unsigned, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("catalog/publisher: marshaling catalog entry: %w", err)
	}
	sigValue, err := artifactsigner.SignJSON(unsigned, "signature", p.SigningKey)
	if err != nil {
		return nil, fmt.Errorf("catalog/publisher: signing catalog entry: %w", err)
	}
	entry.Signature = catalog.EntrySignature{KeyID: p.KeyID, Value: sigValue}
	return json.Marshal(entry)
}

// buildFileEntry computes a versioned URL, digest, and size for one
// already self-signed catalog file (content -- see signCatalogFile/
// signChangeFile): digest is always computed over content as given (the
// canonical, decompressed bytes a reader must verify against) -- content
// is never mutated -- while the returned served bytes and Size reflect
// what's actually written to storage, gzip-compressed with a
// ".gz"-suffixed filename/URL when Gzip is on.
func (p Params) buildFileEntry(catalogID string, version int64, suffix string, content []byte) (catalog.FileEntry, []byte, error) {
	served, fileSuffix, encoding, err := maybeCompress(content, suffix, p.Gzip)
	if err != nil {
		return catalog.FileEntry{}, nil, err
	}
	filename := fmt.Sprintf("%s.v%d.%s", store.LocalName(catalogID), version, fileSuffix)
	url := p.catalogPartURL(filename, fileSuffix)
	return catalog.FileEntry{
		Version:  version,
		URL:      url,
		Size:     int64(len(served)),
		Digest:   "sha-256:" + digestOf(content),
		Encoding: encoding,
	}, served, nil
}

// buildLatestFileEntry is buildFileEntry's counterpart for the "latest"
// pointer: unlike a baseline/change file, its filename carries no version
// number at all -- it is the one file this package overwrites in place on
// every publish, explicitly exempt from the immutable-URL rule every
// other file here follows.
func (p Params) buildLatestFileEntry(catalogID string, version int64, content []byte) (catalog.FileEntry, []byte, error) {
	served, fileSuffix, encoding, err := maybeCompress(content, "json", p.Gzip)
	if err != nil {
		return catalog.FileEntry{}, nil, err
	}
	filename := fmt.Sprintf("%s.latest.%s", store.LocalName(catalogID), fileSuffix)
	url := p.catalogPartURL(filename, fileSuffix)
	return catalog.FileEntry{
		Version:  version,
		URL:      url,
		Size:     int64(len(served)),
		Digest:   "sha-256:" + digestOf(content),
		Encoding: encoding,
	}, served, nil
}

// maybeCompress gzip-compresses content and appends ".gz" to suffix (plus
// reports "gzip" as the encoding) when gzipEnabled is true, or returns
// content/suffix/"" unchanged otherwise -- shared by buildFileEntry and
// buildLatestFileEntry so the served-bytes/filename/encoding convention
// stays identical for every kind of catalog file.
func maybeCompress(content []byte, suffix string, gzipEnabled bool) (served []byte, fileSuffix string, encoding string, err error) {
	if !gzipEnabled {
		return content, suffix, "", nil
	}
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(content); err != nil {
		return nil, "", "", fmt.Errorf("catalog/publisher: gzip-compressing catalog file: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, "", "", fmt.Errorf("catalog/publisher: gzip-compressing catalog file: %w", err)
	}
	return buf.Bytes(), suffix + ".gz", "gzip", nil
}

// currentVersion returns a catalog's implicit current content-lineage
// version: the last change file's version, or the baseline's version if
// there are no change files yet. Zero if there is no prior state at all.
//
// Only "live" change files -- those published after the current baseline
// (EffectiveVersion > baseline's own Version) -- count. Once a catalog
// has been compacted, prior.ChangeFiles legitimately still lists
// superseded (pre-compaction) entries for the CON-TBD-32 grace period;
// their versions predate the current baseline and must never be mistaken
// for the catalog's current version.
func currentVersion(prior store.CatalogState) int64 {
	var baselineVersion int64
	if prior.BaselineFile != nil {
		baselineVersion = prior.BaselineFile.Version
	}
	if n := len(prior.ChangeFiles); n > 0 {
		if last := prior.ChangeFiles[n-1].EffectiveVersion(); last > baselineVersion {
			return last
		}
	}
	return baselineVersion
}

// compactionDue reports whether an automatic compaction trigger
// (Params.CompactionChangeCountThreshold/CompactionSizeRatioThreshold)
// fires for prior -- checked against prior.ChangeFiles as they stand
// before this call's own new change file would be added. Both thresholds
// default to 0 (disabled); either alone can trigger compaction. False
// whenever there's no baseline to compare against yet.
//
// Counts only "live" change files (EffectiveVersion > baseline's own
// Version), same rationale as currentVersion.
func (p Params) compactionDue(prior store.CatalogState) bool {
	var baselineVersion, baselineSize int64
	if prior.BaselineFile != nil {
		baselineVersion = prior.BaselineFile.Version
		baselineSize = prior.BaselineFile.Size
	}
	var liveCount int
	var liveSize int64
	for _, cf := range prior.ChangeFiles {
		if cf.EffectiveVersion() > baselineVersion {
			liveCount++
			liveSize += cf.Size
		}
	}
	if p.CompactionChangeCountThreshold > 0 && liveCount >= p.CompactionChangeCountThreshold {
		return true
	}
	if p.CompactionSizeRatioThreshold > 0 && baselineSize > 0 {
		ratio := float64(liveSize) / float64(baselineSize)
		if ratio >= p.CompactionSizeRatioThreshold {
			return true
		}
	}
	return false
}

// validateSubmission applies a shallow structural check: a Beckn Catalog
// object has no context/message envelope, just {id, descriptor, ...}.
func validateSubmission(sub Submission) error {
	if sub.CatalogID == "" {
		return fmt.Errorf("missing catalogId")
	}
	var c struct {
		ID         string          `json:"id"`
		Descriptor json.RawMessage `json:"descriptor"`
	}
	if err := json.Unmarshal(sub.Catalog, &c); err != nil {
		return fmt.Errorf("invalid catalog JSON: %w", err)
	}
	if c.ID == "" || len(c.Descriptor) == 0 {
		return fmt.Errorf("catalog missing id/descriptor")
	}
	return nil
}

func (p Params) catalogPartURL(filename, suffix string) string {
	if p.PublicBaseURL == "" {
		return "pending-artifact-store://catalog/" + filename
	}
	if strings.HasPrefix(suffix, "changes.json") { // "changes.json" or gzip's "changes.json.gz"
		return joinURL(p.PublicBaseURL, store.CatalogsDirName, store.ChangesDirName, filename)
	}
	return joinURL(p.PublicBaseURL, store.CatalogsDirName, filename)
}

// joinURL appends parts to base, trimming exactly one "/" between each
// segment regardless of how base/parts are themselves slashed.
func joinURL(base string, parts ...string) string {
	out := strings.TrimRight(base, "/")
	for _, part := range parts {
		out += "/" + strings.Trim(part, "/")
	}
	return out
}

// digestOf returns the hex-encoded SHA-256 of body -- a plain hex digest,
// no prefix (the "sha-256:" prefix is added by the caller when building a
// FileEntry digest field, matching the file spec's convention).
func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
