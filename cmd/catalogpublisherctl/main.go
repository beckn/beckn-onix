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

	"github.com/beckn-one/beckn-onix/pkg/catalogfile"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher/localstore"
)

func main() {
	catalogPath := flag.String("catalog", "", "path to a Beckn Catalog JSON file")
	catalogID := flag.String("catalogId", "", `catalog id; a bare name (no "/") is prefixed with -domain, matching the file spec's "domain/localName" convention. Defaults to "{domain}/{the catalog's own top-level id}"`)
	outDir := flag.String("out", "./catalog-publish-out", "output directory for generated artifacts")
	keyID := flag.String("keyID", "local-publisher-key", "signing key id -- embedded in the keyset this CLI's file-backed KeyManager returns")
	domain := flag.String("domain", "local.test", "publisher domain -- embedded in the keyset this CLI's file-backed KeyManager returns; catalogpublisher reads it from there, not from its own config")
	nextUpdateDays := flag.Int("nextUpdateDays", 14, "days until the manifest/index \"next_update\" freshness window expires (0 to omit it)")
	fileValidityDays := flag.Int("fileValidityDays", 14, "days until each catalog file's signature.validUntil expires (0 falls back to -nextUpdateDays)")
	retire := flag.String("retire", "", "comma-separated catalogIds to mark RETIRED this run (works with or without -catalog)")
	forceBaseline := flag.Bool("forceBaseline", false, "publish a fresh baseline for -catalog, discarding its change history (also how to trigger compaction)")
	publicBaseURL := flag.String("publicBaseURL", "", "if set, embed URLs under this base instead of file:// (e.g. http://localhost:8000 when serving -out with `python3 -m http.server` from within it) -- must match wherever -out is actually served from; the manifest itself is still always written under .well-known/ relative to -out, matching the fixed well-known path a crawler expects")
	flag.Parse()

	var retireIDs []string
	if *retire != "" {
		retireIDs = strings.Split(*retire, ",")
	}
	if *catalogPath == "" && len(retireIDs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: catalogpublisherctl -catalog <path> [-catalogId id] [-out dir] [-keyID id] [-domain domain] [-nextUpdateDays n] [-fileValidityDays n] [-retire id1,id2] [-forceBaseline] [-publicBaseURL url]")
		os.Exit(2)
	}

	must(localstore.EnsureDirs(*outDir))

	indexURL := "file://" + mustAbs(localstore.IndexPath(*outDir))
	catalogBaseURL := "file://" + mustAbs(localstore.CatalogsDir(*outDir))
	if *publicBaseURL != "" {
		base := strings.TrimRight(*publicBaseURL, "/")
		indexURL = base + "/dedi/" + localstore.IndexFilename
		catalogBaseURL = base + "/" + localstore.CatalogsDirName
	}

	km, err := newFileKeyManager(*outDir, *keyID, *domain)
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
		SubscriberID:   *keyID,
		NextUpdateIn:   nextUpdateIn,
		FileValidityIn: fileValidityIn,
		IndexURL:       indexURL,
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

	var loadIDs []string
	if id != "" {
		loadIDs = []string{id}
	}
	state, err := localstore.Load(*outDir, loadIDs)
	must(err)
	req.PriorState = state.PriorState
	req.CarryForward = state.CarryForward
	req.PriorIndexVersion = state.PriorIndexVersion

	result, err := publisher.Publish(ctx, req)
	must(err)

	for _, e := range result.Errors {
		fmt.Fprintf(os.Stderr, "publish error [%s/%s]: %s\n", e.CatalogID, e.Stage, e.Reason)
	}

	must(localstore.Write(*outDir, result))

	for _, outcome := range result.Catalogs {
		switch outcome.Mode {
		case "baseline":
			fmt.Printf("catalog %s: published baseline, version %d\n", outcome.CatalogID, outcome.Version)
		case "change":
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
		Resources catalogfile.DiffBlock `json:"resources"`
		Offers    catalogfile.DiffBlock `json:"offers"`
	}
	if json.Unmarshal(content, &change) != nil {
		return
	}
	fmt.Printf("  resources: %d upserts, %d removals; offers: %d upserts, %d removals\n",
		len(change.Resources.Upserts), len(change.Resources.Removals),
		len(change.Offers.Upserts), len(change.Offers.Removals))
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
// fileKeyManager returns keyID/domain as the returned Keyset's
// UniqueKeyID/SubscriberID -- catalogpublisher derives its JWK kid and
// manifest domain from there now, not from its own config.
type fileKeyManager struct {
	path   string
	keyID  string
	domain string
}

func newFileKeyManager(outDir, keyID, domain string) (*fileKeyManager, error) {
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

	return &fileKeyManager{path: path, keyID: keyID, domain: domain}, nil
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
	return &model.Keyset{
		SubscriberID:   f.domain,
		UniqueKeyID:    f.keyID,
		SigningPrivate: sk.SigningPrivate,
		SigningPublic:  sk.SigningPublic,
	}, nil
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
