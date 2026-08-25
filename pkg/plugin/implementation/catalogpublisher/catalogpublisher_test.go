package catalogpublisher

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// fakeBlobStore is an in-memory definition.CatalogBlobStore double.
type fakeBlobStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeBlobStore() *fakeBlobStore {
	return &fakeBlobStore{data: map[string][]byte{}}
}

func (f *fakeBlobStore) Get(ctx context.Context, path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.data[path]
	if !ok {
		return nil, definition.ErrBlobNotFound
	}
	return b, nil
}

func (f *fakeBlobStore) Put(ctx context.Context, path string, content []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[path] = content
	return nil
}

// fakeRegistryMetadata is a configurable definition.RegistryMetadataLookup
// double. LookupNode returns nodeRecord/nodeErr and records the nodeID it
// was called with (lastNodeID) -- checkIndexLink calls it with a synthetic
// subscriberID/dediSubscriberWildcardRegistry/keyID path.
type fakeRegistryMetadata struct {
	nodeRecord *model.SubscriberRecord
	nodeErr    error
	lastNodeID string
}

func (f *fakeRegistryMetadata) LookupRegistry(context.Context, string, string) (*model.RegistryMetadata, error) {
	panic("unused")
}

func (f *fakeRegistryMetadata) LookupNode(_ context.Context, nodeID string) (*model.SubscriberRecord, error) {
	f.lastNodeID = nodeID
	return f.nodeRecord, f.nodeErr
}

func (f *fakeRegistryMetadata) QueryByNetwork(context.Context, string) ([]model.SubscriberRecord, error) {
	panic("unused")
}

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
	bs := newFakeBlobStore()

	if _, _, err := New(context.Background(), nil, bs, nil, &Config{SubscriberID: "k1"}); err == nil {
		t.Fatal("expected error for nil KeyManager")
	}
	if _, _, err := New(context.Background(), km, bs, nil, &Config{}); err == nil {
		t.Fatal("expected error for missing keyID")
	}
	if _, _, err := New(context.Background(), km, bs, nil, &Config{SubscriberID: "k1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNew_RequiresCatalogBlobStore(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	if _, _, err := New(context.Background(), km, nil, nil, &Config{SubscriberID: "k1"}); err == nil {
		t.Fatal("expected error for nil CatalogBlobStore")
	}
}

func TestNew_CheckCatalogIndexLinkRequiresRegistryMetadata(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	bs := newFakeBlobStore()
	if _, _, err := New(context.Background(), km, bs, nil, &Config{SubscriberID: "k1", CheckCatalogIndexLink: true}); err == nil {
		t.Fatal("expected error when CheckCatalogIndexLink is true but registryMetadata is nil")
	}
	rm := &fakeRegistryMetadata{}
	if _, _, err := New(context.Background(), km, bs, rm, &Config{SubscriberID: "k1", CheckCatalogIndexLink: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNew_RejectsSlashInSubscriberIDWhenCatalogIndexLinkCheckEnabled(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	bs := newFakeBlobStore()
	rm := &fakeRegistryMetadata{}
	if _, _, err := New(context.Background(), km, bs, rm, &Config{SubscriberID: "nfh.global/k1", CheckCatalogIndexLink: true}); err == nil {
		t.Fatal("expected error for a subscriberId containing \"/\" when the catalog-index link check is enabled")
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
	p, _, err := New(context.Background(), km, newFakeBlobStore(), nil, &Config{
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
	p, _, err := New(context.Background(), km, newFakeBlobStore(), nil, &Config{SubscriberID: "wrong-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Publish(context.Background(), definition.PublishRequest{}); err == nil {
		t.Fatal("expected error for unknown keyID")
	}
}

// TestPublish_LoadsPriorStateAndPersistsResult exercises Publish's own
// storage round trip: with no prior state, the first Publish call for a
// catalogId writes a fresh baseline into the fake blob store; a second
// Publish call for the same catalogId with the same content should then be
// a metadata/no-op-shaped outcome (Publish loaded its own prior state from
// storage, without the caller supplying any PriorState).
func TestPublish_LoadsPriorStateAndPersistsResult(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	bs := newFakeBlobStore()
	p, _, err := New(context.Background(), km, bs, nil, &Config{SubscriberID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: validCatalogJSON("CAT-1")}},
	}
	first, err := p.Publish(context.Background(), req)
	if err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	if len(first.Catalogs) != 1 || first.Catalogs[0].Mode != "baseline" {
		t.Fatalf("expected a fresh baseline on first publish, got %+v", first.Catalogs)
	}
	if bs.data == nil || len(bs.data) == 0 {
		t.Fatal("expected Publish to persist something to the blob store")
	}

	second, err := p.Publish(context.Background(), req)
	if err != nil {
		t.Fatalf("second Publish: %v", err)
	}
	if len(second.Catalogs) != 1 || second.Catalogs[0].Changed {
		t.Fatalf("expected an unchanged outcome on second publish of identical content (prior state loaded from storage), got %+v", second.Catalogs)
	}
}

// TestPublish_ForceBaselineIsPerCatalog proves ForceBaseline (per
// definition.CatalogSubmission, catalog-core's Submission.Directives) only
// forces a fresh baseline for the catalog it's set on -- a sibling
// submission in the same call with unchanged content and ForceBaseline
// unset should still report Changed=false, not be swept along by the
// other one's forced baseline.
func TestPublish_ForceBaselineIsPerCatalog(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	bs := newFakeBlobStore()
	p, _, err := New(context.Background(), km, bs, nil, &Config{SubscriberID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	seed := definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{
			{CatalogID: "example.test/CAT-1", Catalog: validCatalogJSON("CAT-1")},
			{CatalogID: "example.test/CAT-2", Catalog: validCatalogJSON("CAT-2")},
		},
	}
	if _, err := p.Publish(context.Background(), seed); err != nil {
		t.Fatalf("seeding Publish: %v", err)
	}

	req := definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{
			{CatalogID: "example.test/CAT-1", Catalog: validCatalogJSON("CAT-1"), ForceBaseline: true},
			{CatalogID: "example.test/CAT-2", Catalog: validCatalogJSON("CAT-2")},
		},
	}
	result, err := p.Publish(context.Background(), req)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	byID := make(map[string]string, len(result.Catalogs))
	for _, c := range result.Catalogs {
		byID[c.CatalogID] = c.Mode
	}
	if byID["example.test/CAT-1"] != "baseline" {
		t.Errorf("CAT-1 (ForceBaseline=true): mode = %q, want baseline", byID["example.test/CAT-1"])
	}
	if byID["example.test/CAT-2"] == "baseline" {
		t.Errorf("CAT-2 (ForceBaseline unset, unchanged content): mode = %q, want it not to be forced to baseline", byID["example.test/CAT-2"])
	}
}

// TestPublish_RetireSynthesizesSubmissionForUnsubmittedID proves a
// catalogId named only in Retire (never in Catalogs) is still retired --
// catalog-core needs a Submission to attach Directives.Retire to, so
// Publish must synthesize one rather than requiring the caller to invent a
// CatalogSubmission just to retire something.
func TestPublish_RetireSynthesizesSubmissionForUnsubmittedID(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	bs := newFakeBlobStore()
	p, _, err := New(context.Background(), km, bs, nil, &Config{SubscriberID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	seed := definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: validCatalogJSON("CAT-1")}},
	}
	if _, err := p.Publish(context.Background(), seed); err != nil {
		t.Fatalf("seeding Publish: %v", err)
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{Retire: []string{"example.test/CAT-1"}})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Retirements) != 1 || result.Retirements[0].CatalogID != "example.test/CAT-1" {
		t.Fatalf("expected CAT-1 to be retired, got Retirements=%+v", result.Retirements)
	}
}

// TestPublish_RegistryLinkCheckAddsWarning verifies Publish's own wiring of
// the registry catalog-index-link check into PublishResult.Warnings when
// CheckCatalogIndexLink is configured and the link is missing.
func TestPublish_RegistryLinkCheckAddsWarning(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	rm := &fakeRegistryMetadata{nodeRecord: &model.SubscriberRecord{}}
	p, _, err := New(context.Background(), km, newFakeBlobStore(), rm, &Config{SubscriberID: "k1", CheckCatalogIndexLink: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: validCatalogJSON("CAT-1")}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %+v", result.Warnings)
	}
}

// TestPublish_RegistryLinkCheckNoWarningWhenLinked mirrors the above but
// with the index URL already present -- Publish should produce no warning.
func TestPublish_RegistryLinkCheckNoWarningWhenLinked(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	p0, _, _ := New(context.Background(), km, newFakeBlobStore(), nil, &Config{SubscriberID: "k1"})
	indexURL := p0.IndexURL()

	rm := &fakeRegistryMetadata{nodeRecord: &model.SubscriberRecord{
		MetaArrays: map[string][]string{catalogIndexMetaKey: {indexURL}},
	}}
	p, _, err := New(context.Background(), km, newFakeBlobStore(), rm, &Config{SubscriberID: "k1", CheckCatalogIndexLink: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: validCatalogJSON("CAT-1")}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %+v", result.Warnings)
	}
	wantNodeID := "k1/" + dediSubscriberWildcardRegistry + "/k1"
	if rm.lastNodeID != wantNodeID {
		t.Errorf("LookupNode called with %q, want %q", rm.lastNodeID, wantNodeID)
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

func testPublisher(t *testing.T) *Publisher {
	t.Helper()
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	p, _, err := New(context.Background(), km, newFakeBlobStore(), nil, &Config{SubscriberID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestDecodeRequest_MethodNotAllowed(t *testing.T) {
	p := testPublisher(t)
	req := httptest.NewRequest(http.MethodGet, "/catalog/publish", nil)
	_, err := p.DecodeRequest(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for non-POST method")
	}
	var statusErr *handler.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected a *handler.StatusError so the generic EndpointHandler surfaces 405, got %T: %v", err, err)
	}
	if statusErr.Status != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", statusErr.Status, http.StatusMethodNotAllowed)
	}
}

func TestDecodeRequest_InvalidJSON(t *testing.T) {
	p := testPublisher(t)
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader("not json"))
	if _, err := p.DecodeRequest(context.Background(), req); err == nil {
		t.Fatal("expected error for invalid JSON body")
	}
}

func TestDecodeRequest_RejectsEmptyRequest(t *testing.T) {
	p := testPublisher(t)
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(`{}`))
	if _, err := p.DecodeRequest(context.Background(), req); err == nil {
		t.Fatal("expected error when message.catalogs and retire are both empty")
	}
}

func TestDecodeRequest_AcceptsRetireOnlyRequest(t *testing.T) {
	p := testPublisher(t)
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(`{"retire":["example.test/CAT-OLD"]}`))
	got, err := p.DecodeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(got.Retire) != 1 || got.Retire[0] != "example.test/CAT-OLD" {
		t.Fatalf("unexpected retire list: %+v", got.Retire)
	}
}

func TestDecodeRequest_PublishDirectivesVisibleToMapsToNetworkIds(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"example.test/CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[]}],
		"publishDirectives":[{"catalogId":"example.test/CAT-1","visibleTo":["retail-network","mobility-network"]}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	got, err := p.DecodeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(got.Catalogs) != 1 {
		t.Fatalf("expected 1 catalog, got %+v", got.Catalogs)
	}
	c := got.Catalogs[0]
	if c.CatalogID != "example.test/CAT-1" || len(c.NetworkIds) != 2 || c.NetworkIds[0] != "retail-network" || c.NetworkIds[1] != "mobility-network" {
		t.Fatalf("unexpected catalog submission: %+v", c)
	}
}

func TestDecodeRequest_MissingCatalogIDIsPreservedForNonFatalRejection(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"descriptor":{"name":"missing id"}}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	got, err := p.DecodeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(got.Catalogs) != 1 || got.Catalogs[0].CatalogID != "" {
		t.Fatalf("expected 1 catalog with empty CatalogID (so Publish reports a non-fatal error), got %+v", got.Catalogs)
	}
}

func TestDecodeRequest_ForceBaseline(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{"catalogs":[{"id":"example.test/CAT-1"},{"id":"example.test/CAT-2"}],"publishDirectives":[{"catalogId":"example.test/CAT-1","forceBaseline":true}]}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	got, err := p.DecodeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if len(got.Catalogs) != 2 {
		t.Fatalf("expected 2 catalogs, got %+v", got.Catalogs)
	}
	byID := make(map[string]bool, len(got.Catalogs))
	for _, c := range got.Catalogs {
		byID[c.CatalogID] = c.ForceBaseline
	}
	if !byID["example.test/CAT-1"] {
		t.Fatal("expected CAT-1's ForceBaseline to be decoded as true")
	}
	if byID["example.test/CAT-2"] {
		t.Fatal("expected CAT-2's ForceBaseline to stay false: forceBaseline is per-catalog, not batch-wide")
	}
}

// --- validatePublishRequest coverage (items 1-4, 6 of #905), via DecodeRequest ---

func TestDecodeRequest_DirectiveMatchingSubmittedCatalogIsValid(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}],
		"publishDirectives":[{"catalogId":"CAT-1","catalogType":"REGULAR"}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	if _, err := p.DecodeRequest(context.Background(), req); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDecodeRequest_DanglingPublishDirectiveIsRejected(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}],
		"publishDirectives":[{"catalogId":"CAT-NOT-SUBMITTED","catalogType":"REGULAR"}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	_, err := p.DecodeRequest(context.Background(), req)
	if err == nil {
		t.Fatal("expected an error for a publishDirectives entry naming an unsubmitted catalogId")
	}
	if !strings.Contains(err.Error(), "CAT-NOT-SUBMITTED") || !strings.Contains(err.Error(), "does not match any submitted catalog") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeRequest_DuplicateCatalogIDInCatalogsIsRejected(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[
			{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]},
			{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}
		]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	_, err := p.DecodeRequest(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "duplicate catalog id") {
		t.Fatalf("expected a duplicate-catalog-id error, got %v", err)
	}
}

func TestDecodeRequest_DuplicatePublishDirectiveIsRejected(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}],
		"publishDirectives":[
			{"catalogId":"CAT-1","catalogType":"REGULAR"},
			{"catalogId":"CAT-1","catalogType":"MASTER"}
		]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	_, err := p.DecodeRequest(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "duplicate publishDirectives entry") {
		t.Fatalf("expected a duplicate-publishDirectives error, got %v", err)
	}
}

func TestDecodeRequest_UpdateModeFullIsRejectedAsUnsupported(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}],
		"publishDirectives":[{"catalogId":"CAT-1","catalogType":"REGULAR","updateMode":"FULL"}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	_, err := p.DecodeRequest(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), `updateMode "FULL" is not yet supported`) {
		t.Fatalf("expected updateMode FULL to be rejected, got %v", err)
	}
}

func TestDecodeRequest_UpdateModeMergeIsAccepted(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}],
		"publishDirectives":[{"catalogId":"CAT-1","catalogType":"REGULAR","updateMode":"MERGE"}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	if _, err := p.DecodeRequest(context.Background(), req); err != nil {
		t.Fatalf("expected updateMode MERGE to be accepted, got %v", err)
	}
}

func TestDecodeRequest_UpdateModeInvalidValueIsRejected(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}],
		"publishDirectives":[{"catalogId":"CAT-1","catalogType":"REGULAR","updateMode":"BOGUS"}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	_, err := p.DecodeRequest(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "invalid updateMode") {
		t.Fatalf("expected invalid updateMode to be rejected, got %v", err)
	}
}

func TestDecodeRequest_ResourceDirectiveResourceIDMustExistInCatalog(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[{"id":"ITEM-1"}]}],
		"publishDirectives":[{"catalogId":"CAT-1","catalogType":"REGULAR","resourceDirectives":[
			{"resourceId":"ITEM-DOES-NOT-EXIST","extends":{"masterResourceId":"MASTER-1"}}
		]}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	_, err := p.DecodeRequest(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "ITEM-DOES-NOT-EXIST") || !strings.Contains(err.Error(), "not found in that catalog's resources") {
		t.Fatalf("expected resourceDirectives referential error, got %v", err)
	}
}

func TestDecodeRequest_ResourceDirectiveResourceIDPresentIsAccepted(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[{"id":"ITEM-1"}]}],
		"publishDirectives":[{"catalogId":"CAT-1","catalogType":"REGULAR","resourceDirectives":[
			{"resourceId":"ITEM-1","extends":{"masterResourceId":"MASTER-1"}}
		]}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	if _, err := p.DecodeRequest(context.Background(), req); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDecodeRequest_ResourceDirectiveMissingMasterResourceIDIsRejected(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[{"id":"ITEM-1"}]}],
		"publishDirectives":[{"catalogId":"CAT-1","catalogType":"REGULAR","resourceDirectives":[
			{"resourceId":"ITEM-1","extends":{}}
		]}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	_, err := p.DecodeRequest(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "missing extends.masterResourceId") {
		t.Fatalf("expected a missing-masterResourceId error, got %v", err)
	}
}

func TestDecodeRequest_SchemaTypesValidURIsAreAccepted(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}],
		"publishDirectives":[{"catalogId":"CAT-1","catalogType":"REGULAR",
			"schemaTypes":["https://schema.beckn.org/retail/schema/1.1.0/context.jsonld"]}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	got, err := p.DecodeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(got.Catalogs) != 1 || len(got.Catalogs[0].SchemaTypes) != 1 ||
		got.Catalogs[0].SchemaTypes[0] != "https://schema.beckn.org/retail/schema/1.1.0/context.jsonld" {
		t.Fatalf("expected schemaTypes to flow through to CatalogSubmission, got %+v", got.Catalogs)
	}
}

func TestDecodeRequest_SchemaTypesNonURIIsRejected(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}],
		"publishDirectives":[{"catalogId":"CAT-1","catalogType":"REGULAR","schemaTypes":["not a uri"]}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	_, err := p.DecodeRequest(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "invalid schemaTypes") {
		t.Fatalf("expected invalid schemaTypes to be rejected, got %v", err)
	}
}

func TestDecodeRequest_SchemaTypesDuplicateIsRejected(t *testing.T) {
	p := testPublisher(t)
	body := `{"context":{"action":"catalog/publish"},"message":{
		"catalogs":[{"id":"CAT-1","descriptor":{},"provider":{},"resources":[]}],
		"publishDirectives":[{"catalogId":"CAT-1","catalogType":"REGULAR","schemaTypes":[
			"https://schema.beckn.org/retail/schema/1.1.0/context.jsonld",
			"https://schema.beckn.org/retail/schema/1.1.0/context.jsonld"
		]}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(body))
	_, err := p.DecodeRequest(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "invalid schemaTypes") {
		t.Fatalf("expected duplicate schemaTypes to be rejected, got %v", err)
	}
}

func TestValidateSchemaTypes_EmptyIsValid(t *testing.T) {
	if err := validateSchemaTypes("CAT-1", nil); err != nil {
		t.Fatalf("expected nil for empty schemaTypes, got %v", err)
	}
}

func TestValidateSchemaTypes_EmptyStringItemIsRejected(t *testing.T) {
	if err := validateSchemaTypes("CAT-1", []string{""}); err == nil {
		t.Fatal("expected an error for an empty-string schemaTypes entry")
	}
}
