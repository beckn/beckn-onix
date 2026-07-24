// Command catalogpublisherctl is a minimal, throwaway-for-demo CLI around
// catalogpublisher.Publisher: point it at a catalog JSON file and an output
// directory, and it writes a signed manifest, signed index, and the
// catalog's baseline/change files to that directory. Running it again with
// an updated catalog (same catalogId) against the same output directory
// diffs against what's on disk and produces a change file with a bumped
// version instead of a fresh baseline.
//
// This exists to demonstrate and exercise catalogpublisher.Publish
// end-to-end without needing ONIX's HTTP server, PluginManager, or a
// handler -- Publish is a plain in-process call, and this file is one of
// several equally valid ways to invoke it (a CLI here, an HTTP handler, a
// future desktop app all wire the same call differently).
//
// catalogpublisherctl owns all persistence itself (signing key, prior
// state reconstruction) -- Publish itself holds no storage-backed state,
// per definition.PriorCatalogState's doc comment.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher"
)

func main() {
	catalogPath := flag.String("catalog", "", "path to a Beckn Catalog JSON file (required)")
	catalogID := flag.String("catalogId", "", `catalog id (defaults to the catalog's own top-level "id" field)`)
	outDir := flag.String("out", "./catalog-publish-out", "output directory for generated artifacts")
	keyID := flag.String("keyID", "local-publisher-key", "signing key id")
	domain := flag.String("domain", "local.test", "publisher domain embedded in the manifest/index")
	forceBaseline := flag.Bool("forceBaseline", false, "always publish a fresh baseline, ignoring any prior state on disk")
	flag.Parse()

	if *catalogPath == "" {
		fmt.Fprintln(os.Stderr, "usage: catalogpublisherctl -catalog <path> [-catalogId id] [-out dir] [-keyID id] [-domain domain] [-forceBaseline]")
		os.Exit(2)
	}

	catalogBytes, err := os.ReadFile(*catalogPath)
	must(err)

	id := *catalogID
	if id == "" {
		var withID struct {
			ID string `json:"id"`
		}
		must(json.Unmarshal(catalogBytes, &withID))
		id = withID.ID
	}
	if id == "" {
		fatal(`catalog has no top-level "id" and -catalogId was not given`)
	}

	must(os.MkdirAll(*outDir, 0o755))
	catalogBaseURL := "file://" + mustAbs(filepath.Join(*outDir, "catalog"))

	km, err := newFileKeyManager(*outDir, *keyID)
	must(err)

	ctx := context.Background()
	publisher, _, err := catalogpublisher.New(ctx, km, &catalogpublisher.Config{
		KeyID:          *keyID,
		Domain:         *domain,
		IndexURL:       "file://" + mustAbs(filepath.Join(*outDir, "index.json")),
		CatalogBaseURL: catalogBaseURL,
	})
	must(err)

	prior, err := loadPriorState(*outDir, catalogBaseURL, id)
	must(err)

	req := definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{
			CatalogID:  id,
			Visibility: definition.PublishVisibility{Public: true},
			Catalog:    catalogBytes,
		}},
		ForceBaseline: *forceBaseline,
	}
	if prior != nil {
		req.PriorState = map[string]definition.PriorCatalogState{id: *prior}
	}

	result, err := publisher.Publish(ctx, req)
	must(err)

	for _, e := range result.Errors {
		fmt.Fprintf(os.Stderr, "publish error [%s/%s]: %s\n", e.CatalogID, e.Stage, e.Reason)
	}

	must(os.WriteFile(filepath.Join(*outDir, "manifest.json"), result.Manifest, 0o644))
	must(os.WriteFile(filepath.Join(*outDir, "index.json"), result.Index, 0o644))

	for _, outcome := range result.Catalogs {
		dir := filepath.Join(*outDir, "catalog", outcome.CatalogID)
		must(os.MkdirAll(dir, 0o755))

		switch outcome.Mode {
		case "baseline":
			must(os.WriteFile(filepath.Join(dir, "baseline.json"), outcome.Content, 0o644))
			fmt.Printf("catalog %s: published baseline, version %d\n", outcome.CatalogID, outcome.Version)
		case "change":
			must(os.WriteFile(filepath.Join(dir, fmt.Sprintf("change-%d.json", outcome.Version)), outcome.Content, 0o644))
			fmt.Printf("catalog %s: published change file, version %d\n", outcome.CatalogID, outcome.Version)
			printChangeSummary(outcome.Content)
		default:
			fmt.Printf("catalog %s: unchanged, still version %d\n", outcome.CatalogID, outcome.Version)
		}
		fmt.Printf("  digest: %s\n", outcome.Digest)
	}

	fmt.Printf("artifacts written to %s\n", *outDir)
}

func printChangeSummary(content json.RawMessage) {
	var change struct {
		Added   []json.RawMessage `json:"added"`
		Updated []json.RawMessage `json:"updated"`
		Removed []string          `json:"removed"`
	}
	if json.Unmarshal(content, &change) != nil {
		return
	}
	fmt.Printf("  +%d added, ~%d updated, -%d removed\n", len(change.Added), len(change.Updated), len(change.Removed))
}

// loadPriorState reconstructs definition.PriorCatalogState for catalogID
// from whatever this tool previously wrote to outDir/catalog/<catalogID>/:
// a baseline.json and zero or more change-<version>.json files, applied in
// version order to rebuild the full "currently effective" catalog content
// Publish needs to diff against. Returns nil (not an error) when no
// baseline exists yet -- this catalog has never been published.
func loadPriorState(outDir, catalogBaseURL, catalogID string) (*definition.PriorCatalogState, error) {
	dir := filepath.Join(outDir, "catalog", catalogID)
	baselineBytes, err := os.ReadFile(filepath.Join(dir, "baseline.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("reading baseline: %w", err)
	}

	changeFiles, err := discoverChangeFiles(dir)
	if err != nil {
		return nil, err
	}

	effective := json.RawMessage(baselineBytes)
	version := 1
	changeParts := make([]definition.PartRef, 0, len(changeFiles))
	for _, cf := range changeFiles {
		raw, err := os.ReadFile(cf.path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", cf.path, err)
		}
		effective, err = applyChangeFile(effective, raw)
		if err != nil {
			return nil, fmt.Errorf("applying %s: %w", cf.path, err)
		}
		version = cf.version
		changeParts = append(changeParts, definition.PartRef{
			URL:    catalogPartURL(catalogBaseURL, catalogID, filepath.Base(cf.path)),
			Digest: "sha-256:" + hexSHA256(raw),
		})
	}

	return &definition.PriorCatalogState{
		Version: version,
		Catalog: effective,
		BaselinePart: &definition.PartRef{
			URL:    catalogPartURL(catalogBaseURL, catalogID, "baseline.json"),
			Digest: "sha-256:" + hexSHA256(baselineBytes),
		},
		ChangeParts: changeParts,
	}, nil
}

type changeFileRef struct {
	version int
	path    string
}

var changeFileRE = regexp.MustCompile(`^change-(\d+)\.json$`)

func discoverChangeFiles(dir string) ([]changeFileRef, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []changeFileRef
	for _, e := range entries {
		m := changeFileRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		out = append(out, changeFileRef{version: v, path: filepath.Join(dir, e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// catalogDoc is the fixed top-level shape a Beckn Catalog carries (design
// doc: "the bare {id, descriptor, provider, resources} shape") -- a change
// file only ever touches Resources.
type catalogDoc struct {
	ID         json.RawMessage   `json:"id"`
	Descriptor json.RawMessage   `json:"descriptor"`
	Provider   json.RawMessage   `json:"provider"`
	Resources  []json.RawMessage `json:"resources"`
}

type changeFileDoc struct {
	Version int               `json:"version"`
	Added   []json.RawMessage `json:"added,omitempty"`
	Updated []json.RawMessage `json:"updated,omitempty"`
	Removed []string          `json:"removed,omitempty"`
}

// applyChangeFile folds one change file onto catalog's resources array:
// removed ids drop out, updated ids are replaced in place, added items are
// appended.
func applyChangeFile(catalog []byte, changeRaw []byte) ([]byte, error) {
	var doc catalogDoc
	if err := json.Unmarshal(catalog, &doc); err != nil {
		return nil, fmt.Errorf("parsing catalog: %w", err)
	}
	var change changeFileDoc
	if err := json.Unmarshal(changeRaw, &change); err != nil {
		return nil, fmt.Errorf("parsing change file: %w", err)
	}

	removed := make(map[string]bool, len(change.Removed))
	for _, id := range change.Removed {
		removed[id] = true
	}
	updated := make(map[string]json.RawMessage, len(change.Updated))
	for _, r := range change.Updated {
		id, err := resourceID(r)
		if err != nil {
			return nil, err
		}
		updated[id] = r
	}

	next := make([]json.RawMessage, 0, len(doc.Resources)+len(change.Added))
	for _, r := range doc.Resources {
		id, err := resourceID(r)
		if err != nil {
			return nil, err
		}
		if removed[id] {
			continue
		}
		if u, ok := updated[id]; ok {
			next = append(next, u)
			continue
		}
		next = append(next, r)
	}
	next = append(next, change.Added...)
	doc.Resources = next

	return json.Marshal(doc)
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

func catalogPartURL(base, catalogID, filename string) string {
	return strings.TrimRight(base, "/") + "/" + catalogID + "/" + filename
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

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
