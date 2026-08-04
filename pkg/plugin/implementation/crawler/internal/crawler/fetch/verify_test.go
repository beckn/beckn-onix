package fetch

// verify_test.go — the two self-signature gates: verifyEntrySignature (a
// catalog index entry signing itself) and verifyFileSignature (a fetched
// baseline/change file signing its own content). Every way of not being
// validly signed (tampered field, wrong key, unknown keyId, absent, no trust
// anchor at all) is rejected as a PERMANENT fault so the runner parks and
// alerts instead of retrying forever.
//
// It also covers the registry-backed KeySource, where the park-vs-retry
// distinction actually lives: a registry that ANSWERED "no such key" is a
// permanent verdict, a registry that could not answer at all is transient.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactsigner"
)

// testSigner is a publisher identity for tests: a key pair, the KeySource that
// trusts it, and helpers that self-sign a document the way a publisher would.
// Shared with client_test.go.
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

// signDoc self-signs fields the way the file spec requires: sign the JCS
// canonicalization of the document with "signature" removed, then embed
// {keyId, value} back under "signature". Returns the final raw JSON.
func (s testSigner) signDoc(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	val, err := artifactsigner.SignJSON(body, "signature", s.priv)
	if err != nil {
		t.Fatal(err)
	}
	fields["signature"] = map[string]string{"keyId": s.keyID, "value": val}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// signChangeFile signs a flat (unenveloped) change-file-shaped document.
func (s testSigner) signChangeFile(t *testing.T, catalogID string, fromV, toV int) []byte {
	t.Helper()
	return s.signDoc(t, map[string]any{
		"catalogId":   catalogID,
		"fromVersion": fromV,
		"toVersion":   toV,
		"resources":   map[string]any{"upserts": []any{}, "removals": []any{}},
		"offers":      map[string]any{"upserts": []any{}, "removals": []any{}},
	})
}

// signBaseline signs an enveloped {catalog, signature} baseline file.
func (s testSigner) signBaseline(t *testing.T, catalog map[string]any) []byte {
	t.Helper()
	return s.signDoc(t, map[string]any{"catalog": catalog})
}

// signEntry signs a catalog index entry the way the index itself would.
func (s testSigner) signEntry(t *testing.T, catalogID string) json.RawMessage {
	t.Helper()
	raw := s.signDoc(t, map[string]any{
		"catalogId": catalogID,
		"status":    "ACTIVE",
		"baseline":  map[string]any{"version": 1, "url": "https://pub.example/c/v1.json", "size": 10, "digest": "sha-256:abc"},
		"changes":   []any{},
	})
	return json.RawMessage(raw)
}

func TestVerifyFileSignature(t *testing.T) {
	signer := newTestSigner(t)
	other := newTestSigner(t)
	const nodeID = "publisher.example.com"

	t.Run("valid change file passes and needs no unwrap", func(t *testing.T) {
		raw := signer.signChangeFile(t, "p/c", 1, 2)
		out, err := verifyFileSignature(context.Background(), signer.source(), nodeID, "https://x/c.json", raw)
		if err != nil {
			t.Fatalf("verifyFileSignature() = %v, want nil", err)
		}
		if string(out) != string(raw) {
			t.Fatalf("change file must not be unwrapped: got %q, want %q", out, raw)
		}
	})

	// Regression: a change file's optional catalog-level attribute patch is a
	// non-empty "catalog" field too, but it must NOT trigger the baseline
	// unwrap -- that would silently strip the change file down to just the
	// patch and drop its resources/offers. fromVersion (required on a change
	// file, absent from a baseline) is what tells them apart, not whether
	// "catalog" happens to be non-empty.
	t.Run("change file with a non-empty catalog patch still isn't unwrapped", func(t *testing.T) {
		raw := signer.signDoc(t, map[string]any{
			"catalogId":   "p/c",
			"fromVersion": 1,
			"toVersion":   2,
			"catalog":     map[string]any{"descriptor": map[string]any{"name": "renamed"}},
			"resources":   map[string]any{"upserts": []any{}, "removals": []any{}},
			"offers":      map[string]any{"upserts": []any{}, "removals": []any{}},
		})
		out, err := verifyFileSignature(context.Background(), signer.source(), nodeID, "https://x/c.json", raw)
		if err != nil {
			t.Fatalf("verifyFileSignature() = %v, want nil", err)
		}
		if string(out) != string(raw) {
			t.Fatalf("change file with a catalog patch must not be unwrapped: got %q, want %q", out, raw)
		}
	})

	t.Run("valid baseline unwraps to the bare catalog", func(t *testing.T) {
		cat := map[string]any{"id": "p/c", "resources": []any{}}
		raw := signer.signBaseline(t, cat)
		out, err := verifyFileSignature(context.Background(), signer.source(), nodeID, "https://x/b.json", raw)
		if err != nil {
			t.Fatalf("verifyFileSignature() = %v, want nil", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("unwrapped bytes must parse as the bare catalog: %v", err)
		}
		if got["id"] != "p/c" {
			t.Fatalf("unwrapped catalog = %+v, want id p/c", got)
		}
	})

	t.Run("tampered content after signing fails", func(t *testing.T) {
		raw := signer.signChangeFile(t, "p/c", 1, 2)
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		doc["toVersion"] = json.RawMessage(`99`)
		tampered, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifyFileSignature(context.Background(), signer.source(), nodeID, "https://x/c.json", tampered); err == nil {
			t.Fatal("want an error on tampered content")
		} else {
			assertPermanentFault(t, err, faultSignature)
		}
	})

	t.Run("wrong signer under the same keyId fails", func(t *testing.T) {
		raw := other.signChangeFile(t, "p/c", 1, 2)
		_, err := verifyFileSignature(context.Background(), signer.source(), nodeID, "https://x/c.json", raw)
		assertPermanentFault(t, err, faultSignature)
	})

	t.Run("unknown keyId fails", func(t *testing.T) {
		raw := signer.signChangeFile(t, "p/c", 1, 2)
		_, err := verifyFileSignature(context.Background(), other.source(), nodeID, "https://x/c.json", raw)
		assertPermanentFault(t, err, faultSignature)
	})

	t.Run("missing signature fails closed", func(t *testing.T) {
		raw := []byte(`{"catalogId":"p/c","fromVersion":1,"toVersion":2}`)
		_, err := verifyFileSignature(context.Background(), signer.source(), nodeID, "https://x/c.json", raw)
		assertPermanentFault(t, err, faultSignature)
	})

	t.Run("baseline with no catalog content fails closed", func(t *testing.T) {
		raw := signer.signDoc(t, map[string]any{})
		_, err := verifyFileSignature(context.Background(), signer.source(), nodeID, "https://x/b.json", raw)
		if err == nil || !catalog.IsPermanent(err) {
			t.Fatalf("want a permanent fault, got %v", err)
		}
	})

	t.Run("not a JSON object fails closed", func(t *testing.T) {
		_, err := verifyFileSignature(context.Background(), signer.source(), nodeID, "https://x/c.json", []byte("not json"))
		if err == nil || !catalog.IsPermanent(err) {
			t.Fatalf("want a permanent fault, got %v", err)
		}
	})

	t.Run("no key source fails closed", func(t *testing.T) {
		raw := signer.signChangeFile(t, "p/c", 1, 2)
		_, err := verifyFileSignature(context.Background(), nil, nodeID, "https://x/c.json", raw)
		assertPermanentFault(t, err, faultSignature)
	})
}

// A key of the wrong length must be rejected rather than handed to
// ed25519.Verify, which panics on a mis-sized key.
func TestVerifyFileSignature_MalformedTrustedKey(t *testing.T) {
	signer := newTestSigner(t)
	raw := signer.signChangeFile(t, "p/c", 1, 2)
	keys := StaticKeys(map[string]ed25519.PublicKey{signer.keyID: []byte("too short")})
	_, err := verifyFileSignature(context.Background(), keys, "publisher.example.com", "https://x/c.json", raw)
	assertPermanentFault(t, err, faultSignature)
}

func TestVerifyEntrySignature(t *testing.T) {
	signer := newTestSigner(t)
	const nodeID = "publisher.example.com"

	t.Run("valid entry passes", func(t *testing.T) {
		raw := signer.signEntry(t, "p/c")
		var entry catalog.CatalogEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatal(err)
		}
		if err := verifyEntrySignature(context.Background(), signer.source(), nodeID, raw, entry.Signature); err != nil {
			t.Fatalf("verifyEntrySignature() = %v, want nil", err)
		}
	})

	t.Run("tampered entry fails", func(t *testing.T) {
		raw := signer.signEntry(t, "p/c")
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		doc["status"] = json.RawMessage(`"RETIRED"`)
		tampered, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		var entry catalog.CatalogEntry
		if err := json.Unmarshal(tampered, &entry); err != nil {
			t.Fatal(err)
		}
		if err := verifyEntrySignature(context.Background(), signer.source(), nodeID, tampered, entry.Signature); err == nil {
			t.Fatal("want an error on a tampered entry")
		} else {
			assertPermanentFault(t, err, faultSignature)
		}
	})

	t.Run("missing signature fails closed", func(t *testing.T) {
		err := verifyEntrySignature(context.Background(), signer.source(), nodeID, []byte(`{"catalogId":"p/c"}`), catalog.EntrySignature{})
		assertPermanentFault(t, err, faultSignature)
	})
}

// --- registry-backed key source -------------------------------------------

// fakeRegistry is a scripted definition.RegistryLookup: it records every call so
// a test can assert on caching, and returns whatever the script says.
type fakeRegistry struct {
	subs  []model.Subscription
	err   error
	calls int
	// gotSubscriberID / gotKeyID record the last lookup's inputs, so a test can
	// assert the nodeId really is what the crawler asks the registry for.
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
		nodeID        string
		wantKey       ed25519.PublicKey
		wantErr       bool
		wantPermanent bool
		wantIn        string
	}{
		{
			name:    "a subscribed key resolves",
			reg:     &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", good)}},
			nodeID:  "publisher.example.com",
			wantKey: good,
		},
		{
			name:    "an under-subscription key still resolves",
			reg:     &fakeRegistry{subs: []model.Subscription{registrySub("UNDER_SUBSCRIPTION", good)}},
			nodeID:  "publisher.example.com",
			wantKey: good,
		},
		{
			name:          "a registry error is TRANSIENT so the runner retries",
			reg:           &fakeRegistry{err: errors.New("connection refused")},
			nodeID:        "publisher.example.com",
			wantErr:       true,
			wantPermanent: false,
			wantIn:        "registry lookup",
		},
		{
			name:          "an unknown keyId is PERMANENT so the runner parks",
			reg:           &fakeRegistry{subs: nil},
			nodeID:        "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "has no key",
		},
		{
			name:          "an expired subscription is rejected",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("EXPIRED", good)}},
			nodeID:        "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "unusable status",
		},
		{
			name:          "an unsubscribed (revoked) subscription is rejected",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("UNSUBSCRIBED", good)}},
			nodeID:        "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "unusable status",
		},
		{
			name:          "an invalid-ssl subscription is rejected",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("INVALID_SSL", good)}},
			nodeID:        "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "unusable status",
		},
		{
			name:          "an empty signing key is rejected",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", nil)}},
			nodeID:        "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "no signing public key",
		},
		{
			name: "an undecodable signing key is rejected",
			reg: &fakeRegistry{subs: []model.Subscription{{
				KeyID: "pub-key-1", Status: "SUBSCRIBED", SigningPublicKey: "not!base64!",
			}}},
			nodeID:        "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "not valid base64",
		},
		{
			name:          "a wrong-length key is rejected before ed25519.Verify can panic",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", []byte("too short"))}},
			nodeID:        "publisher.example.com",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "want 32",
		},
		{
			name:          "an index with no nodeId cannot resolve anything",
			reg:           &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", good)}},
			nodeID:        "",
			wantErr:       true,
			wantPermanent: true,
			wantIn:        "no nodeId",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := RegistryKeys(tt.reg, time.Minute)
			key, err := src(context.Background(), tt.nodeID, "pub-key-1")
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("RegistryKeys = %v, want a key", err)
				}
				if !key.Equal(tt.wantKey) {
					t.Fatalf("resolved the wrong key")
				}
				if tt.reg.gotSubscriberID != tt.nodeID {
					t.Errorf("registry asked for subscriber %q, want the index nodeId %q", tt.reg.gotSubscriberID, tt.nodeID)
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

	// A different node is a different cache entry, even for the same keyId
	// string: two publishers may both call their key "pub-key-1".
	if _, err := src(ctx, "other.example.com", "pub-key-1"); err != nil {
		t.Fatalf("second node: %v", err)
	}
	if reg.calls != 2 {
		t.Fatalf("registry called %d times, want 2 (node is part of the cache key)", reg.calls)
	}

	// A different keyId under the same node is also its own entry, so a key
	// rotation is picked up rather than served from the old entry.
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

// TestVerifyFileSignatureWithRegistryKeys is the end-to-end shape of the
// production path: a publisher whose key the registry vouches for verifies,
// and the same file fails once that key's subscription is revoked or the
// registry is down.
func TestVerifyFileSignatureWithRegistryKeys(t *testing.T) {
	signer := newTestSigner(t)
	const nodeID = "publisher.example.com"
	raw := signer.signChangeFile(t, "p/c", 1, 2)

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
			keys := RegistryKeys(tt.reg, time.Minute)
			_, err := verifyFileSignature(context.Background(), keys, nodeID, "https://x/c.json", raw)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("verifyFileSignature() = %v, want nil", err)
				}
				if tt.reg.gotSubscriberID != nodeID {
					t.Errorf("registry asked for subscriber %q, want the index nodeId %q", tt.reg.gotSubscriberID, nodeID)
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
