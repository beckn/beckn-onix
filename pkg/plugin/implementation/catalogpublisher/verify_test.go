package catalogpublisher

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"
)

// stubRegistryLookup is a definition.RegistryLookup double giving each test
// full control over what Lookup returns, unlike fakeVerifyRegistry (which
// only ever returns a single usable subscription or nothing).
type stubRegistryLookup struct {
	subs []model.Subscription
	err  error
}

func (s stubRegistryLookup) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	return s.subs, s.err
}

func TestResolveRegistryKey_TransientLookupError(t *testing.T) {
	lookupErr := errors.New("registry unavailable")
	_, err := resolveRegistryKey(context.Background(), stubRegistryLookup{err: lookupErr}, "node", "key1")
	if err == nil {
		t.Fatal("expected an error")
	}
	if crawler.IsPermanent(err) {
		t.Errorf("a registry lookup error should classify as transient (retryable), got a PermanentError: %v", err)
	}
}

func TestResolveRegistryKey_NoSubscription(t *testing.T) {
	_, err := resolveRegistryKey(context.Background(), stubRegistryLookup{}, "node", "key1")
	assertPermanentSignatureFault(t, err)
}

func TestResolveRegistryKey_UnusableStatus(t *testing.T) {
	subs := []model.Subscription{{SigningPublicKey: "AA==", Status: "UNSUBSCRIBED"}}
	_, err := resolveRegistryKey(context.Background(), stubRegistryLookup{subs: subs}, "node", "key1")
	assertPermanentSignatureFault(t, err)
}

func TestResolveRegistryKey_EmptySigningPublicKey(t *testing.T) {
	subs := []model.Subscription{{Status: "SUBSCRIBED"}}
	_, err := resolveRegistryKey(context.Background(), stubRegistryLookup{subs: subs}, "node", "key1")
	assertPermanentSignatureFault(t, err)
}

func TestResolveRegistryKey_InvalidBase64(t *testing.T) {
	subs := []model.Subscription{{SigningPublicKey: "not-base64!!", Status: "SUBSCRIBED"}}
	_, err := resolveRegistryKey(context.Background(), stubRegistryLookup{subs: subs}, "node", "key1")
	assertPermanentSignatureFault(t, err)
}

func TestResolveRegistryKey_WrongKeySize(t *testing.T) {
	subs := []model.Subscription{{SigningPublicKey: base64.StdEncoding.EncodeToString([]byte("too-short")), Status: "SUBSCRIBED"}}
	_, err := resolveRegistryKey(context.Background(), stubRegistryLookup{subs: subs}, "node", "key1")
	assertPermanentSignatureFault(t, err)
}

func TestResolveRegistryKey_Valid(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	subs := []model.Subscription{{SigningPublicKey: base64.StdEncoding.EncodeToString(pub), Status: "SUBSCRIBED"}}
	got, err := resolveRegistryKey(context.Background(), stubRegistryLookup{subs: subs}, "node", "key1")
	if err != nil {
		t.Fatalf("resolveRegistryKey: %v", err)
	}
	if !got.Equal(pub) {
		t.Error("returned key does not match the registry's key")
	}
}

func assertPermanentSignatureFault(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !crawler.IsPermanent(err) {
		t.Fatalf("expected a PermanentError (retrying won't fix a bad/unusable key), got: %v", err)
	}
	if crawler.PermanentClass(err) != crawler.FaultSignature {
		t.Errorf("class = %q, want %q", crawler.PermanentClass(err), crawler.FaultSignature)
	}
}

func TestRegistryKeySource_NilRegistry(t *testing.T) {
	src := registryKeySource(nil)
	_, err := src(context.Background(), "node", "key1")
	assertPermanentSignatureFault(t, err)
}

func TestRegistryKeySource_EmptyNodeID(t *testing.T) {
	src := registryKeySource(stubRegistryLookup{})
	_, err := src(context.Background(), "", "key1")
	assertPermanentSignatureFault(t, err)
}

func TestChangeAtVersion_Found(t *testing.T) {
	target := catalog.FileEntry{Version: 3}
	changes := []catalog.FileEntry{{Version: 1}, {Version: 2}, target}
	got, ok := changeAtVersion(changes, 3)
	if !ok {
		t.Fatal("expected to find version 3")
	}
	if got.Version != 3 {
		t.Errorf("got version %d, want 3", got.Version)
	}
}

func TestChangeAtVersion_NotFound(t *testing.T) {
	changes := []catalog.FileEntry{{Version: 1}, {Version: 2}}
	if _, ok := changeAtVersion(changes, 99); ok {
		t.Fatal("expected version 99 not to be found")
	}
}

func TestChangeAtVersion_Empty(t *testing.T) {
	if _, ok := changeAtVersion(nil, 1); ok {
		t.Fatal("expected no match against an empty slice")
	}
}

// TestVerifyPublished_RetriesTransientFailureThenSucceeds proves the
// read-after-write retry (see verify.go's verifyRetryAttempts): a registry
// lookup that fails transiently on its first call (simulating a blob
// store/CDN that hasn't caught up yet) but succeeds on a later one still
// ends with no verify errors, instead of an immediate false REJECTED.
func TestVerifyPublished_RetriesTransientFailureThenSucceeds(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	bs := newFakeBlobStore()

	pubB64 := ""
	failuresLeft := 2
	flaky := &flakyThenUsableRegistry{
		pubB64Fn:     func() string { return pubB64 },
		failuresLeft: &failuresLeft,
	}

	srv := newVerifyingBlobServer(t, bs)
	cfg := &Config{SubscriberID: "k1", PublicBaseURL: srv.URL, AllowPrivateVerifyHosts: true}
	p, _, err := New(context.Background(), km, bs, flaky, nil, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pubB64 = base64.StdEncoding.EncodeToString(km.pub)

	result, err := p.Publish(context.Background(), definitionPublishRequestOneCatalog("example.test/CAT-1"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected verification to succeed after retrying past transient failures, got errors: %+v", result.Errors)
	}
	if failuresLeft != 0 {
		t.Errorf("expected the flaky registry's failuresLeft to reach 0 (all injected failures consumed), got %d", failuresLeft)
	}
}

// TestVerifyPublished_PermanentFailureIsNotRetried proves a genuine content
// problem -- the file on the blob store no longer matches the digest its
// own signed index entry recorded (a crawler.PermanentError, not a
// propagation delay) -- is reported without burning through every retry
// attempt's backoff. The index entry itself still verifies fine (its
// self-signature is untouched), only the file bytes are corrupted after
// Publish already wrote them, isolating this to a genuine
// FaultDigestMismatch rather than the "not found in index" ambiguity an
// unusable/rotated key would produce.
func TestVerifyPublished_PermanentFailureIsNotRetried(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	bs := newFakeBlobStore()
	p := newVerifiablePublisher(t, km, bs, nil, &Config{SubscriberID: "k1"})

	published, err := p.Publish(context.Background(), definitionPublishRequestOneCatalog("example.test/CAT-1"))
	if err != nil {
		t.Fatalf("seeding Publish: %v", err)
	}
	if len(published.Errors) != 0 {
		t.Fatalf("expected the seeding publish to verify cleanly, got errors: %+v", published.Errors)
	}

	bs.mu.Lock()
	bs.data["catalogs/CAT-1.v1.json"] = []byte(`{"corrupted":"content, no longer matches the index's recorded digest"}`)
	bs.mu.Unlock()

	start := time.Now()
	errs := p.verifyPublished(context.Background(), published)
	elapsed := time.Since(start)

	if len(errs) == 0 {
		t.Fatal("expected a verify error for a corrupted (digest-mismatched) published file")
	}
	// verifyRetryBaseDelay*(1+2) = 600ms is the minimum time a retried
	// (transient-looking) failure would take across 3 attempts; a permanent
	// failure must short-circuit on the first attempt and stay well under
	// that.
	if elapsed >= verifyRetryBaseDelay*3 {
		t.Errorf("verifyPublished took %v for a permanent failure; expected it to short-circuit without retry backoff", elapsed)
	}
}

// TestLogVerifyOutcome_Passed proves a clean verification logs at Info,
// naming the touched catalog(s), so a log stream alone can confirm success
// without inspecting the HTTP response body.
func TestLogVerifyOutcome_Passed(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	bs := newFakeBlobStore()
	p := newVerifiablePublisher(t, km, bs, nil, &Config{SubscriberID: "k1"})

	var buf bytes.Buffer
	p.log = slog.New(slog.NewTextHandler(&buf, nil))

	if _, err := p.Publish(context.Background(), definitionPublishRequestOneCatalog("example.test/CAT-1")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "level=INFO") || !strings.Contains(out, "post-write verification passed") {
		t.Fatalf("expected an Info-level post-write-verification-passed log line, got:\n%s", out)
	}
	if !strings.Contains(out, "example.test/CAT-1") {
		t.Fatalf("expected the log line to name the passed catalog, got:\n%s", out)
	}
}

// TestLogVerifyOutcome_Failed proves a verification failure logs at Warn,
// naming which catalog was rejected.
func TestLogVerifyOutcome_Failed(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	bs := newFakeBlobStore()
	p := newVerifiablePublisher(t, km, bs, nil, &Config{SubscriberID: "k1"})

	// logVerifyOutcome is exercised directly against a synthetic
	// verify-error here, rather than forcing a real Publish call through a
	// genuine verification failure -- simpler, and the mapping from
	// touched/failed catalog IDs to the logged message is what's under
	// test, not verifyPublished's own fault-classification (already
	// covered elsewhere in this file).
	var buf bytes.Buffer
	p.log = slog.New(slog.NewTextHandler(&buf, nil))
	result := definitionPublishResultOneChanged("example.test/CAT-1")
	verifyErrs := []definition.PublishError{{CatalogID: "example.test/CAT-1", Stage: "verify", Reason: "boom"}}
	p.logVerifyOutcome(result, verifyErrs)

	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "post-write verification failed") {
		t.Fatalf("expected a Warn-level post-write-verification-failed log line, got:\n%s", out)
	}
	if !strings.Contains(out, "example.test/CAT-1") {
		t.Fatalf("expected the log line to name the rejected catalog, got:\n%s", out)
	}
}

func definitionPublishRequestOneCatalog(catalogID string) definition.PublishRequest {
	return definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: catalogID, Catalog: validCatalogJSON("CAT-1")}},
	}
}

func TestVerifyOutcome_EntryNotFoundInIndex(t *testing.T) {
	p := testPublisher(t)
	err := p.verifyOutcome(context.Background(), "example.test", catalog.Index{}, definition.CatalogPublishOutcome{CatalogID: "example.test/CAT-1", Mode: "baseline"})
	if err == nil {
		t.Fatal("expected an error when the catalog isn't in the re-fetched index")
	}
}

func TestVerifyOutcome_ChangeVersionNotFoundInIndex(t *testing.T) {
	p := testPublisher(t)
	idx := catalog.Index{Catalogs: []catalog.CatalogEntry{{
		CatalogID: "example.test/CAT-1",
		Changes:   []catalog.FileEntry{{ToVersion: 1}},
	}}}
	err := p.verifyOutcome(context.Background(), "example.test", idx,
		definition.CatalogPublishOutcome{CatalogID: "example.test/CAT-1", Mode: "change", Version: 99})
	if err == nil {
		t.Fatal("expected an error when no changes[] entry matches the outcome's version")
	}
}

func TestVerifyOutcome_LatestExpectedButMissingFromIndex(t *testing.T) {
	p := testPublisher(t)
	idx := catalog.Index{Catalogs: []catalog.CatalogEntry{{CatalogID: "example.test/CAT-1"}}}
	err := p.verifyOutcome(context.Background(), "example.test", idx, definition.CatalogPublishOutcome{
		CatalogID:     "example.test/CAT-1",
		LatestContent: []byte(`{}`),
	})
	if err == nil {
		t.Fatal("expected an error when LatestContent was produced but the re-fetched entry has no Latest pointer")
	}
}

func TestVerifyRetirement_EntryNotFoundInIndex(t *testing.T) {
	p := testPublisher(t)
	err := p.verifyRetirement(context.Background(), "example.test", catalog.Index{},
		definition.RetirementOutcome{CatalogID: "example.test/CAT-1"}, false)
	if err == nil {
		t.Fatal("expected an error when the retired catalog isn't in the re-fetched index")
	}
}

func TestVerifyRetirement_LatestExpectedButMissingFromIndex(t *testing.T) {
	p := testPublisher(t)
	idx := catalog.Index{Catalogs: []catalog.CatalogEntry{{CatalogID: "example.test/CAT-1"}}}
	err := p.verifyRetirement(context.Background(), "example.test", idx,
		definition.RetirementOutcome{CatalogID: "example.test/CAT-1"}, true)
	if err == nil {
		t.Fatal("expected an error when a final latest write was expected but the re-fetched entry has no Latest pointer")
	}
}

// TestVerifyPublished_IndexFetchFailureIsReportedForEveryTouchedCatalog
// exercises verifyPublished's whole-index-fetch-failure branch: the
// server that would serve the index is gone, so FetchIndex itself fails
// (a transient, network-shaped error) and every changed/retired catalog in
// the batch should be reported, not just silently dropped.
func TestVerifyPublished_IndexFetchFailureIsReportedForEveryTouchedCatalog(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	bs := newFakeBlobStore()
	srv := newVerifyingBlobServer(t, bs)
	cfg := &Config{SubscriberID: "k1", PublicBaseURL: srv.URL, AllowPrivateVerifyHosts: true}
	p, _, err := New(context.Background(), km, bs, fakeVerifyRegistry{PubB64: base64.StdEncoding.EncodeToString(km.pub)}, nil, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.Close() // FetchIndex now fails to even connect.

	result := definitionPublishResultOneChanged("example.test/CAT-1")
	errs := p.verifyPublished(context.Background(), result)
	if len(errs) != 1 || errs[0].CatalogID != "example.test/CAT-1" || errs[0].Stage != "verify" {
		t.Fatalf("expected one verify error for the changed catalog, got %+v", errs)
	}
}

func definitionPublishResultOneChanged(catalogID string) definition.PublishResult {
	return definition.PublishResult{
		NodeID: "example.test",
		Catalogs: []definition.CatalogPublishOutcome{
			{CatalogID: catalogID, Changed: true, Mode: "baseline"},
		},
	}
}

// flakyThenUsableRegistry fails Lookup with a plain (transient) error a
// fixed number of times before returning a usable key -- simulating a
// registry/store that hasn't caught up yet, without ever producing a
// crawler.PermanentError.
type flakyThenUsableRegistry struct {
	pubB64Fn     func() string
	failuresLeft *int
}

func (f *flakyThenUsableRegistry) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	if *f.failuresLeft > 0 {
		*f.failuresLeft--
		return nil, errors.New("registry temporarily unavailable")
	}
	return []model.Subscription{{SigningPublicKey: f.pubB64Fn(), Status: "SUBSCRIBED"}}, nil
}
