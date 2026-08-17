package crawler

// verify_test.go — covers key resolution (StaticKeys, RegistryKeys caching
// and transient-vs-permanent classification of a registry outcome) and the
// generic self-signature gate, VerifySignature.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// signDoc signs fields (minus sigField, which is added after) with priv,
// mirroring the file spec's self-signing convention that VerifySignature
// checks against.
func signDoc(t *testing.T, priv ed25519.PrivateKey, sigField string, fields map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := artifactverifier.CanonicalizeJCSExcluding(body, sigField)
	if err != nil {
		t.Fatal(err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, canonical))
	fields[sigField] = sigB64
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStaticKeys(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	keys := StaticKeys(map[string]ed25519.PublicKey{"k1": pub})

	got, err := keys(context.Background(), "node", "k1")
	if err != nil || !got.Equal(pub) {
		t.Fatalf("keys(k1) = %v, %v; want %v, nil", got, err, pub)
	}

	_, err = keys(context.Background(), "node", "unknown")
	assertPermanentFault(t, err, FaultSignature)
}

func TestVerifySignature_Success(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{"catalogId": "p/c", "version": 1}
	raw := signDoc(t, priv, "signature", fields)

	var doc struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	keys := StaticKeys(map[string]ed25519.PublicKey{"k1": pub})
	if err := VerifySignature(context.Background(), keys, "node", "k1", doc.Signature, raw, "signature"); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
}

func TestVerifySignature_WrongKeyFails(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{"catalogId": "p/c"}
	raw := signDoc(t, priv, "signature", fields)
	var doc struct {
		Signature string `json:"signature"`
	}
	json.Unmarshal(raw, &doc)

	keys := StaticKeys(map[string]ed25519.PublicKey{"k1": otherPub})
	err = VerifySignature(context.Background(), keys, "node", "k1", doc.Signature, raw, "signature")
	assertPermanentFault(t, err, FaultSignature)
}

func TestVerifySignature_NoSignatureIsPermanent(t *testing.T) {
	keys := StaticKeys(nil)
	err := VerifySignature(context.Background(), keys, "node", "", "", []byte(`{}`), "signature")
	assertPermanentFault(t, err, FaultSignature)
}

func TestVerifySignature_NoKeySourceIsPermanent(t *testing.T) {
	err := VerifySignature(context.Background(), nil, "node", "k1", "sig", []byte(`{}`), "signature")
	assertPermanentFault(t, err, FaultSignature)
}

// fakeRegistry drives RegistryKeys in tests, without a real network registry.
type fakeRegistry struct {
	subs []model.Subscription
	err  error
	n    int // calls made, for the caching test
}

func (r *fakeRegistry) Lookup(_ context.Context, _ *model.Subscription) ([]model.Subscription, error) {
	r.n++
	if r.err != nil {
		return nil, r.err
	}
	return r.subs, nil
}

func registrySub(status string, pub ed25519.PublicKey) model.Subscription {
	return model.Subscription{
		KeyID:            "k1",
		Status:           status,
		SigningPublicKey: base64.StdEncoding.EncodeToString(pub),
	}
}

func TestRegistryKeys_CachesSuccess(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := &fakeRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", pub)}}
	keys := RegistryKeys(reg, time.Minute)

	for i := 0; i < 3; i++ {
		got, err := keys(context.Background(), "node", "k1")
		if err != nil || !got.Equal(pub) {
			t.Fatalf("call %d: keys() = %v, %v", i, got, err)
		}
	}
	if reg.n != 1 {
		t.Fatalf("registry called %d times, want 1 (cached)", reg.n)
	}
}

func TestRegistryKeys_UnusableStatusIsPermanent(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := &fakeRegistry{subs: []model.Subscription{registrySub("UNSUBSCRIBED", pub)}}
	keys := RegistryKeys(reg, time.Minute)
	_, err = keys(context.Background(), "node", "k1")
	assertPermanentFault(t, err, FaultSignature)
}

func TestRegistryKeys_NoSuchKeyIsPermanent(t *testing.T) {
	reg := &fakeRegistry{subs: nil}
	keys := RegistryKeys(reg, time.Minute)
	_, err := keys(context.Background(), "node", "k1")
	assertPermanentFault(t, err, FaultSignature)
}

func TestRegistryKeys_OutageIsTransient(t *testing.T) {
	reg := &fakeRegistry{err: errors.New("registry unreachable")}
	keys := RegistryKeys(reg, time.Minute)
	_, err := keys(context.Background(), "node", "k1")
	if err == nil {
		t.Fatal("expected an error")
	}
	if IsPermanent(err) {
		t.Fatalf("a registry outage must stay transient (retryable), got permanent: %v", err)
	}
}

func TestRegistryKeys_NoNodeIDIsPermanent(t *testing.T) {
	keys := RegistryKeys(&fakeRegistry{}, time.Minute)
	_, err := keys(context.Background(), "", "k1")
	assertPermanentFault(t, err, FaultSignature)
}
