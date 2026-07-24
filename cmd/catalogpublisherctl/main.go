// Command catalogpublisherctl is a minimal, throwaway-for-demo CLI around
// catalogpublisher.Publisher: point it at a catalog JSON file and an output
// directory, and it writes a signed manifest and a catalog index (whose
// file entries carry their own signatures) to that directory, plus the
// catalog's versioned baseline/change files. Running it again with an
// updated catalog (same catalogId) against the same output directory diffs
// against what's on disk and produces a change file with a bumped version
// instead of a fresh baseline.
//
// This exists to demonstrate and exercise catalogpublisher.Publish
// end-to-end without needing ONIX's HTTP server, PluginManager, or a
// handler -- Publish is a plain in-process call, and this file is one of
// several equally valid ways to invoke it (a CLI here, an HTTP handler, a
// future desktop app all wire the same call differently).
//
// catalogpublisherctl owns all persistence itself (signing key, prior
// state reconstruction, carrying forward every other catalog in the
// index) -- Publish itself holds no storage-backed state, per
// definition.PriorCatalogState's doc comment.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher"
)

// dediManifestFilename is the well-known manifest's real filename per the
// decentralized-catalog file spec ("The manifest at /.well-known/
// dedi.index.json"). Despite the filename, this is NOT the catalog index
// (that's catalogIndexFilename, below) -- a naming coincidence, not a
// hint that they're the same file. In a real deployment this is served at
// {domain}/.well-known/dedi.index.json; here it's written under a local
// .well-known/ subdirectory purely to mirror that path shape.
const dediManifestFilename = "dedi.index.json"

// catalogIndexFilename is the catalog index's filename, matching the
// manifest's files[].name value ("becknCatalogs" -- see
// catalogIndexFileName in catalogpublisher.go). Written under a "dedi"
// subdirectory of the output dir; the per-catalog baseline/change files
// live separately under "catalogs/" (see catalogsDirName), flat -- not one
// subdirectory per catalogId -- matching the file spec's own example URLs
// (all catalog files for a domain sit under one shared path).
const catalogIndexFilename = "becknCatalogs.index.json"

// catalogsDirName is the top-level output subdirectory holding every
// catalog's versioned files (e.g. electronics-2026.v40.json,
// electronics-2026.v41.changes.json), flat.
const catalogsDirName = "catalogs"

// defaultIndexSchemaURL is the catalog-index JSON-Schema from the live
// reference fixture (onix-catalog-crawler-plugin-requirements.md §10) --
// used as this CLI's default so files[].schema is populated without
// requiring a flag every run.
const defaultIndexSchemaURL = "https://raw.githubusercontent.com/beckn/starter-kit/catalog-crawler/schemas/Beckn_catalog_index.json"

func main() {
	catalogPath := flag.String("catalog", "", "path to a Beckn Catalog JSON file")
	catalogID := flag.String("catalogId", "", `catalog id; a bare name (no "/") is prefixed with -domain, matching the file spec's "domain/localName" convention. Defaults to "{domain}/{the catalog's own top-level id}"`)
	outDir := flag.String("out", "./catalog-publish-out", "output directory for generated artifacts")
	keyID := flag.String("keyID", "local-publisher-key", "signing key id")
	domain := flag.String("domain", "local.test", "publisher domain -- embedded as the manifest's domain and the index's participantId")
	indexSchemaURL := flag.String("indexSchemaURL", defaultIndexSchemaURL, "JSON-Schema URL for the catalog-index document shape")
	nextUpdateDays := flag.Int("nextUpdateDays", 14, "days until the manifest/index \"next_update\" freshness window expires (0 to omit it)")
	fileValidityDays := flag.Int("fileValidityDays", 14, "days until each catalog file's signature.validUntil expires (0 falls back to -nextUpdateDays)")
	retire := flag.String("retire", "", "comma-separated catalogIds to mark RETIRED this run (works with or without -catalog)")
	forceBaseline := flag.Bool("forceBaseline", false, "publish a fresh baseline for -catalog, discarding its change history (also how to trigger compaction)")
	flag.Parse()

	var retireIDs []string
	if *retire != "" {
		retireIDs = strings.Split(*retire, ",")
	}
	if *catalogPath == "" && len(retireIDs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: catalogpublisherctl -catalog <path> [-catalogId id] [-out dir] [-keyID id] [-domain domain] [-indexSchemaURL url] [-nextUpdateDays n] [-fileValidityDays n] [-retire id1,id2] [-forceBaseline]")
		os.Exit(2)
	}

	must(os.MkdirAll(*outDir, 0o755))
	wellKnownDir := filepath.Join(*outDir, ".well-known")
	must(os.MkdirAll(wellKnownDir, 0o755))
	dediDir := filepath.Join(*outDir, "dedi")
	must(os.MkdirAll(dediDir, 0o755))
	catalogsDir := filepath.Join(*outDir, catalogsDirName)
	must(os.MkdirAll(catalogsDir, 0o755))
	catalogBaseURL := "file://" + mustAbs(catalogsDir)

	km, err := newFileKeyManager(*outDir, *keyID)
	must(err)

	var nextUpdateIn time.Duration
	if *nextUpdateDays > 0 {
		nextUpdateIn = time.Duration(*nextUpdateDays) * 24 * time.Hour
	}
	var fileValidityIn time.Duration
	if *fileValidityDays > 0 {
		fileValidityIn = time.Duration(*fileValidityDays) * 24 * time.Hour
	}

	ctx := context.Background()
	publisher, _, err := catalogpublisher.New(ctx, km, &catalogpublisher.Config{
		KeyID:          *keyID,
		Domain:         *domain,
		IndexSchemaURL: *indexSchemaURL,
		NextUpdateIn:   nextUpdateIn,
		FileValidityIn: fileValidityIn,
		IndexURL:       "file://" + mustAbs(filepath.Join(dediDir, catalogIndexFilename)),
		CatalogBaseURL: catalogBaseURL,
	})
	must(err)

	req := definition.PublishRequest{Retire: retireIDs, ForceBaseline: *forceBaseline}

	var id string
	if *catalogPath != "" {
		catalogBytes, err := os.ReadFile(*catalogPath)
		must(err)

		id = *catalogID
		if id == "" {
			var withID struct {
				ID string `json:"id"`
			}
			must(json.Unmarshal(catalogBytes, &withID))
			if withID.ID == "" {
				fatal(`catalog has no top-level "id" and -catalogId was not given`)
			}
			id = *domain + "/" + withID.ID
		} else if !strings.Contains(id, "/") {
			id = *domain + "/" + id
		}

		req.Catalogs = []definition.CatalogSubmission{{CatalogID: id, Catalog: catalogBytes}}
	}

	prior, carryForward, priorIndexVersion, err := loadPriorState(*outDir, id)
	must(err)
	if prior != nil {
		req.PriorState = map[string]definition.PriorCatalogState{id: *prior}
	}
	req.CarryForward = carryForward
	req.PriorIndexVersion = priorIndexVersion

	result, err := publisher.Publish(ctx, req)
	must(err)

	for _, e := range result.Errors {
		fmt.Fprintf(os.Stderr, "publish error [%s/%s]: %s\n", e.CatalogID, e.Stage, e.Reason)
	}

	must(os.WriteFile(filepath.Join(wellKnownDir, dediManifestFilename), result.Manifest, 0o644))
	must(os.WriteFile(filepath.Join(dediDir, catalogIndexFilename), result.Index, 0o644))

	for _, outcome := range result.Catalogs {
		local := localCatalogName(outcome.CatalogID)
		switch outcome.Mode {
		case "baseline":
			path := filepath.Join(catalogsDir, fmt.Sprintf("%s.v%d.json", local, outcome.Version))
			must(os.WriteFile(path, outcome.Content, 0o644))
			fmt.Printf("catalog %s: published baseline, version %d\n", outcome.CatalogID, outcome.Version)
		case "change":
			path := filepath.Join(catalogsDir, fmt.Sprintf("%s.v%d.changes.json", local, outcome.Version))
			must(os.WriteFile(path, outcome.Content, 0o644))
			fmt.Printf("catalog %s: published change file, version %d\n", outcome.CatalogID, outcome.Version)
			printChangeSummary(outcome.Content)
		default:
			fmt.Printf("catalog %s: unchanged, still version %d\n", outcome.CatalogID, outcome.Version)
		}
		fmt.Printf("  digest: %s\n", outcome.Digest)
	}
	for _, rid := range retireIDs {
		fmt.Printf("catalog %s: marked RETIRED\n", rid)
	}

	fmt.Printf("index version %d, artifacts written to %s\n", result.IndexVersion, *outDir)
}

func printChangeSummary(content json.RawMessage) {
	var change struct {
		Resources diffBlock `json:"resources"`
		Offers    diffBlock `json:"offers"`
	}
	if json.Unmarshal(content, &change) != nil {
		return
	}
	fmt.Printf("  resources: %d upserts, %d removals; offers: %d upserts, %d removals\n",
		len(change.Resources.Upserts), len(change.Resources.Removals),
		len(change.Offers.Upserts), len(change.Offers.Removals))
}

// localCatalogName returns catalogID with any "domain/" prefix stripped,
// matching catalogpublisher's own filename convention (catalogId
// "open-economy.nfh.global/electronics-2026" -> "electronics-2026").
func localCatalogName(catalogID string) string {
	if i := strings.LastIndex(catalogID, "/"); i != -1 {
		return catalogID[i+1:]
	}
	return catalogID
}

// --- Prior-state reconstruction ------------------------------------------

// indexDoc/indexEntry/wireFileEntry are the subset of the catalog index's
// shape this tool needs to read back, mirroring catalogpublisher's own
// wire types (duplicated rather than imported: this is a wire-format
// contract crossing the CLI/plugin boundary, not Go code the two should
// share, matching the convention already used for validateSubmission-style
// logic).
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

// loadPriorState reads the previously-written catalog index (if any),
// returning: PriorCatalogState for id (nil if id is new, retired, or
// unpublishable), every other catalog's raw entry to carry forward
// unmodified, and the index's own last-published version.
func loadPriorState(outDir, id string) (*definition.PriorCatalogState, []json.RawMessage, int, error) {
	indexPath := filepath.Join(outDir, "dedi", catalogIndexFilename)
	raw, err := os.ReadFile(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, 0, nil
	} else if err != nil {
		return nil, nil, 0, fmt.Errorf("reading existing index: %w", err)
	}

	var doc indexDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, 0, fmt.Errorf("parsing existing index: %w", err)
	}

	var prior *definition.PriorCatalogState
	var carryForward []json.RawMessage
	for _, rawEntry := range doc.Catalogs {
		var probe struct {
			CatalogID string `json:"catalogId"`
		}
		if json.Unmarshal(rawEntry, &probe) != nil {
			continue
		}
		if id != "" && probe.CatalogID == id {
			var entry indexEntry
			if err := json.Unmarshal(rawEntry, &entry); err != nil {
				return nil, nil, 0, fmt.Errorf("parsing entry for %s: %w", id, err)
			}
			if entry.Status == "RETIRED" || entry.Baseline == nil {
				continue // no publishable prior state; this run starts a fresh baseline
			}
			state, err := reconstructState(outDir, localCatalogName(id), entry)
			if err != nil {
				return nil, nil, 0, err
			}
			prior = state
			continue
		}
		carryForward = append(carryForward, rawEntry)
	}
	return prior, carryForward, doc.Version, nil
}

func reconstructState(outDir, localName string, entry indexEntry) (*definition.PriorCatalogState, error) {
	baselinePath := localFilePath(outDir, localName, entry.Baseline.Version, "json")
	baselineBytes, err := os.ReadFile(baselinePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", baselinePath, err)
	}

	effective := json.RawMessage(baselineBytes)
	changeFiles := make([]definition.FileRef, 0, len(entry.Changes))
	for _, ch := range entry.Changes {
		path := localFilePath(outDir, localName, ch.Version, "changes.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		effective, err = applyChangeFile(effective, raw)
		if err != nil {
			return nil, fmt.Errorf("applying %s: %w", path, err)
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

func localFilePath(outDir, localName string, version int, suffix string) string {
	return filepath.Join(outDir, catalogsDirName, fmt.Sprintf("%s.v%d.%s", localName, version, suffix))
}

// --- Change-file application ----------------------------------------------
//
// catalogDoc is the fixed top-level shape a Beckn Catalog carries (file
// spec: "the plain Beckn catalog JSON, exactly the schema used today").

type catalogDoc struct {
	ID         json.RawMessage   `json:"id"`
	Descriptor json.RawMessage   `json:"descriptor"`
	Provider   json.RawMessage   `json:"provider"`
	Resources  []json.RawMessage `json:"resources"`
	Offers     []json.RawMessage `json:"offers,omitempty"`
}

type diffBlock struct {
	Upserts  []json.RawMessage `json:"upserts,omitempty"`
	Removals []string          `json:"removals,omitempty"`
}

type changeFileDoc struct {
	CatalogID   string          `json:"catalogId"`
	FromVersion int             `json:"fromVersion"`
	ToVersion   int             `json:"toVersion"`
	Resources   diffBlock       `json:"resources"`
	Offers      diffBlock       `json:"offers"`
	Catalog     json.RawMessage `json:"catalog,omitempty"`
}

// applyChangeFile folds one change file onto catalog's resources/offers
// arrays (upserts replace by id or append; removals drop by id) and
// overlays any catalog-level attribute changes.
func applyChangeFile(catalog []byte, changeRaw []byte) ([]byte, error) {
	var doc catalogDoc
	if err := json.Unmarshal(catalog, &doc); err != nil {
		return nil, fmt.Errorf("parsing catalog: %w", err)
	}
	var change changeFileDoc
	if err := json.Unmarshal(changeRaw, &change); err != nil {
		return nil, fmt.Errorf("parsing change file: %w", err)
	}

	resources, err := applyDiffBlock(doc.Resources, change.Resources)
	if err != nil {
		return nil, fmt.Errorf("applying resources: %w", err)
	}
	doc.Resources = resources

	offers, err := applyDiffBlock(doc.Offers, change.Offers)
	if err != nil {
		return nil, fmt.Errorf("applying offers: %w", err)
	}
	doc.Offers = offers

	if len(change.Catalog) > 0 {
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal(change.Catalog, &attrs); err != nil {
			return nil, fmt.Errorf("parsing catalog attribute changes: %w", err)
		}
		if v, ok := attrs["descriptor"]; ok {
			doc.Descriptor = v
		}
		if v, ok := attrs["provider"]; ok {
			doc.Provider = v
		}
	}

	return json.Marshal(doc)
}

// applyDiffBlock applies one diffBlock (upserts by id, replacing existing
// or appending new; removals by id) to items.
func applyDiffBlock(items []json.RawMessage, block diffBlock) ([]json.RawMessage, error) {
	removed := make(map[string]bool, len(block.Removals))
	for _, id := range block.Removals {
		removed[id] = true
	}
	upserts := make(map[string]json.RawMessage, len(block.Upserts))
	for _, u := range block.Upserts {
		id, err := resourceID(u)
		if err != nil {
			return nil, err
		}
		upserts[id] = u
	}

	next := make([]json.RawMessage, 0, len(items)+len(block.Upserts))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		id, err := resourceID(item)
		if err != nil {
			return nil, err
		}
		seen[id] = true
		if removed[id] {
			continue
		}
		if u, ok := upserts[id]; ok {
			next = append(next, u)
			continue
		}
		next = append(next, item)
	}
	for _, u := range block.Upserts {
		id, _ := resourceID(u) // already validated above
		if !seen[id] {
			next = append(next, u)
		}
	}
	return next, nil
}

func resourceID(raw json.RawMessage) (string, error) {
	var withID struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &withID); err != nil {
		return "", fmt.Errorf("parsing resource: %w", err)
	}
	if withID.ID == "" {
		return "", fmt.Errorf("resource missing id")
	}
	return withID.ID, nil
}

// --- Demo-only local key manager ------------------------------------------

// storedKey is the on-disk shape of a locally-generated signing keypair --
// a demo-only substitute for a real KeyManager backend.
type storedKey struct {
	SigningPrivate string `json:"signingPrivate"`
	SigningPublic  string `json:"signingPublic"`
}

// fileKeyManager is a demo-only definition.KeyManager: one keypair, read
// from (and, on first use, generated into) a JSON file under
// outDir/.keys/. Real deployments use a real KeyManager plugin; this
// exists so the CLI needs no external key infrastructure to run.
type fileKeyManager struct {
	path string
}

func newFileKeyManager(outDir, keyID string) (*fileKeyManager, error) {
	dir := filepath.Join(outDir, ".keys")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, keyID+".json")

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			return nil, err
		}
		sk := storedKey{
			SigningPrivate: base64.StdEncoding.EncodeToString(priv.Seed()),
			SigningPublic:  base64.StdEncoding.EncodeToString(pub),
		}
		raw, err := json.MarshalIndent(sk, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	return &fileKeyManager{path: path}, nil
}

func (f *fileKeyManager) GenerateKeyset() (*model.Keyset, error) {
	return nil, fmt.Errorf("fileKeyManager: not supported")
}
func (f *fileKeyManager) InsertKeyset(ctx context.Context, keyID string, keyset *model.Keyset) error {
	return fmt.Errorf("fileKeyManager: not supported")
}
func (f *fileKeyManager) Keyset(ctx context.Context, keyID string) (*model.Keyset, error) {
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	var sk storedKey
	if err := json.Unmarshal(raw, &sk); err != nil {
		return nil, err
	}
	return &model.Keyset{SigningPrivate: sk.SigningPrivate, SigningPublic: sk.SigningPublic}, nil
}
func (f *fileKeyManager) LookupNPKeys(ctx context.Context, subscriberID, uniqueKeyID string) (string, string, error) {
	return "", "", fmt.Errorf("fileKeyManager: not supported")
}
func (f *fileKeyManager) DeleteKeyset(ctx context.Context, keyID string) error {
	return os.Remove(f.path)
}

func mustAbs(path string) string {
	abs, err := filepath.Abs(path)
	must(err)
	return abs
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}
