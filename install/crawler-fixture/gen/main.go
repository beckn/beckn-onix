// Command gen regenerates the crawler verification fixture in
// install/crawler-fixture: the publisher keypair, the registry subscription
// record that key has to be registered as, and the catalog index plus its
// baseline and change file, each self-signed per the Decentralized Catalog
// v2 file spec ("each catalog entry signs itself"; "catalog files and change
// files ... self-sign their own content").
//
// It exists so the fixture is reproducible instead of a pile of magic
// constants. Edit a catalog body below, re-run this, and everything (digests,
// sizes, signatures) is correct again:
//
//	go run ./install/crawler-fixture/gen
//
// Everything it writes is derived, never invented:
//
//   - each catalog FILE (baseline, change file) is built here, then signed
//     in place with artifactsigner.SignJSON over its own JCS canonicalization
//     with "signature" removed, and written to disk WITH that signature
//     embedded — the crawler verifies this same embedded signature, not a
//     tuple carried elsewhere;
//   - the catalog INDEX ENTRY is signed the same way, over
//     {catalogId, catalogType, status, networkIds, schemaTypes, baseline,
//     changes} together;
//   - digest and size are then computed by hashing the SIGNED file bytes as
//     they will actually be served, so the index always describes exactly
//     what is on disk;
//   - every signature is re-checked with artifactverifier.VerifyJSON before
//     anything is written, so the command cannot emit an index the crawler
//     would reject.
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

	"github.com/beckn-one/beckn-onix/pkg/security/artifactsigner"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// The fixture publisher's identity and signing-key parameters.
//
// nodeID doubles as the registry SUBSCRIBER ID: the crawler resolves a
// signature's key by looking up {index nodeId, signature keyId} in the
// network registry. keyID is what the index/files name and what the
// registered key has to be filed under, because that pair is the whole
// lookup.
//
// keyID is deliberately a plain token with no '#' and no path separators. Some
// registry clients interpolate the key id straight into a lookup URL path
// (pkg/plugin/implementation/dediregistry builds
// "{url}/lookup/{subscriber}/{registry}/{keyID}" with no escaping), so a '#'
// would be parsed as a URL fragment and silently truncate the lookup.
const (
	nodeID    = "sunrise-ev.example.org"
	keyID     = "fixture-key-1"
	seedLabel = "beckn-onix crawler local verification fixture v1"

	// originBase is the in-network address nginx serves the fixture on. The
	// index entries carry these URLs, and the crawler fetches exactly them.
	originBase = "http://publisher-origin/catalogs/"

	// nextUpdateRFC3339 bounds how long any copy of the index may be believed
	// (file spec: "a crawler past it re-fetches before relying").
	nextUpdateRFC3339 = "2030-01-01T00:00:00Z"

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

// --- the JSON shapes written out --------------------------------------------
// Declared as structs (not maps) so field order in the emitted files is stable
// and the output stays readable in a diff.

type signature struct {
	KeyID string `json:"keyId"`
	Value string `json:"value"`
}

type descriptor struct {
	Name      string `json:"name"`
	ShortDesc string `json:"shortDesc,omitempty"`
}

type provider struct {
	ID         string     `json:"id"`
	Descriptor descriptor `json:"descriptor"`
}

type resource struct {
	ID          string     `json:"id"`
	Descriptor  descriptor `json:"descriptor"`
	CategoryIDs []string   `json:"categoryIds,omitempty"`
}

type offer struct {
	ID         string     `json:"id"`
	Descriptor descriptor `json:"descriptor"`
}

// catalogDoc is the bare Beckn catalog document (file spec's `Catalog`
// schema, unchanged) -- what a baseline's `.catalog` envelope field wraps.
type catalogDoc struct {
	ID         string     `json:"id"`
	Descriptor descriptor `json:"descriptor"`
	Provider   provider   `json:"provider"`
	Resources  []resource `json:"resources"`
	Offers     []offer    `json:"offers"`
}

// baselineFile is a self-signed `CatalogFile` (file spec): the catalog
// wrapped with its own signature, nothing else.
type baselineFile struct {
	Catalog   catalogDoc `json:"catalog"`
	Signature signature  `json:"signature"`
}

// catalogAttrs is the change file's optional catalog-level attribute patch
// (name/validity changes) -- a best-effort subset (descriptor, provider), per
// pkg/catalogfile.Apply.
type catalogAttrs struct {
	ID         string     `json:"id"`
	Descriptor descriptor `json:"descriptor"`
	Provider   provider   `json:"provider"`
}

type diffBlock struct {
	Upserts  []resource `json:"upserts,omitempty"`
	Removals []string   `json:"removals,omitempty"`
}

type offerDiffBlock struct {
	Upserts  []offer  `json:"upserts,omitempty"`
	Removals []string `json:"removals,omitempty"`
}

// changeFile is a self-signed `CatalogChangeFile` (file spec): flat, not
// enveloped -- signature is a sibling of its own top-level fields.
type changeFile struct {
	CatalogID   string         `json:"catalogId"`
	FromVersion int            `json:"fromVersion"`
	ToVersion   int            `json:"toVersion"`
	Catalog     catalogAttrs   `json:"catalog"`
	Resources   diffBlock      `json:"resources"`
	Offers      offerDiffBlock `json:"offers"`
	Signature   signature      `json:"signature"`
}

// fileEntry is one baseline/change reference in the index: no signature of
// its own -- the file it points at self-signs (see baselineFile/changeFile
// above), and the enclosing catalogEntry self-signs the reference together
// with everything else.
type fileEntry struct {
	Version  int    `json:"version"`
	URL      string `json:"url"`
	Size     int64  `json:"size"`
	Digest   string `json:"digest"`
	Encoding string `json:"encoding"`
}

// catalogEntry is one catalog's index record: self-signed as a whole (RFC
// NFH-014: "each catalog entry signs itself"). isActive (a pointer, per
// NFH-014: nil is meaningfully different from an explicit false) mirrors the
// catalog's own isActive; there is no separate status/ACTIVE-RETIRED field.
type catalogEntry struct {
	CatalogID    string      `json:"catalogId"`
	EntryVersion int64       `json:"entryVersion"`
	CatalogType  string      `json:"catalogType"`
	SchemaTypes  []string    `json:"schemaTypes"`
	NetworkIDs   []string    `json:"networkIds"`
	IsActive     *bool       `json:"isActive,omitempty"`
	Baseline     fileEntry   `json:"baseline"`
	Changes      []fileEntry `json:"changes"`
	Signature    signature   `json:"signature"`
}

// indexDoc carries no top-level version (RFC NFH-014 §Versioning: "there is
// no whole-index version field") -- whether the index changed at all is
// answered by conditional HTTP, not a document-level counter.
type indexDoc struct {
	Comment    []string       `json:"_comment"`
	NodeID     string         `json:"nodeId"`
	NextUpdate string         `json:"next_update"`
	Catalogs   []catalogEntry `json:"catalogs"`
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

	baselineURL := originBase + "cat-ev-001-v1.json"
	changeURL := originBase + "cat-ev-001-v1-to-v2.json"

	baseline := &baselineFile{
		Catalog: catalogDoc{
			ID:         catalogID,
			Descriptor: descriptor{Name: "Sunrise EV Charging", ShortDesc: "Public DC and AC charging points"},
			Provider:   provider{ID: "prov-sunrise", Descriptor: descriptor{Name: "Sunrise Mobility"}},
			Resources: []resource{
				{ID: "res-chg-001", Descriptor: descriptor{Name: "Sunrise Charger MG Road", ShortDesc: "60 kW CCS2 DC fast charger"}, CategoryIDs: []string{"ev-charging"}},
				{ID: "res-chg-002", Descriptor: descriptor{Name: "Sunrise Charger Indiranagar", ShortDesc: "22 kW Type 2 AC charger"}, CategoryIDs: []string{"ev-charging"}},
			},
			Offers: []offer{{ID: "off-launch-01", Descriptor: descriptor{Name: "Launch offer: 10 percent off every session"}}},
		},
	}
	baselineBytes, err := signInPlace(baseline, &baseline.Signature, priv)
	if err != nil {
		return fmt.Errorf("signing baseline: %w", err)
	}
	if err := artifactverifier.VerifyJSON(baselineBytes, "signature", baseline.Signature.Value, pub); err != nil {
		return fmt.Errorf("self-check failed for baseline: %w", err)
	}
	// The trailing newline is part of what gets served and fetched, so it must
	// be part of what is hashed below -- append it here, once, before both the
	// digest and the on-disk write use this exact slice.
	baselineBytes = append(baselineBytes, '\n')

	change := &changeFile{
		CatalogID:   catalogID,
		FromVersion: 1,
		ToVersion:   2,
		Catalog: catalogAttrs{
			ID:         catalogID,
			Descriptor: descriptor{Name: "Sunrise EV Charging", ShortDesc: "Public DC and AC charging points across Bengaluru"},
			Provider:   provider{ID: "prov-sunrise", Descriptor: descriptor{Name: "Sunrise Mobility"}},
		},
		Resources: diffBlock{
			Upserts: []resource{
				{ID: "res-chg-002", Descriptor: descriptor{Name: "Sunrise Charger Indiranagar", ShortDesc: "22 kW Type 2 AC charger, now open 24x7"}, CategoryIDs: []string{"ev-charging"}},
				{ID: "res-chg-003", Descriptor: descriptor{Name: "Sunrise Charger Whitefield", ShortDesc: "120 kW CCS2 DC ultra fast charger"}, CategoryIDs: []string{"ev-charging"}},
			},
		},
	}
	changeBytes, err := signInPlace(change, &change.Signature, priv)
	if err != nil {
		return fmt.Errorf("signing change file: %w", err)
	}
	if err := artifactverifier.VerifyJSON(changeBytes, "signature", change.Signature.Value, pub); err != nil {
		return fmt.Errorf("self-check failed for change file: %w", err)
	}
	changeBytes = append(changeBytes, '\n')

	active := true
	entry := &catalogEntry{
		CatalogID:    catalogID,
		EntryVersion: 1,
		CatalogType:  "regular",
		SchemaTypes:  []string{"beckn:Catalog"},
		NetworkIDs:   []string{}, // empty => public, taken by any crawler
		IsActive:     &active,
		Baseline:     fileEntryFor(baselineURL, 1, baselineBytes),
		Changes:      []fileEntry{fileEntryFor(changeURL, 2, changeBytes)},
	}
	entryBytes, err := signInPlace(entry, &entry.Signature, priv)
	if err != nil {
		return fmt.Errorf("signing catalog entry: %w", err)
	}
	if err := artifactverifier.VerifyJSON(entryBytes, "signature", entry.Signature.Value, pub); err != nil {
		return fmt.Errorf("self-check failed for catalog entry: %w", err)
	}

	idx := indexDoc{
		Comment: []string{
			"GENERATED FILE. Do not hand-edit digests, sizes or signatures.",
			"Regenerate with: go run ./install/crawler-fixture/gen",
		},
		NodeID:     nodeID,
		NextUpdate: nextUpdateRFC3339,
		Catalogs:   []catalogEntry{*entry},
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
		SubscriberID:     nodeID,
		KeyID:            keyID,
		SigningPublicKey: pubB64,
		Status:           "SUBSCRIBED",
	}

	writes := []struct {
		path string
		body []byte
	}{
		{filepath.Join(dir, "publisher", "catalogs", "cat-ev-001-v1.json"), baselineBytes},
		{filepath.Join(dir, "publisher", "catalogs", "cat-ev-001-v1-to-v2.json"), changeBytes},
	}
	for _, w := range writes {
		if err := writeRaw(w.path, w.body); err != nil {
			return err
		}
		fmt.Println("wrote", w.path)
	}

	docWrites := []struct {
		path string
		doc  any
	}{
		{filepath.Join(dir, "publisher", "catalog-index.json"), idx},
		{filepath.Join(dir, "registry-subscription.json"), sub},
		{filepath.Join(dir, "fixture-signing-key.json"), keyFile{
			Comment:      keyWarning,
			SubscriberID: nodeID,
			KeyID:        keyID,
			Algorithm:    "Ed25519",
			SeedLabel:    seedLabel,
			PublicKey:    pubB64,
			PrivateKey:   base64.StdEncoding.EncodeToString(priv),
		}},
	}
	for _, w := range docWrites {
		if err := writeJSON(w.path, w.doc); err != nil {
			return err
		}
		fmt.Println("wrote", w.path)
	}

	fmt.Println()
	fmt.Println("Register this in the registry before crawling (see install/crawler-fixture/README.md):")
	fmt.Printf("  subscriber_id      %s\n", nodeID)
	fmt.Printf("  key_id             %s\n", keyID)
	fmt.Printf("  signing_public_key %s\n", pubB64)
	fmt.Println()
	fmt.Printf("digest %s  %s\n", entry.Baseline.Digest, entry.Baseline.URL)
	fmt.Printf("digest %s  %s\n", entry.Changes[0].Digest, entry.Changes[0].URL)
	return nil
}

// fileEntryFor builds the index's reference to a signed file already written
// to signedBytes: size and digest are hashed off the EXACT bytes that will be
// served, so the index always describes what is actually on disk.
func fileEntryFor(url string, version int, signedBytes []byte) fileEntry {
	sum := sha256.Sum256(signedBytes)
	return fileEntry{
		Version:  version,
		URL:      url,
		Size:     int64(len(signedBytes)),
		Digest:   "sha-256:" + fmt.Sprintf("%x", sum[:]),
		Encoding: "json",
	}
}

// signInPlace marshals v (with its embedded Signature field still at its
// zero value -- harmless, since canonicalization deletes "signature" before
// hashing regardless of what it holds), signs that draft with SignJSON, sets
// sig to the real {keyId, value}, and re-marshals v for the final, signed
// bytes. v must be a pointer so the second marshal picks up sig's real value.
func signInPlace(v any, sig *signature, priv ed25519.PrivateKey) ([]byte, error) {
	draft, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshaling draft: %w", err)
	}
	val, err := artifactsigner.SignJSON(draft, "signature", priv)
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	sig.KeyID = keyID
	sig.Value = val
	final, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshaling signed doc: %w", err)
	}
	return final, nil
}

// seedFrom derives the 32-byte Ed25519 seed from a label, so the fixture keypair
// is reproducible from a string anyone can read in this file.
func seedFrom(label string) []byte {
	sum := sha256.Sum256([]byte(label))
	return sum[:]
}

// writeRaw writes already-marshaled bytes exactly as given. The caller is
// responsible for including the trailing newline BEFORE hashing it for the
// index (fileEntryFor), so the digest matches what actually lands on disk.
func writeRaw(path string, body []byte) error {
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// writeJSON writes doc indented, with a trailing newline, so the emitted files
// are readable and diff cleanly. Only used for docs nothing else hashes (the
// index and the registry/key sidecar files).
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
