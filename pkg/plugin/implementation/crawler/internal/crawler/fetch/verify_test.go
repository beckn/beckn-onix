package fetch

// verify_test.go — the per-file signature gate: a well-formed tuple passes, and
// every way of not being one (tampered field, wrong key, unknown keyId, expired,
// absent, no trust anchor at all) is rejected as a PERMANENT fault so the runner
// parks and alerts instead of retrying forever.
//
// It also covers the registry-backed KeySource, where the park-vs-retry
// distinction actually lives: a registry that ANSWERED "no such key" is a
// permanent verdict, a registry that could not answer at all is transient.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactsigner"
)

// testSigner is a publisher identity for tests: a key pair, the KeySource that
// trusts it, and a helper that signs a file entry the way a publisher's index
// would. Shared with client_test.go.
type testSigner struct {
	keyID string
	pub   ed25519.PublicKey
	priv  ed25519.PrivateKey
}

func newTestSigner(t *testing.T) testSigner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return testSigner{keyID: "pub-key-1", pub: pub, priv: priv}
}

// source is the trust anchor the composition root would hand the client. Tests
// use the static form; production always uses RegistryKeys.
func (s testSigner) source() KeySource {
	return StaticKeys(map[string]ed25519.PublicKey{s.keyID: s.pub})
}

// sign returns f with a signature over its own {catalogId, version, url,
// digest, validUntil}, valid for an hour.
func (s testSigner) sign(t *testing.T, catalogID string, f catalog.FileEntry) catalog.FileEntry {
	t.Helper()
	return s.signAs(t, catalogID, f, f, time.Now().Add(time.Hour).UTC().Truncate(time.Second))
}

// signAs signs the tuple of `signed` but attaches the signature to `f`, so a
// test can present a file entry whose fields no longer match what was signed.
func (s testSigner) signAs(t *testing.T, catalogID string, f, signed catalog.FileEntry, validUntil time.Time) catalog.FileEntry {
	t.Helper()
	value, err := artifactsigner.SignFileTuple(catalogID, int(signed.Version), signed.URL, signed.Digest, validUntil, s.priv)
	if err != nil {
		t.Fatal(err)
	}
	f.Signature = catalog.Signature{
		KeyID:      s.keyID,
		Value:      value,
		ValidUntil: validUntil.Format(time.RFC3339),
		CatalogID:  catalogID,
	}
	return f
}

func TestTupleVerifier(t *testing.T) {
	signer := newTestSigner(t)
	other := newTestSigner(t)
	const catalogID = "p/c"
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)

	base := catalog.FileEntry{Version: 3, URL: "https://pub.example/c/v3.json", Digest: "sha-256:abc123"}

	tests := []struct {
		name    string
		keys    KeySource
		entry   catalog.FileEntry
		wantErr bool
	}{
		{
			name:  "valid tuple passes",
			keys:  signer.source(),
			entry: signer.signAs(t, catalogID, base, base, future),
		},
		{
			name: "tampered digest fails",
			keys: signer.source(),
			entry: func() catalog.FileEntry {
				e := signer.signAs(t, catalogID, base, base, future)
				e.Digest = "sha-256:deadbeef" // swapped after signing
				return e
			}(),
			wantErr: true,
		},
		{
			name: "tampered url fails",
			keys: signer.source(),
			entry: func() catalog.FileEntry {
				e := signer.signAs(t, catalogID, base, base, future)
				e.URL = "https://attacker.example/c/v3.json"
				return e
			}(),
			wantErr: true,
		},
		{
			name: "tampered version fails",
			keys: signer.source(),
			entry: func() catalog.FileEntry {
				e := signer.signAs(t, catalogID, base, base, future)
				e.Version = 4
				return e
			}(),
			wantErr: true,
		},
		{
			name:    "wrong key fails",
			keys:    signer.source(), // trusts signer, but `other` signed it under the same keyId
			entry:   other.signAs(t, catalogID, base, base, future),
			wantErr: true,
		},
		{
			name:    "unknown keyId fails",
			keys:    other.source(), // trusts a different keyId entirely
			entry:   signer.signAs(t, catalogID, base, base, future),
			wantErr: true,
		},
		{
			name:    "expired validUntil fails",
			keys:    signer.source(),
			entry:   signer.signAs(t, catalogID, base, base, past),
			wantErr: true,
		},
		{
			name:    "missing signature fails closed",
			keys:    signer.source(),
			entry:   base, // no Signature at all
			wantErr: true,
		},
		{
			name: "missing keyId fails closed",
			keys: signer.source(),
			entry: func() catalog.FileEntry {
				e := signer.signAs(t, catalogID, base, base, future)
				e.Signature.KeyID = ""
				return e
			}(),
			wantErr: true,
		},
		{
			name: "missing validUntil fails closed",
			keys: signer.source(),
			entry: func() catalog.FileEntry {
				e := signer.signAs(t, catalogID, base, base, future)
				e.Signature.ValidUntil = ""
				return e
			}(),
			wantErr: true,
		},
		{
			name: "malformed validUntil fails closed",
			keys: signer.source(),
			entry: func() catalog.FileEntry {
				e := signer.signAs(t, catalogID, base, base, future)
				e.Signature.ValidUntil = "next tuesday"
				return e
			}(),
			wantErr: true,
		},
		{
			name: "unbound catalogId fails closed",
			keys: signer.source(),
			entry: func() catalog.FileEntry {
				e := signer.signAs(t, catalogID, base, base, future)
				e.Signature.CatalogID = "" // index decoder never stamped it
				return e
			}(),
			wantErr: true,
		},
		{
			name: "signature replayed onto another catalog fails",
			keys: signer.source(),
			entry: func() catalog.FileEntry {
				e := signer.signAs(t, catalogID, base, base, future)
				e.Signature.CatalogID = "p/other"
				return e
			}(),
			wantErr: true,
		},
		{
			name:    "no key source fails closed",
			keys:    nil,
			entry:   signer.signAs(t, catalogID, base, base, future),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := tupleVerifier{keys: tt.keys, now: func() time.Time { return now }}
			err := v.verify(context.Background(), tt.entry)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("verify() = %v, want nil", err)
				}
				return
			}
			// Every rejection must park + alert, not retry: an unsigned or forged
			// entry stays that way however often it is re-fetched.
			assertPermanentFault(t, err, faultSignature)
		})
	}
}

// A key of the wrong length must be rejected rather than handed to
// ed25519.Verify, which panics on a mis-sized key.
func TestTupleVerifier_MalformedTrustedKey(t *testing.T) {
	signer := newTestSigner(t)
	entry := signer.sign(t, "p/c", catalog.FileEntry{Version: 1, URL: "https://pub.example/c/v1.json", Digest: "sha-256:abc"})
	v := tupleVerifier{
		keys: StaticKeys(map[string]ed25519.PublicKey{signer.keyID: []byte("too short")}),
		now:  time.Now,
	}
	assertPermanentFault(t, v.verify(context.Background(), entry), faultSignature)
}

// --- registry-backed key source -------------------------------------------

// fakeRegistry is a scripted definition.RegistryLookup: it records every call so
// a test can assert on caching, and returns whatever the script says.
type fakeRegistry struct {
	subs  []model.Subscription
	err   error
	calls int
	// gotSubscriberID / gotKeyID record the last lookup's inputs, so a test can
	// assert the participantId really is what the crawler asks the registry for.
	gotSubscriberID string
	gotKeyID        string
}

func (r *fakeRegistry) Lookup(_ context.Context, req *model.Subscription) ([]model.Subscription, error) {
	r.calls++
	r.gotSubscriberID = req.SubscriberID
	r.gotKeyID = req.KeyID
	if r.err != nil {
		return nil, r.err
	}
	return r.subs, nil
}

// registrySub builds a registry answer carrying pub as a base64 signing key.
func registrySub(status string, pub []byte) model.Subscription {
	return model.Subscription{
		Subscriber:       model.Subscriber{SubscriberID: "publisher.example.com"},
		KeyID:            "pub-key-1",
		SigningPublicKey: base64.StdEncoding.EncodeToString(pub),
		Status:           status,
	}
}

// TestRegistryKeys covers what the registry answers and how each answer is
// classified. The permanent/transient split is the point: it decides whether the
// runner parks the catalog or retries it, so a registry outage must NOT look
// like a forged file.
func TestRegistryKeys(t *testing.T) {
	good, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		reg           *fakeRegistry
		participantID string
		wantKey       ed25519.PublicKey
		wantErr       bool
		wantPermanent bool
		wantIn        string
	}{
		{
			name:          "a subscribed key resolves",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", good)}},
			participantID: "publisher.example.com",
			wantKey:       good,
		},
		{
			name:          "an under-subscription key still resolves",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("UNDER_SUBSCRIPTION", good)}},
			participantID: "publisher.example.com",
			wantKey:       good,
		},
		{
			name:          "a registry error is TRANSIENT so the runner retries",
			reg:           &fakeRegistry{err: errors.New("connection refused")},
			participantID: "publisher.example.com",
			wantErr:       true,
			wantPermanent: false,
			wantIn:        "registry lookup",
		},
		{
			name:          "an unknown keyId is PERMANENT so the runner parks",
			reg:           &fakeRegistry{subs: nil},
			participantID: "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "has no key",
		},
		{
			name:          "an expired subscription is rejected",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("EXPIRED", good)}},
			participantID: "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "unusable status",
		},
		{
			name:          "an unsubscribed (revoked) subscription is rejected",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("UNSUBSCRIBED", good)}},
			participantID: "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "unusable status",
		},
		{
			name:          "an invalid-ssl subscription is rejected",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("INVALID_SSL", good)}},
			participantID: "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "unusable status",
		},
		{
			name:          "an empty signing key is rejected",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", nil)}},
			participantID: "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "no signing public key",
		},
		{
			name: "an undecodable signing key is rejected",
			reg: &fakeRegistry{subs: []model.Subscription{{
				KeyID: "pub-key-1", Status: "SUBSCRIBED", SigningPublicKey: "not!base64!",
			}}},
			participantID: "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "not valid base64",
		},
		{
			name:          "a wrong-length key is rejected before ed25519.Verify can panic",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", []byte("too short"))}},
			participantID: "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "want 32",
		},
		{
			name:          "an index with no participantId cannot resolve anything",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", good)}},
			participantID: "",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "no participantId",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := RegistryKeys(tt.reg, time.Minute)
			key, err := src(context.Background(), tt.participantID, "pub-key-1")
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("RegistryKeys = %v, want a key", err)
				}
				if !key.Equal(tt.wantKey) {
					t.Fatalf("resolved the wrong key")
				}
				if tt.reg.gotSubscriberID != tt.participantID {
					t.Errorf("registry asked for subscriber %q, want the index participantId %q", tt.reg.gotSubscriberID, tt.participantID)
				}
				if tt.reg.gotKeyID != "pub-key-1" {
					t.Errorf("registry asked for keyId %q, want %q", tt.reg.gotKeyID, "pub-key-1")
				}
				return
			}
			if err == nil {
				t.Fatal("want an error, got a key")
			}
			if key != nil {
				t.Error("a failed resolution must return no key")
			}
			if got := catalog.IsPermanent(err); got != tt.wantPermanent {
				t.Fatalf("IsPermanent(%v) = %v, want %v (this decides park vs retry)", err, got, tt.wantPermanent)
			}
			wantClass := catalog.FaultTransient
			if tt.wantPermanent {
				wantClass = catalog.FaultSignature
			}
			if got := catalog.ClassifyFault(0, err); got != wantClass {
				t.Errorf("ClassifyFault(%v) = %q, want %q", err, got, wantClass)
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("error %q does not mention %q", err.Error(), tt.wantIn)
			}
		})
	}
}

// A crawler wired with no registry at all must fail closed rather than skip
// verification.
func TestRegistryKeys_NilRegistry(t *testing.T) {
	src := RegistryKeys(nil, time.Minute)
	_, err := src(context.Background(), "publisher.example.com", "pub-key-1")
	if err == nil {
		t.Fatal("want an error when no registry is configured")
	}
	if !catalog.IsPermanent(err) || catalog.ClassifyFault(0, err) != catalog.FaultSignature {
		t.Fatalf("err = %v, want a permanent signature fault", err)
	}
}

// TestRegistryKeys_Caches pins the reason the cache exists: a large index is
// thousands of files, and one registry round trip per file would swamp the
// registry and dominate crawl latency.
func TestRegistryKeys_Caches(t *testing.T) {
	good, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", good)}}
	src := RegistryKeys(reg, time.Minute)
	ctx := context.Background()

	for i := 0; i < 50; i++ {
		if _, err := src(ctx, "publisher.example.com", "pub-key-1"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if reg.calls != 1 {
		t.Fatalf("registry called %d times for 50 files, want 1 (lookups must be cached)", reg.calls)
	}

	// A different participant is a different cache entry, even for the same
	// keyId string: two subscribers may both call their key "pub-key-1".
	if _, err := src(ctx, "other.example.com", "pub-key-1"); err != nil {
		t.Fatalf("second participant: %v", err)
	}
	if reg.calls != 2 {
		t.Fatalf("registry called %d times, want 2 (participant is part of the cache key)", reg.calls)
	}

	// A different keyId under the same participant is also its own entry, so a
	// key rotation is picked up rather than served from the old entry.
	if _, err := src(ctx, "publisher.example.com", "pub-key-2"); err != nil {
		t.Fatalf("rotated keyId: %v", err)
	}
	if reg.calls != 3 {
		t.Fatalf("registry called %d times, want 3 (keyId is part of the cache key)", reg.calls)
	}
}

// A failure must never be cached: a registry blip that pinned itself for the
// whole TTL would keep failing files long after the registry recovered.
func TestRegistryKeys_DoesNotCacheFailures(t *testing.T) {
	reg := &fakeRegistry{err: errors.New("connection refused")}
	src := RegistryKeys(reg, time.Minute)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := src(ctx, "publisher.example.com", "pub-key-1"); err == nil {
			t.Fatalf("call %d: want an error", i)
		}
	}
	if reg.calls != 3 {
		t.Fatalf("registry called %d times, want 3 (failures must not be cached)", reg.calls)
	}
}

// TestRegistryKeys_Expires checks the TTL is honoured, so a rotated or revoked
// key is re-resolved rather than trusted forever.
func TestRegistryKeys_Expires(t *testing.T) {
	good, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", good)}}
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	c := &keyCache{reg: reg, ttl: time.Minute, now: func() time.Time { return now }, entries: map[keyCacheKey]keyCacheEntry{}}
	ctx := context.Background()

	if _, err := c.get(ctx, "publisher.example.com", "pub-key-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.get(ctx, "publisher.example.com", "pub-key-1"); err != nil {
		t.Fatal(err)
	}
	if reg.calls != 1 {
		t.Fatalf("registry called %d times inside the TTL, want 1", reg.calls)
	}

	now = now.Add(2 * time.Minute)
	if _, err := c.get(ctx, "publisher.example.com", "pub-key-1"); err != nil {
		t.Fatal(err)
	}
	if reg.calls != 2 {
		t.Fatalf("registry called %d times after the TTL, want 2", reg.calls)
	}
}

// TestVerifyWithRegistryKeys is the end-to-end shape of the production path: a
// publisher whose key the registry vouches for verifies, and the same file
// fails once that key's subscription is revoked or the registry is down.
func TestVerifyWithRegistryKeys(t *testing.T) {
	signer := newTestSigner(t)
	const participant = "publisher.example.com"
	const catalogID = "p/c"
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

	entry := signer.signAs(t, catalogID,
		catalog.FileEntry{Version: 3, URL: "https://pub.example/c/v3.json", Digest: "sha-256:abc123"},
		catalog.FileEntry{Version: 3, URL: "https://pub.example/c/v3.json", Digest: "sha-256:abc123"},
		now.Add(time.Hour))
	// StampCatalogIDs would have set this from the index; set it directly here.
	entry.Signature.ParticipantID = participant

	tests := []struct {
		name          string
		reg           *fakeRegistry
		wantErr       bool
		wantPermanent bool
	}{
		{
			name: "registry vouches for the key and the file verifies",
			reg:  &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", signer.pub)}},
		},
		{
			name:          "a revoked subscription parks the file",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("UNSUBSCRIBED", signer.pub)}},
			wantErr:       true,
			wantPermanent: true,
		},
		{
			name:          "an unreachable registry retries the file",
			reg:           &fakeRegistry{err: errors.New("i/o timeout")},
			wantErr:       true,
			wantPermanent: false,
		},
		{
			name:          "a registry key that is not the signer's parks the file",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", newTestSigner(t).pub)}},
			wantErr:       true,
			wantPermanent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := tupleVerifier{
				keys: RegistryKeys(tt.reg, time.Minute),
				now:  func() time.Time { return now },
			}
			err := v.verify(context.Background(), entry)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("verify() = %v, want nil", err)
				}
				if tt.reg.gotSubscriberID != participant {
					t.Errorf("registry asked for subscriber %q, want the index participantId %q", tt.reg.gotSubscriberID, participant)
				}
				return
			}
			if err == nil {
				t.Fatal("want an error")
			}
			if tt.wantPermanent {
				assertPermanentFault(t, err, faultSignature)
				return
			}
			if catalog.IsPermanent(err) {
				t.Fatalf("%v: a registry outage must stay transient (retry), not park", err)
			}
			if got := catalog.ClassifyFault(0, err); got != catalog.FaultTransient {
				t.Fatalf("ClassifyFault = %q, want %q", got, catalog.FaultTransient)
			}
		})
	}
}
