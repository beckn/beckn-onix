// Package catalogpublisher implements definition.CatalogPublisher: given a
// publisher's catalog submissions, it produces a catalog index whose wire
// shape matches "Decentralized Catalog file spec.md" (the file-spec doc
// supersedes the earlier DeDi-wrapper-shaped index this package originally
// produced -- see git history for that version; catalogcrawler has not yet
// been updated to match this shape, tracked as the immediate next step).
//
// This package does not produce a DeDi manifest: the catalog index's
// location is declared directly in the subscriber's own DeDi registry
// record (meta.catalog_index_url, see core/module/handler/
// catalogPublishHandler.go's checkRegistryLinksCatalogIndex), not via a
// separate node-manifest document's catalog.catalogIndexes indirection --
// an earlier version of this package signed and returned such a manifest,
// but nothing ever consumed it (see git history).
//
// Publish diffs each submission against caller-supplied PriorState (added/
// updated/removed items in the catalog's "resources" and "offers" arrays)
// and emits either a fresh baseline (no prior state, or ForceBaseline) or a
// change file (prior state present and the diff is non-empty); an empty
// diff is a no-op. Publish holds no storage-backed state of its own -- see
// definition.PriorCatalogState's doc comment -- callers (a CLI, a handler)
// own reconstructing "what was last published" and pass it back in.
// Compaction beyond ForceBaseline-as-manual-trigger is not implemented yet
// (see the package README's phased plan).
//
// The catalog index is not signed as a whole -- trust rides on two
// independent, per-catalog signature layers instead (file spec v2):
// every catalog file (baseline and change file alike) self-signs its own
// content, a plain Ed25519 signature (pkg/security/artifactsigner) over
// the JCS-canonicalized file document with its own "signature" field
// removed; and every catalog-index entry separately self-signs itself as
// a whole (catalogId, catalogType, status, networkIds, schemaTypes, and
// every baseline/changes[] file reference together), the same
// non-circular convention applied one level up. Signatures carry no
// expiry (no validUntil): the index's own next_update bounds staleness
// instead.
package catalogpublisher

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher/localstore"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactsigner"
)

// defaultFileValidity is the fallback used for a catalog file's own
// required next_update when Config.NextUpdateIn is unset (zero) -- unlike
// the index's next_update (optional, omitted when unset), the file spec
// v2 schemas require next_update on every CatalogFile/CatalogChangeFile.
const defaultFileValidity = 24 * time.Hour

// Config controls publish behavior.
type Config struct {
	// SubscriberID is the identifier passed to KeyManager.Keyset to load
	// the signing keypair -- the same lookup key every other caller of
	// Keyset uses (see pkg/security/artifactfetcher,
	// core/module/handler/responsestep.go). Every signature.keyId, and the
	// domain embedded as the catalog index's "nodeId", both come from the
	// returned Keyset (UniqueKeyID/SubscriberID) instead of being
	// duplicated here -- that's the keymanager plugin's own config to own,
	// not this one's.
	SubscriberID string

	// NextUpdateIn sets how far in the future the index's "next_update"
	// freshness window extends from the moment of publishing. Zero omits
	// next_update entirely.
	NextUpdateIn time.Duration

	// PublicBaseURL, if set, is the one public URL prefix everything this
	// publish writes gets addressed under -- the same root a static file
	// server (or ngrok tunnel) exposes localstore's on-disk layout from.
	// The catalog index is reachable at
	// {PublicBaseURL}/{localstore.IndexDirName}/{localstore.IndexFilename},
	// baselines at
	// {PublicBaseURL}/{localstore.CatalogsDirName}/{localName}.v{version}.json,
	// and change files at
	// {PublicBaseURL}/{localstore.CatalogsDirName}/{localstore.ChangesDirName}/{localName}.v{version}.changes.json,
	// where localName is CatalogID with any "domain/" prefix stripped (file
	// spec's example: catalogId "open-economy.nfh.global/electronics-2026"
	// -> file "electronics-2026.v40.json"). A placeholder
	// ("pending-artifact-store://...") is used for both when unset -- there
	// is no ArtifactStore yet to ask for a real location.
	PublicBaseURL string

	// PublishLatest opts into publishing/maintaining each catalog's
	// "latest" entry (NFH-014 §Schema Changes): a full CatalogFile
	// overwritten in place at a stable URL, for consumers who want
	// fully-current content without ever applying changes[]. Off by
	// default -- it changes storage semantics (overwrite-in-place,
	// explicitly exempt from the immutable-URL rule every other file
	// here follows), so a caller opts in deliberately rather than getting
	// it for free.
	PublishLatest bool

	// Gzip opts into serving every CatalogFile/CatalogChangeFile (and
	// "latest", if enabled) gzip-compressed (NFH-014 §10.1,
	// "Compression"), signaled purely by the file's URL/filename extension
	// (".json.gz" vs ".json") -- never a content-type header or
	// negotiation. Off by default for direct programmatic callers
	// (catalogpublisher.Config's own zero value); the plugin/CLI entry
	// points default it on. Digest/signature are always computed over the
	// canonical, decompressed content regardless of this setting -- only
	// the served bytes and the index entry's reported size (the actual
	// served size) change.
	Gzip bool
}

// Publisher implements definition.CatalogPublisher.
type Publisher struct {
	keyManager definition.KeyManager
	config     *Config
}

// New creates a Publisher instance.
func New(ctx context.Context, keyManager definition.KeyManager, cfg *Config) (*Publisher, func() error, error) {
	if keyManager == nil {
		return nil, nil, fmt.Errorf("catalogpublisher: KeyManager plugin not configured")
	}
	if cfg == nil || cfg.SubscriberID == "" {
		return nil, nil, fmt.Errorf("catalogpublisher: subscriberID is required")
	}
	p := &Publisher{keyManager: keyManager, config: cfg}
	log.Debugf(ctx, "catalogpublisher: New, subscriberId=%s, publicBaseURL=%s", cfg.SubscriberID, cfg.PublicBaseURL)
	return p, func() error { return nil }, nil
}

// --- Catalog index wire types -------------------------------------------
//
// A plain Beckn file; DeDi never reads it, and it is not required to be
// signed as a whole -- trust rides on each catalog entry's own signature
// (file spec v2, "The catalog index").

// catalogIndexDoc carries no top-level version field (NFH-014
// §Versioning, "There is no whole-index version field") -- a catalog
// index as a whole is unsigned, so a document-level counter would be
// forgeable; a DS detects that the index changed via conditional HTTP
// (ETag/If-Modified-Since) instead, at no extra cost.
type catalogIndexDoc struct {
	NodeID     string            `json:"nodeId"`
	NextUpdate *time.Time        `json:"next_update,omitempty"`
	Catalogs   []json.RawMessage `json:"catalogs"`
}

// catalogEntry self-signs as a whole (NFH-014 §Schema Changes, "Catalog
// Index"): Signature covers every other field here together, JCS-
// canonicalized with "signature" itself removed -- see signEntry. Every
// entry carries one, including retired (tombstone) entries.
//
// EntryVersion is distinct from Baseline/Changes' own file-lineage
// versions (§Versioning): it bumps on *any* entry change, content or
// metadata (NetworkIds/SchemaTypes/CatalogType/Dependencies/IsActive/
// RetiredAt), giving a DS a cheap first check independent of whether a
// new file was actually published.
//
// IsActive is *bool, not bool: omitempty on a plain bool would also
// drop an explicit PAUSED (false) entry, indistinguishable from RETIRED
// (which omits IsActive entirely, per NFH-014's Catalog Index examples).
type catalogEntry struct {
	CatalogID    string             `json:"catalogId"`
	EntryVersion int                `json:"entryVersion"`
	CatalogType  string             `json:"catalogType,omitempty"`
	Dependencies *dependenciesWire  `json:"dependencies,omitempty"`
	NetworkIds   []string           `json:"networkIds,omitempty"`
	SchemaTypes  []string           `json:"schemaTypes,omitempty"`
	IsActive     *bool              `json:"isActive,omitempty"`
	Baseline     *fileEntry         `json:"baseline,omitempty"`
	Changes      []fileEntry        `json:"changes,omitempty"`
	Latest       *fileEntry         `json:"latest,omitempty"`
	RetiredAt    *time.Time         `json:"retiredAt,omitempty"`
	CrawlHint    string             `json:"crawlHint,omitempty"`
	Signature    entrySignatureWire `json:"signature"`
}

// dependenciesWire is a REGULAR catalog entry's declared MASTER
// dependencies (NFH-014 §10.3); wrapped in an object (not a bare
// masters[] array) so a future dependency kind can be added without a
// breaking schema change.
type dependenciesWire struct {
	Masters []masterDependencyWire `json:"masters,omitempty"`
}

type masterDependencyWire struct {
	CatalogID string `json:"catalogId"`
	IndexURL  string `json:"indexUrl"`
}

// fileEntry points at one already self-signed catalog file (see
// catalogFileDoc/changeFileDoc) -- digest/size are computed over that
// file's final, already-signed bytes, so no signature is duplicated here;
// file-level integrity lives in the file itself.
type fileEntry struct {
	Version int    `json:"version"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	Digest  string `json:"digest"` // "sha-256:<hex>"
}

// entrySignatureWire is the catalog-index entry's own self-signature
// (file spec v2's catalog-index example: `{"keyId": "...", "value": "..."}`
// -- no canonicalization field, unlike fileSignatureWire below).
type entrySignatureWire struct {
	KeyID string `json:"keyId"`
	Value string `json:"value"`
}

// fileSignatureWire is a catalog file's (baseline or change file) own
// self-signature, per the file spec v2's CatalogFile/CatalogChangeFile
// schemas: `{keyId, canonicalization: "JCS", value}`.
type fileSignatureWire struct {
	KeyID            string `json:"keyId"`
	Canonicalization string `json:"canonicalization"`
	Value            string `json:"value"`
}

// catalogFileDoc is the self-signed baseline file (file spec v2's
// `CatalogFile`): the submitted Catalog object wrapped with its own
// signature, plus the file-level identity/freshness fields the schema now
// requires as siblings of "catalog" (CatalogID/Version/NextUpdate) so the
// file is independently verifiable without the index entry that points at
// it. The wrap never reopens Catalog for changes -- beckn.yaml's Catalog
// schema still governs the wire format; a crawler unwraps `.catalog`
// before anything reaches the wire.
type catalogFileDoc struct {
	CatalogID  string            `json:"catalogId"`
	Version    int               `json:"version"`
	NextUpdate time.Time         `json:"next_update"`
	Catalog    json.RawMessage   `json:"catalog"`
	Signature  fileSignatureWire `json:"signature"`
}

// changeFileDoc is the change-file shape for one publish (file spec v2's
// `CatalogChangeFile`), keyed by id never by position. Upserts merge added
// and updated items into one list -- the receiver replaces by id either
// way -- and Removals names ids only. NextUpdate is required here too
// (same freshness rationale as catalogFileDoc). Self-signed like
// catalogFileDoc.
type changeFileDoc struct {
	CatalogID   string            `json:"catalogId"`
	FromVersion int               `json:"fromVersion"`
	ToVersion   int               `json:"toVersion"`
	NextUpdate  time.Time         `json:"next_update"`
	Resources   diffBlock         `json:"resources"`
	Offers      diffBlock         `json:"offers"`
	Catalog     json.RawMessage   `json:"catalog,omitempty"`
	Signature   fileSignatureWire `json:"signature"`
}

type diffBlock struct {
	Upserts  []json.RawMessage `json:"upserts,omitempty"`
	Removals []string          `json:"removals,omitempty"`
}

func (b diffBlock) isEmpty() bool { return len(b.Upserts) == 0 && len(b.Removals) == 0 }

// Publish validates each submission, diffs it against any PriorState for
// its catalogId, builds the resulting catalog-index entry (baseline,
// change file, or a carried-forward no-op), folds in retirements and
// carried-forward untouched entries, and signs the manifest (the index
// itself is not signed as a whole). A submission that fails validation or
// diffing is reported as a non-fatal definition.PublishError and skipped;
// it does not fail the rest of the batch.
func (p *Publisher) Publish(ctx context.Context, req definition.PublishRequest) (definition.PublishResult, error) {
	now := time.Now()
	result := definition.PublishResult{PublishedAt: now}
	log.Debugf(ctx, "catalogpublisher: Publish called, subscriberId=%s, %d catalog(s), %d retire(s), forceBaseline=%v",
		p.config.SubscriberID, len(req.Catalogs), len(req.Retire), req.ForceBaseline)

	keyset, err := p.keyManager.Keyset(ctx, p.config.SubscriberID)
	if err != nil {
		return result, fmt.Errorf("catalogpublisher: loading keyset %q: %w", p.config.SubscriberID, err)
	}
	priv, _, err := decodeKeyset(keyset)
	if err != nil {
		return result, fmt.Errorf("catalogpublisher: decoding keyset %q: %w", p.config.SubscriberID, err)
	}
	keyID := keyset.UniqueKeyID
	domain := keyset.SubscriberID

	var nextUpdate *time.Time
	fileValidityIn := p.config.NextUpdateIn
	if fileValidityIn <= 0 {
		fileValidityIn = defaultFileValidity
	}
	fileNextUpdate := now.Add(fileValidityIn)
	if p.config.NextUpdateIn > 0 {
		nextUpdate = &fileNextUpdate
	}

	submitted := make(map[string]bool, len(req.Catalogs))
	retireSet := make(map[string]bool, len(req.Retire))
	for _, id := range req.Retire {
		retireSet[id] = true
	}

	var entries []json.RawMessage

	for _, sub := range req.Catalogs {
		submitted[sub.CatalogID] = true
		if err := validateSubmission(sub); err != nil {
			result.Errors = append(result.Errors, definition.PublishError{
				CatalogID: sub.CatalogID, Stage: "validate", Reason: err.Error(), Fatal: false,
			})
			continue
		}

		outcome, entry, err := p.publishOne(sub, req.PriorState[sub.CatalogID], req.ForceBaseline, priv, keyID, fileNextUpdate)
		if err != nil {
			result.Errors = append(result.Errors, definition.PublishError{
				CatalogID: sub.CatalogID, Stage: "diff", Reason: err.Error(), Fatal: false,
			})
			continue
		}
		log.Debugf(ctx, "catalogpublisher: %s mode=%s version=%d entryVersion=%d changed=%v", sub.CatalogID, outcome.Mode, outcome.Version, outcome.EntryVersion, outcome.Changed)

		raw, err := p.signEntry(entry, keyID, priv)
		if err != nil {
			return result, fmt.Errorf("catalogpublisher: signing catalog entry %q: %w", sub.CatalogID, err)
		}
		entries = append(entries, raw)
		result.Catalogs = append(result.Catalogs, outcome)
	}

	tombstoned := make(map[string]bool, len(req.Retire))
	for _, id := range req.Retire {
		if submitted[id] || tombstoned[id] {
			continue // submitting and retiring the same catalogId in one call: submission wins; duplicate retire ids collapse to one tombstone
		}
		tombstoned[id] = true
		prior := req.PriorState[id]
		raw, err := p.signEntry(catalogEntry{
			CatalogID:    id,
			EntryVersion: prior.EntryVersion + 1,
			CatalogType:  prior.CatalogType,
			NetworkIds:   prior.NetworkIds,
			SchemaTypes:  prior.SchemaTypes,
			RetiredAt:    &now,
		}, keyID, priv)
		if err != nil {
			return result, fmt.Errorf("catalogpublisher: signing tombstone %q: %w", id, err)
		}
		entries = append(entries, raw)
	}

	for _, raw := range req.CarryForward {
		var probe struct {
			CatalogID string `json:"catalogId"`
		}
		if json.Unmarshal(raw, &probe) == nil && (submitted[probe.CatalogID] || retireSet[probe.CatalogID]) {
			continue
		}
		entries = append(entries, raw)
	}

	indexBytes, err := json.Marshal(catalogIndexDoc{
		NodeID:     domain,
		NextUpdate: nextUpdate,
		Catalogs:   entries,
	})
	if err != nil {
		return result, fmt.Errorf("catalogpublisher: marshaling catalog index: %w", err)
	}
	result.Index = indexBytes
	log.Debugf(ctx, "catalogpublisher: built catalog index, nodeId=%s, %d entries, indexURL=%s", domain, len(entries), p.indexURL())
	log.Debugf(ctx, "catalogpublisher: Publish done, keyId=%s, domain=%s, %d catalog(s) published, %d error(s)", keyID, domain, len(result.Catalogs), len(result.Errors))

	return result, nil
}

// currentVersion returns a catalog's implicit current version: the last
// change file's version, or the baseline's version if there are no change
// files yet. Zero if there is no prior state at all.
func currentVersion(prior definition.PriorCatalogState) int {
	if n := len(prior.ChangeFiles); n > 0 {
		return prior.ChangeFiles[n-1].Version
	}
	if prior.BaselineFile != nil {
		return prior.BaselineFile.Version
	}
	return 0
}

// publishOne decides baseline vs. change-file vs. metadata-only vs. no-op
// for one submission and builds both its definition.CatalogPublishOutcome
// and its catalogEntry. The returned entry is not yet signed -- Publish
// signs every entry uniformly (submitted and tombstoned alike) via
// signEntry.
//
// entryVersion (NFH-014 §Versioning) bumps whenever *anything* about the
// entry changes -- content (a new baseline/change file) or metadata
// (CatalogType/NetworkIds/SchemaTypes/IsActive/Dependencies/CrawlHint) --
// independent of Baseline/Changes' own file-lineage versions, which bump
// only when a file is actually (re)published.
func (p *Publisher) publishOne(sub definition.CatalogSubmission, prior definition.PriorCatalogState, forceBaseline bool, priv ed25519.PrivateKey, keyID string, fileNextUpdate time.Time) (definition.CatalogPublishOutcome, catalogEntry, error) {
	hasPrior := prior.Catalog != nil
	catalogType := sub.CatalogType
	if catalogType == "" {
		catalogType = "REGULAR"
	}
	isActive := catalogIsActive(sub.Catalog)

	entry := catalogEntry{
		CatalogID:    sub.CatalogID,
		CatalogType:  catalogType,
		Dependencies: dependenciesToWire(sub.Dependencies),
		NetworkIds:   sub.NetworkIds,
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
		entry.Baseline = fileRefToWire(prior.BaselineFile)
		entry.Changes = fileRefsToWire(prior.ChangeFiles)
	}

	if !hasPrior || forceBaseline {
		version := currentVersion(prior) + 1 // 0+1 == 1 for a brand-new catalog
		content, err := p.signCatalogFile(sub.CatalogID, version, fileNextUpdate, sub.Catalog, keyID, priv)
		if err != nil {
			return definition.CatalogPublishOutcome{}, catalogEntry{}, err
		}
		fe, served, err := p.buildFileEntry(sub.CatalogID, version, "json", content)
		if err != nil {
			return definition.CatalogPublishOutcome{}, catalogEntry{}, err
		}
		entry.Baseline = &fe
		// entry.Changes is deliberately left as whatever prior.ChangeFiles
		// carried (set above), not reset to nil: on a forced re-baseline
		// (compaction), NFH-014 CON-TBD-32 requires the change files that
		// led up to the new baseline to stay *listed* -- not merely
		// hosted -- for a grace period, so a DS mid-lineage can still
		// reach equivalent content by applying them instead of fetching
		// the baseline. Publish holds no timer of its own (PriorCatalogState's
		// doc comment); how long to keep passing them here is the
		// caller's own policy.
		entry.EntryVersion = prior.EntryVersion + 1

		outcome := definition.CatalogPublishOutcome{
			CatalogID: sub.CatalogID, Version: version, EntryVersion: entry.EntryVersion, Changed: true, Digest: fe.Digest, Mode: "baseline",
			Content: content, ServedContent: served, Compressed: p.config.Gzip,
		}
		return p.finishEntry(sub, entry, outcome, priv, keyID, fileNextUpdate)
	}

	diff, changeCatalog, err := diffCatalogs(prior.Catalog, sub.Catalog)
	if err != nil {
		return definition.CatalogPublishOutcome{}, catalogEntry{}, err
	}
	contentChanged := !diff.Resources.isEmpty() || !diff.Offers.isEmpty() || changeCatalog != nil

	if !contentChanged {
		version := currentVersion(prior)
		if !metadataChanged {
			entry.EntryVersion = prior.EntryVersion
			outcome := definition.CatalogPublishOutcome{CatalogID: sub.CatalogID, Version: version, EntryVersion: entry.EntryVersion, Changed: false, Mode: "unchanged"}
			return p.finishEntry(sub, entry, outcome, priv, keyID, fileNextUpdate)
		}
		entry.EntryVersion = prior.EntryVersion + 1
		outcome := definition.CatalogPublishOutcome{CatalogID: sub.CatalogID, Version: version, EntryVersion: entry.EntryVersion, Changed: true, Mode: "metadata"}
		return p.finishEntry(sub, entry, outcome, priv, keyID, fileNextUpdate)
	}

	fromVersion := currentVersion(prior)
	toVersion := fromVersion + 1
	changeDoc := changeFileDoc{
		CatalogID: sub.CatalogID, FromVersion: fromVersion, ToVersion: toVersion, NextUpdate: fileNextUpdate,
		Resources: diff.Resources, Offers: diff.Offers, Catalog: changeCatalog,
	}
	content, err := p.signChangeFile(changeDoc, keyID, priv)
	if err != nil {
		return definition.CatalogPublishOutcome{}, catalogEntry{}, err
	}

	fe, served, err := p.buildFileEntry(sub.CatalogID, toVersion, "changes.json", content)
	if err != nil {
		return definition.CatalogPublishOutcome{}, catalogEntry{}, err
	}
	entry.Changes = append(entry.Changes, fe)
	entry.EntryVersion = prior.EntryVersion + 1

	outcome := definition.CatalogPublishOutcome{
		CatalogID: sub.CatalogID, Version: toVersion, EntryVersion: entry.EntryVersion, Changed: true, Digest: fe.Digest, Mode: "change",
		Content: content, ServedContent: served, Compressed: p.config.Gzip,
	}
	return p.finishEntry(sub, entry, outcome, priv, keyID, fileNextUpdate)
}

// finishEntry adds a "latest" full-CatalogFile pointer (NFH-014 §Schema
// Changes) when Config.PublishLatest is on -- regenerated from sub.Catalog
// on every call regardless of Mode, since "latest" always mirrors current
// content, not a specific lineage step. A no-op when PublishLatest is off,
// which every publishOne return path funnels through so "latest" (or its
// absence) is applied uniformly rather than duplicated at each call site.
func (p *Publisher) finishEntry(sub definition.CatalogSubmission, entry catalogEntry, outcome definition.CatalogPublishOutcome, priv ed25519.PrivateKey, keyID string, fileNextUpdate time.Time) (definition.CatalogPublishOutcome, catalogEntry, error) {
	if !p.config.PublishLatest {
		return outcome, entry, nil
	}
	content, err := p.signCatalogFile(sub.CatalogID, outcome.Version, fileNextUpdate, sub.Catalog, keyID, priv)
	if err != nil {
		return definition.CatalogPublishOutcome{}, catalogEntry{}, err
	}
	fe, served, err := p.buildLatestFileEntry(sub.CatalogID, outcome.Version, content)
	if err != nil {
		return definition.CatalogPublishOutcome{}, catalogEntry{}, err
	}
	entry.Latest = &fe
	outcome.LatestContent = content
	outcome.LatestServedContent = served
	outcome.LatestDigest = fe.Digest
	outcome.Compressed = p.config.Gzip
	return outcome, entry, nil
}

// catalogIsActive reads the submitted Catalog's own pre-existing
// "isActive" field (NFH-014: the entry's isActive mirrors catalog.isActive,
// it is never a second, independently-supplied input) -- defaulting to
// true when absent, matching Catalog's public-by-default posture.
func catalogIsActive(catalog json.RawMessage) bool {
	var probe struct {
		IsActive *bool `json:"isActive"`
	}
	if json.Unmarshal(catalog, &probe) != nil || probe.IsActive == nil {
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

func masterDependenciesEqual(a, b []definition.MasterDependency) bool {
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

func dependenciesToWire(deps []definition.MasterDependency) *dependenciesWire {
	if len(deps) == 0 {
		return nil
	}
	masters := make([]masterDependencyWire, len(deps))
	for i, d := range deps {
		masters[i] = masterDependencyWire{CatalogID: d.CatalogID, IndexURL: d.IndexURL}
	}
	return &dependenciesWire{Masters: masters}
}

// signCatalogFile builds the file spec's self-signed CatalogFile (baseline):
// {catalog, signature}. "Avoiding circular signing" (file spec): the
// signing input is the JCS canonicalization of this document with
// "signature" itself removed, so the pre-signing marshal's placeholder
// Signature value never matters -- it's stripped before signing anyway.
// Returns the final, already-signed bytes -- digest/size (buildFileEntry)
// are computed over these, not the bare catalog content.
func (p *Publisher) signCatalogFile(catalogID string, version int, nextUpdate time.Time, catalog json.RawMessage, keyID string, priv ed25519.PrivateKey) ([]byte, error) {
	doc := catalogFileDoc{CatalogID: catalogID, Version: version, NextUpdate: nextUpdate, Catalog: catalog}
	unsigned, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshaling catalog file: %w", err)
	}
	sigValue, err := artifactsigner.SignJSON(unsigned, "signature", priv)
	if err != nil {
		return nil, fmt.Errorf("signing catalog file: %w", err)
	}
	doc.Signature = fileSignatureWire{KeyID: keyID, Canonicalization: "JCS", Value: sigValue}
	return json.Marshal(doc)
}

// signChangeFile is signCatalogFile's counterpart for a CatalogChangeFile:
// doc's own fields (catalogId, fromVersion, toVersion, resources, offers,
// catalog) are signed with "signature" removed, then the signature is
// attached and the final document re-marshaled.
func (p *Publisher) signChangeFile(doc changeFileDoc, keyID string, priv ed25519.PrivateKey) ([]byte, error) {
	unsigned, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshaling change file: %w", err)
	}
	sigValue, err := artifactsigner.SignJSON(unsigned, "signature", priv)
	if err != nil {
		return nil, fmt.Errorf("signing change file: %w", err)
	}
	doc.Signature = fileSignatureWire{KeyID: keyID, Canonicalization: "JCS", Value: sigValue}
	return json.Marshal(doc)
}

// signEntry self-signs a catalog-index entry as a whole (file spec v2):
// every other field of entry, JCS-canonicalized with "signature" removed.
// Applies uniformly to ACTIVE and RETIRED (tombstone) entries alike.
func (p *Publisher) signEntry(entry catalogEntry, keyID string, priv ed25519.PrivateKey) (json.RawMessage, error) {
	unsigned, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("marshaling catalog entry: %w", err)
	}
	sigValue, err := artifactsigner.SignJSON(unsigned, "signature", priv)
	if err != nil {
		return nil, fmt.Errorf("signing catalog entry: %w", err)
	}
	entry.Signature = entrySignatureWire{KeyID: keyID, Value: sigValue}
	return json.Marshal(entry)
}

// buildFileEntry computes a versioned URL, digest, and size for one
// already self-signed catalog file (content -- see signCatalogFile/
// signChangeFile), per NFH-014's §10.1 compression rule: digest is always
// computed over content as given (the canonical, decompressed bytes a DS
// must verify against, CON-TBD-29) -- content is never mutated -- while
// the returned served bytes and Size reflect what's actually written to
// storage, gzip-compressed with a ".gz"-suffixed filename/URL when
// Config.Gzip is on.
func (p *Publisher) buildFileEntry(catalogID string, version int, suffix string, content []byte) (fileEntry, []byte, error) {
	served, fileSuffix, err := p.maybeCompress(content, suffix)
	if err != nil {
		return fileEntry{}, nil, err
	}
	filename := fmt.Sprintf("%s.v%d.%s", localCatalogName(catalogID), version, fileSuffix)
	url := p.catalogPartURL(filename, fileSuffix)
	return fileEntry{
		Version: version,
		URL:     url,
		Size:    int64(len(served)),
		Digest:  "sha-256:" + digestOf(content),
	}, served, nil
}

// buildLatestFileEntry is buildFileEntry's counterpart for the "latest"
// pointer (NFH-014 §Schema Changes): unlike a baseline/change file, its
// filename carries no version number at all -- it is the one file this
// package overwrites in place on every publish, explicitly exempt from
// the immutable-URL rule every other file here follows.
func (p *Publisher) buildLatestFileEntry(catalogID string, version int, content []byte) (fileEntry, []byte, error) {
	served, fileSuffix, err := p.maybeCompress(content, "json")
	if err != nil {
		return fileEntry{}, nil, err
	}
	filename := fmt.Sprintf("%s.latest.%s", localCatalogName(catalogID), fileSuffix)
	url := p.catalogPartURL(filename, fileSuffix)
	return fileEntry{
		Version: version,
		URL:     url,
		Size:    int64(len(served)),
		Digest:  "sha-256:" + digestOf(content),
	}, served, nil
}

// maybeCompress gzip-compresses content and appends ".gz" to suffix when
// Config.Gzip is on, or returns content/suffix unchanged otherwise --
// shared by buildFileEntry and buildLatestFileEntry so the served-bytes/
// filename convention stays identical for every kind of catalog file.
func (p *Publisher) maybeCompress(content []byte, suffix string) ([]byte, string, error) {
	if !p.config.Gzip {
		return content, suffix, nil
	}
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(content); err != nil {
		return nil, "", fmt.Errorf("gzip-compressing catalog file: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, "", fmt.Errorf("gzip-compressing catalog file: %w", err)
	}
	return buf.Bytes(), suffix + ".gz", nil
}

// localCatalogName returns catalogID with any "domain/" prefix stripped,
// matching the file spec's example filenames (catalogId
// "open-economy.nfh.global/electronics-2026" -> "electronics-2026.v40.json").
func localCatalogName(catalogID string) string {
	if i := strings.LastIndex(catalogID, "/"); i != -1 {
		return catalogID[i+1:]
	}
	return catalogID
}

func fileRefToWire(fr *definition.FileRef) *fileEntry {
	if fr == nil {
		return nil
	}
	fe := fileRefValueToWire(*fr)
	return &fe
}

func fileRefValueToWire(fr definition.FileRef) fileEntry {
	return fileEntry{
		Version: fr.Version,
		URL:     fr.URL,
		Size:    fr.Size,
		Digest:  fr.Digest,
	}
}

func fileRefsToWire(frs []definition.FileRef) []fileEntry {
	if len(frs) == 0 {
		return nil
	}
	out := make([]fileEntry, len(frs))
	for i, fr := range frs {
		out[i] = fileRefValueToWire(fr)
	}
	return out
}

// catalogDiff is the result of comparing two catalogs' "resources" and
// "offers" arrays by item id.
type catalogDiff struct {
	Resources diffBlock
	Offers    diffBlock
}

// diffCatalogs compares prior and next by their top-level "resources" and
// "offers" arrays, matched by each item's "id" field, and separately
// detects catalog-level attribute changes: any top-level field other than
// "id" (identity, never diffed) and "resources"/"offers" (diffed
// separately above) -- not a fixed list, so it covers whatever a Catalog
// object carries beyond those, matching the file spec's own examples
// ("name, validity window") without having to special-case each one.
// changeCatalog is nil when no catalog-level attributes changed.
func diffCatalogs(prior, next json.RawMessage) (catalogDiff, json.RawMessage, error) {
	var priorFields, nextFields map[string]json.RawMessage
	if err := json.Unmarshal(prior, &priorFields); err != nil {
		return catalogDiff{}, nil, fmt.Errorf("parsing prior catalog: %w", err)
	}
	if err := json.Unmarshal(next, &nextFields); err != nil {
		return catalogDiff{}, nil, fmt.Errorf("parsing submitted catalog: %w", err)
	}

	resourcesDiff, err := diffArrayField(priorFields, nextFields, "resources")
	if err != nil {
		return catalogDiff{}, nil, fmt.Errorf("diffing resources: %w", err)
	}
	offersDiff, err := diffArrayField(priorFields, nextFields, "offers")
	if err != nil {
		return catalogDiff{}, nil, fmt.Errorf("diffing offers: %w", err)
	}
	changeCatalog := diffCatalogAttributes(priorFields, nextFields)
	return catalogDiff{Resources: resourcesDiff, Offers: offersDiff}, changeCatalog, nil
}

// diffArrayField diffs priorFields[field] against nextFields[field] (each a
// json.RawMessage array, defaulting to empty when the field is absent),
// matched by item id, merging added+updated into one Upserts list.
// priorFields/nextFields are each catalog's already-parsed top-level shape
// (see diffCatalogs), so a submission's prior/next JSON is decoded once
// overall rather than once per field diffed.
func diffArrayField(priorFields, nextFields map[string]json.RawMessage, field string) (diffBlock, error) {
	priorItems, _, err := itemsByIDOrdered(priorFields, field)
	if err != nil {
		return diffBlock{}, fmt.Errorf("prior catalog: %w", err)
	}
	nextItems, nextIDs, err := itemsByIDOrdered(nextFields, field)
	if err != nil {
		return diffBlock{}, fmt.Errorf("submitted catalog: %w", err)
	}

	var block diffBlock
	for _, id := range nextIDs {
		item := nextItems[id]
		if old, ok := priorItems[id]; !ok || !jsonEqual(old, item) {
			block.Upserts = append(block.Upserts, item)
		}
	}
	for id := range priorItems {
		if _, ok := nextItems[id]; !ok {
			block.Removals = append(block.Removals, id)
		}
	}
	sort.Strings(block.Removals)
	return block, nil
}

// itemsByIDOrdered reads fields[field] (a catalog's already-parsed top-level
// shape) as an array of {id, ...} items, returning them by id plus the ids
// in their original array order so diff output (Upserts) is deterministic
// rather than depending on Go's randomized map iteration order.
func itemsByIDOrdered(fields map[string]json.RawMessage, field string) (map[string]json.RawMessage, []string, error) {
	raw, ok := fields[field]
	if !ok || len(raw) == 0 {
		return map[string]json.RawMessage{}, nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", field, err)
	}
	m := make(map[string]json.RawMessage, len(items))
	ids := make([]string, 0, len(items))
	for _, item := range items {
		id, err := itemID(item)
		if err != nil {
			return nil, nil, err
		}
		// A later item with the same id overwrites the map entry (last
		// write wins), so ids must not gain a second entry for it too --
		// otherwise diffArrayField would emit the same upsert twice.
		if _, dup := m[id]; !dup {
			ids = append(ids, id)
		}
		m[id] = item
	}
	return m, ids, nil
}

func itemID(raw json.RawMessage) (string, error) {
	var withID struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &withID); err != nil {
		return "", fmt.Errorf("parsing item: %w", err)
	}
	if withID.ID == "" {
		return "", fmt.Errorf("item missing id")
	}
	return withID.ID, nil
}

// catalogAttributeFieldsToSkip are handled elsewhere and never belong in
// the change file's "catalog" overlay: "id" is the catalog's identity
// (never diffed), "resources"/"offers" are diffed separately as arrays
// keyed by item id, not as whole-field replacements.
var catalogAttributeFieldsToSkip = map[string]bool{"id": true, "resources": true, "offers": true}

// diffCatalogAttributes returns a non-nil json.RawMessage carrying every
// top-level catalog field (other than id/resources/offers) that changed
// or is new between priorFields and nextFields (each catalog's
// already-parsed top-level shape, see diffCatalogs), or nil if none did.
// Not a fixed field list -- a Catalog object can carry anything beyond the
// ones diffed elsewhere (the file spec's own examples: "name, validity
// window"), and all of it needs to round-trip through a change file, not
// just whichever fields happen to be hardcoded here.
func diffCatalogAttributes(priorFields, nextFields map[string]json.RawMessage) json.RawMessage {
	changed := map[string]json.RawMessage{}
	for field, nv := range nextFields {
		if catalogAttributeFieldsToSkip[field] {
			continue
		}
		if pv, ok := priorFields[field]; !ok || !jsonEqual(pv, nv) {
			changed[field] = nv
		}
	}
	if len(changed) == 0 {
		return nil
	}
	// changed only holds json.RawMessage values already produced by a
	// successful Unmarshal above, so marshaling map[string]json.RawMessage
	// back out cannot fail.
	raw, _ := json.Marshal(changed)
	return raw
}

// jsonEqual compares two JSON values semantically (decoded structure, not
// raw bytes) so whitespace/key-order differences don't register as an
// update.
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// decodeKeyset decodes a model.Keyset's base64-encoded signing keypair into
// raw Ed25519 keys, matching the exact encoding convention
// simplekeymanager/keymanager already use: SigningPrivate is
// base64(seed), expanded via ed25519.NewKeyFromSeed; SigningPublic is
// base64(rawPublicKey).
func decodeKeyset(keyset *model.Keyset) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	if keyset == nil {
		return nil, nil, fmt.Errorf("nil keyset")
	}
	seed, err := base64.StdEncoding.DecodeString(keyset.SigningPrivate)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding signing private key: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, nil, fmt.Errorf("invalid signing private key length %d, want %d", len(seed), ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)

	pub, err := base64.StdEncoding.DecodeString(keyset.SigningPublic)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding signing public key: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return nil, nil, fmt.Errorf("invalid signing public key length %d, want %d", len(pub), ed25519.PublicKeySize)
	}

	return priv, ed25519.PublicKey(pub), nil
}

// digestOf returns the hex-encoded SHA-256 of body, matching
// artifactfetcher's digest convention (plain hex, no prefix -- the
// "sha-256:" prefix is added by the caller when building a fileEntry
// digest field, matching the file spec's convention).
func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// validateSubmission applies the same shallow structural check
// catalogcrawler applies on the way in: a Beckn Catalog object has no
// context/message envelope, just {id, descriptor, ...}. Keeping this
// duplicated rather than shared avoids a cross-package dependency between
// the two plugins for a few lines of logic; it should be lifted into a
// shared validator once a real schemaValidator plugin call replaces both
// inline checks (tracked as an open item on both sides).
func validateSubmission(sub definition.CatalogSubmission) error {
	if sub.CatalogID == "" {
		return fmt.Errorf("missing catalogId")
	}
	var catalog struct {
		ID         string          `json:"id"`
		Descriptor json.RawMessage `json:"descriptor"`
	}
	if err := json.Unmarshal(sub.Catalog, &catalog); err != nil {
		return fmt.Errorf("invalid catalog JSON: %w", err)
	}
	if catalog.ID == "" || len(catalog.Descriptor) == 0 {
		return fmt.Errorf("catalog missing id/descriptor")
	}
	return nil
}

// indexURL returns the configured index location under PublicBaseURL, or a
// placeholder when no ArtifactStore-assigned location exists yet.
func (p *Publisher) indexURL() string {
	if p.config.PublicBaseURL != "" {
		return joinURL(p.config.PublicBaseURL, localstore.IndexDirName, localstore.IndexFilename)
	}
	return "pending-artifact-store://catalog-index.json"
}

// IndexURL implements definition.CatalogPublisher.
func (p *Publisher) IndexURL() string { return p.indexURL() }

// catalogPartURL returns the configured location for one of a catalog's
// versioned file names (see buildFileEntry) under PublicBaseURL, or a
// placeholder when no ArtifactStore-assigned location exists yet. A change
// file (suffix "changes.json") is addressed under catalogs/changes/,
// matching localstore.CatalogFilePath's on-disk layout; a baseline sits
// directly under catalogs/.
func (p *Publisher) catalogPartURL(filename, suffix string) string {
	if p.config.PublicBaseURL == "" {
		return "pending-artifact-store://catalog/" + filename
	}
	if strings.HasPrefix(suffix, "changes.json") { // "changes.json" or gzip's "changes.json.gz"
		return joinURL(p.config.PublicBaseURL, localstore.CatalogsDirName, localstore.ChangesDirName, filename)
	}
	return joinURL(p.config.PublicBaseURL, localstore.CatalogsDirName, filename)
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
