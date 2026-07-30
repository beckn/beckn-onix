// Command gen regenerates the crawler verification fixture in
// install/crawler-fixture: the publisher keypair, the registry subscription
// record that key has to be registered as, and catalog-index.json with real
// sha-256 digests, real sizes, and real Ed25519 tuple signatures over the
// catalog files as they sit on disk.
//
// It exists so the fixture is reproducible instead of a pile of magic constants.
// Edit a catalog file under publisher/catalogs, re-run this, and the index is
// correct again:
//
//	go run ./install/crawler-fixture/gen
//
// Everything it writes is derived, never invented:
//
//   - digest and size come from hashing the file bytes on disk;
//   - each signature is produced by artifactsigner.SignFileTuple over
//     {catalogId, version, url, digest, validUntil};
//   - every signature is then re-checked with artifactverifier.VerifyFileTuple
//     before anything is written, so the command cannot emit an index the
//     crawler would reject.
//
// The keypair is derived deterministically from a fixed seed label
// (seed = sha256(seedLabel)), so re-running produces byte-identical output and
// the committed signatures stay valid. That is a real Ed25519 keypair; it is
// simply not a random one, because a random one would invalidate the committed
// index on every run. It is a throwaway fixture key with no authority anywhere.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/security/artifactsigner"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// The fixture publisher's identity and signing-key parameters.
//
// participantID doubles as the registry SUBSCRIBER ID: the crawler resolves a
// file's signing key by looking up {index participantId, entry keyId} in the
// network registry. keyID is what the index entries name and what the
// registered key has to be filed under, because that pair is the whole lookup.
//
// keyID is deliberately a plain token with no '#' and no path separators. Some
// registry clients interpolate the key id straight into a lookup URL path
// (pkg/plugin/implementation/dediregistry builds
// "{url}/lookup/{subscriber}/{registry}/{keyID}" with no escaping), so a '#'
// would be parsed as a URL fragment and silently truncate the lookup. The key
// id is not part of the signed tuple, so this is free to choose.
const (
	participantID = "sunrise-ev.example.org"
	keyID         = "fixture-key-1"
	seedLabel     = "beckn-onix crawler local verification fixture v1"

	// originBase is the in-network address nginx serves the fixture on. The
	// signed tuple covers the URL, so this must match what the crawler fetches.
	originBase = "http://publisher-origin/catalogs/"

	// validUntilRFC3339 is the signature expiry. It is signed as part of the
	// tuple and must stay byte-identical to the "validUntil" string in the index.
	validUntilRFC3339 = "2030-01-01T00:00:00Z"

	// indexVersion is the index's own version. The crawler's change detection
	// compares it against the stored one, so bump it if you want a running
	// crawler to notice an edit.
	indexVersion = 2

	catalogID = "cat-ev-001"
)

// keyWarning is written into the private-key file. The key is committed on
// purpose, and this states plainly what it is so nobody mistakes it for a
// credential.
var keyWarning = []string{
	"TEST KEY. NOT A CREDENTIAL. This is a throwaway Ed25519 keypair that exists",
	"only so install/crawler-fixture carries real, verifiable signatures instead",
	"of placeholders. It grants nothing, protects nothing, and is trusted by",
	"nothing outside this fixture.",
	"",
	"It is committed deliberately and it is derived deterministically from",
	"seedLabel (seed = sha256(seedLabel)), so anyone can regenerate it. Never",
	"reuse it, and never treat a report of it leaking as a security incident.",
	"",
	"Regenerate everything with: go run ./install/crawler-fixture/gen",
}

// fileSpec is one catalog file the index lists: where it lives on disk, the
// version it carries, and the URL it is published at. Digest, size and
// signature are computed, never written by hand.
type fileSpec struct {
	relPath string // relative to the publisher root
	version int
}

func (f fileSpec) url() string { return originBase + filepath.Base(f.relPath) }

// --- the JSON shapes written out --------------------------------------------
// Declared as structs (not maps) so field order in the emitted files is stable
// and the output stays readable in a diff.

type signature struct {
	KeyID      string `json:"keyId"`
	Value      string `json:"value"`
	ValidUntil string `json:"validUntil"`
}

type fileEntry struct {
	Version   int       `json:"version"`
	URL       string    `json:"url"`
	Size      int64     `json:"size"`
	Digest    string    `json:"digest"`
	Encoding  string    `json:"encoding"`
	Signature signature `json:"signature"`
}

type catalogEntry struct {
	CatalogID   string      `json:"catalogId"`
	CatalogType string      `json:"catalogType"`
	Status      string      `json:"status"`
	SchemaTypes []string    `json:"schemaTypes"`
	NetworkIDs  []string    `json:"networkIds"`
	Baseline    fileEntry   `json:"baseline"`
	Changes     []fileEntry `json:"changes"`
}

type indexDoc struct {
	Comment       []string       `json:"_comment"`
	ParticipantID string         `json:"participantId"`
	Version       int64          `json:"version"`
	NextUpdate    string         `json:"next_update"`
	Catalogs      []catalogEntry `json:"catalogs"`
}

// registrySubscription is the record that has to exist in the network registry
// before this fixture can be crawled. The crawler has no key configuration: it
// resolves the verifying key by calling the registry plugin's Lookup with
// {subscriber_id, key_id} and using the signing_public_key that comes back.
//
// The field names are model.Subscription's JSON tags, so this file lines up
// with what a registry lookup is expected to return. signing_public_key is
// STANDARD base64 (with padding) of the raw 32-byte Ed25519 key, because that
// is what fetch/verify.go decodes it as.
//
// status is SUBSCRIBED. model.IsKeyStatusUsable rejects EXPIRED, UNSUBSCRIBED
// and INVALID_SSL, and a rejected status verifies nothing.
type registrySubscription struct {
	Comment          []string `json:"_comment"`
	SubscriberID     string   `json:"subscriber_id"`
	KeyID            string   `json:"key_id"`
	SigningPublicKey string   `json:"signing_public_key"`
	Status           string   `json:"status"`
}

type keyFile struct {
	Comment      []string `json:"_comment"`
	SubscriberID string   `json:"subscriberId"`
	KeyID        string   `json:"keyId"`
	Algorithm    string   `json:"algorithm"`
	SeedLabel    string   `json:"seedLabel"`
	PublicKey    string   `json:"publicKey"`  // base64 standard, raw 32 bytes
	PrivateKey   string   `json:"privateKey"` // base64 standard, raw 64 bytes (seed||public)
}

func main() {
	dir := flag.String("dir", "install/crawler-fixture", "fixture directory to regenerate, relative to the working directory")
	flag.Parse()

	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	priv := ed25519.NewKeyFromSeed(seedFrom(seedLabel))
	pub := priv.Public().(ed25519.PublicKey)

	validUntil, err := time.Parse(time.RFC3339, validUntilRFC3339)
	if err != nil {
		return fmt.Errorf("parsing validUntil: %w", err)
	}

	baseline, err := buildEntry(dir, fileSpec{relPath: "catalogs/cat-ev-001-v1.json", version: 1}, validUntil, priv, pub)
	if err != nil {
		return err
	}
	change, err := buildEntry(dir, fileSpec{relPath: "catalogs/cat-ev-001-v1-to-v2.json", version: 2}, validUntil, priv, pub)
	if err != nil {
		return err
	}

	idx := indexDoc{
		Comment: []string{
			"GENERATED FILE. Do not hand-edit digests, sizes or signatures.",
			"Regenerate with: go run ./install/crawler-fixture/gen",
		},
		ParticipantID: participantID,
		Version:       indexVersion,
		NextUpdate:    validUntilRFC3339,
		Catalogs: []catalogEntry{{
			CatalogID:   catalogID,
			CatalogType: "regular",
			Status:      "ACTIVE",
			SchemaTypes: []string{"beckn:Catalog"},
			NetworkIDs:  []string{}, // empty => public, taken by any crawler
			Baseline:    baseline,
			Changes:     []fileEntry{change},
		}},
	}

	pubB64 := base64.StdEncoding.EncodeToString(pub)

	sub := registrySubscription{
		Comment: []string{
			"GENERATED FILE. This is the record the fixture publisher's signing key",
			"must exist as in whatever network registry the crawler is pointed at.",
			"There is no CRAWLER_* variable for keys: the crawler looks them up by",
			"{subscriber_id, key_id} and there is no fallback.",
			"Registering it is a manual step. This fixture does not automate it.",
		},
		SubscriberID:     participantID,
		KeyID:            keyID,
		SigningPublicKey: pubB64,
		Status:           "SUBSCRIBED",
	}

	writes := []struct {
		path string
		doc  any
	}{
		{filepath.Join(dir, "publisher", "catalog-index.json"), idx},
		{filepath.Join(dir, "registry-subscription.json"), sub},
		{filepath.Join(dir, "fixture-signing-key.json"), keyFile{
			Comment:      keyWarning,
			SubscriberID: participantID,
			KeyID:        keyID,
			Algorithm:    "Ed25519",
			SeedLabel:    seedLabel,
			PublicKey:    pubB64,
			PrivateKey:   base64.StdEncoding.EncodeToString(priv),
		}},
	}
	for _, w := range writes {
		if err := writeJSON(w.path, w.doc); err != nil {
			return err
		}
		fmt.Println("wrote", w.path)
	}

	fmt.Println()
	fmt.Println("Register this in the registry before crawling (see install/crawler-fixture/README.md):")
	fmt.Printf("  subscriber_id      %s\n", participantID)
	fmt.Printf("  key_id             %s\n", keyID)
	fmt.Printf("  signing_public_key %s\n", pubB64)
	fmt.Println()
	fmt.Printf("digest %s  %s\n", baseline.Digest, baseline.URL)
	fmt.Printf("digest %s  %s\n", change.Digest, change.URL)
	return nil
}

// seedFrom derives the 32-byte Ed25519 seed from a label, so the fixture keypair
// is reproducible from a string anyone can read in this file.
func seedFrom(label string) []byte {
	sum := sha256.Sum256([]byte(label))
	return sum[:]
}

// buildEntry hashes the file on disk, signs the resulting tuple, and verifies
// that signature before returning. A signature that does not verify here is a
// bug in this generator, so it stops rather than writing an index the crawler
// would park on.
func buildEntry(dir string, f fileSpec, validUntil time.Time, priv ed25519.PrivateKey, pub ed25519.PublicKey) (fileEntry, error) {
	path := filepath.Join(dir, "publisher", f.relPath)
	body, err := os.ReadFile(path)
	if err != nil {
		return fileEntry{}, fmt.Errorf("reading %s: %w", path, err)
	}
	sum := sha256.Sum256(body)
	digest := "sha-256:" + fmt.Sprintf("%x", sum[:])

	// The digest goes into the tuple exactly as published, prefix included: the
	// verifier signs over the declared string, not over the parsed hash.
	value, err := artifactsigner.SignFileTuple(catalogID, f.version, f.url(), digest, validUntil, priv)
	if err != nil {
		return fileEntry{}, fmt.Errorf("signing %s: %w", f.relPath, err)
	}
	if err := artifactverifier.VerifyFileTuple(catalogID, f.version, f.url(), digest, validUntil, value, pub); err != nil {
		return fileEntry{}, fmt.Errorf("self-check failed for %s: %w", f.relPath, err)
	}

	return fileEntry{
		Version:  f.version,
		URL:      f.url(),
		Size:     int64(len(body)),
		Digest:   digest,
		Encoding: "json",
		Signature: signature{
			KeyID:      keyID,
			Value:      value,
			ValidUntil: validUntil.UTC().Format(time.RFC3339),
		},
	}, nil
}

// writeJSON writes doc indented, with a trailing newline, so the emitted files
// are readable and diff cleanly.
func writeJSON(path string, doc any) error {
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
