package catalogpublisher

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// fakeKeyManager returns a fixed Ed25519 keyset for one configured
// subscriberID; it satisfies definition.KeyManager but only Keyset is
// ever exercised here.
type fakeKeyManager struct {
	keyID  string // also doubles as the lookup key (subscriberID) in these tests
	domain string
	priv   ed25519.PrivateKey
	pub    ed25519.PublicKey
	failed bool
}

func newFakeKeyManager(t *testing.T, keyID string) *fakeKeyManager {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	return &fakeKeyManager{keyID: keyID, priv: priv, pub: pub}
}

func (f *fakeKeyManager) GenerateKeyset() (*model.Keyset, error) { return nil, nil }
func (f *fakeKeyManager) InsertKeyset(ctx context.Context, keyID string, keyset *model.Keyset) error {
	return nil
}
func (f *fakeKeyManager) Keyset(ctx context.Context, keyID string) (*model.Keyset, error) {
	if f.failed || keyID != f.keyID {
		return nil, errNotFound
	}
	return &model.Keyset{
		SubscriberID:   f.domain,
		UniqueKeyID:    f.keyID,
		SigningPrivate: base64.StdEncoding.EncodeToString(f.priv.Seed()),
		SigningPublic:  base64.StdEncoding.EncodeToString(f.pub),
	}, nil
}
func (f *fakeKeyManager) LookupNPKeys(ctx context.Context, subscriberID, uniqueKeyID string) (string, string, error) {
	return "", "", nil
}
func (f *fakeKeyManager) DeleteKeyset(ctx context.Context, keyID string) error { return nil }

var errNotFound = &keyNotFoundError{}

type keyNotFoundError struct{}

func (e *keyNotFoundError) Error() string { return "key not found" }

func validCatalogJSON(id string) json.RawMessage {
	return json.RawMessage(`{"id":"` + id + `","descriptor":{"name":"Test Provider"},"provider":{},"resources":[]}`)
}

func TestNew_RequiresKeyManagerAndKeyID(t *testing.T) {
	km := newFakeKeyManager(t, "k1")

	if _, _, err := New(context.Background(), nil, &Config{SubscriberID: "k1"}); err == nil {
		t.Fatal("expected error for nil KeyManager")
	}
	if _, _, err := New(context.Background(), km, &Config{}); err == nil {
		t.Fatal("expected error for missing keyID")
	}
	if _, _, err := New(context.Background(), km, &Config{SubscriberID: "k1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPublish_DelegatesToLibraryAndConvertsResult is an integration-level
// check that this plugin's only remaining job -- resolving the keyset and
// converting definition's types to/from pkg/catalog/publisher's -- works
// end to end. The actual diff/sign/version/compaction behavior is
// pkg/catalog/publisher's own test suite's job, not this package's.
func TestPublish_DelegatesToLibraryAndConvertsResult(t *testing.T) {
	km := newFakeKeyManager(t, "publisher-key-1")
	km.domain = "example.test"
	p, _, err := New(context.Background(), km, &Config{
		SubscriberID:  "publisher-key-1",
		PublicBaseURL: "https://cdn.example.test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: validCatalogJSON("CAT-1")}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", result.Errors)
	}
	if result.NodeID != "example.test" {
		t.Errorf("result.NodeID = %q, want example.test", result.NodeID)
	}
	if len(result.Catalogs) != 1 {
		t.Fatalf("expected 1 catalog outcome, got %+v", result.Catalogs)
	}
	got := result.Catalogs[0]
	if got.CatalogID != "example.test/CAT-1" || !got.Changed || got.Mode != "baseline" || got.Version != 1 || got.EntryVersion != 1 {
		t.Fatalf("unexpected catalog outcome: %+v", got)
	}
	if len(got.SignedEntry) == 0 {
		t.Fatal("expected a non-empty signed entry")
	}
	var entry struct {
		CatalogID string `json:"catalogId"`
		Signature struct {
			KeyID string `json:"keyId"`
			Value string `json:"value"`
		} `json:"signature"`
	}
	if err := json.Unmarshal(got.SignedEntry, &entry); err != nil {
		t.Fatalf("parsing signed entry: %v", err)
	}
	if entry.Signature.KeyID != "publisher-key-1" || entry.Signature.Value == "" {
		t.Errorf("unexpected signature on delegated entry: %+v", entry.Signature)
	}
}

func TestPublish_UnknownKeyIDFails(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "wrong-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Publish(context.Background(), definition.PublishRequest{}); err == nil {
		t.Fatal("expected error for unknown keyID")
	}
}

func TestIndexURL(t *testing.T) {
	p := &Publisher{config: &Config{}}
	if got := p.IndexURL(); got != "pending-artifact-store://catalog-index.json" {
		t.Errorf("expected placeholder index URL when PublicBaseURL is unset, got %q", got)
	}
	p.config.PublicBaseURL = "https://example.test"
	if got := p.IndexURL(); got != "https://example.test/index/becknCatalogs.index.json" {
		t.Errorf("expected index URL under PublicBaseURL, got %q", got)
	}
}

func TestDecodeKeyset_NilKeysetIsError(t *testing.T) {
	if _, _, err := decodeKeyset(nil); err == nil {
		t.Fatal("expected error for nil keyset")
	}
}

func TestDecodeKeyset_InvalidBase64IsError(t *testing.T) {
	if _, _, err := decodeKeyset(&model.Keyset{SigningPrivate: "not-base64!!", SigningPublic: "AA=="}); err == nil {
		t.Fatal("expected error for invalid base64 signing private key")
	}
	validSeed := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SeedSize))
	if _, _, err := decodeKeyset(&model.Keyset{SigningPrivate: validSeed, SigningPublic: "not-base64!!"}); err == nil {
		t.Fatal("expected error for invalid base64 signing public key")
	}
}

func TestDecodeKeyset_WrongKeyLengthIsError(t *testing.T) {
	shortSeed := base64.StdEncoding.EncodeToString([]byte("too-short"))
	if _, _, err := decodeKeyset(&model.Keyset{SigningPrivate: shortSeed, SigningPublic: base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))}); err == nil {
		t.Fatal("expected error for wrong-length signing private key")
	}

	validSeed := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SeedSize))
	shortPub := base64.StdEncoding.EncodeToString([]byte("too-short"))
	if _, _, err := decodeKeyset(&model.Keyset{SigningPrivate: validSeed, SigningPublic: shortPub}); err == nil {
		t.Fatal("expected error for wrong-length signing public key")
	}
}

func TestDecodeKeyset_Valid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	keyset := &model.Keyset{
		SigningPrivate: base64.StdEncoding.EncodeToString(priv.Seed()),
		SigningPublic:  base64.StdEncoding.EncodeToString(pub),
	}
	gotPriv, gotPub, err := decodeKeyset(keyset)
	if err != nil {
		t.Fatalf("decodeKeyset: %v", err)
	}
	if !gotPriv.Equal(priv) || !gotPub.Equal(pub) {
		t.Error("decoded keys do not match input")
	}
}
