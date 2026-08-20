package catalogcrawler

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"

	"github.com/beckn/catalog-core/pkg/catalog/crawler"
)

// fakeSubscriptionRegistry drives newRegistryKeySource in tests, without a
// real registry.
type fakeSubscriptionRegistry struct {
	subs []model.Subscription
	err  error
	n    int // calls made
}

func (r *fakeSubscriptionRegistry) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
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

func TestNewRegistryKeySource_Success(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := &fakeSubscriptionRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", pub)}}
	keys := newRegistryKeySource(reg)

	got, err := keys(context.Background(), "node", "k1")
	if err != nil || !got.Equal(pub) {
		t.Fatalf("keys() = %v, %v; want %v, nil", got, err, pub)
	}
}

func TestNewRegistryKeySource_NoRegistryIsPermanent(t *testing.T) {
	keys := newRegistryKeySource(nil)
	_, err := keys(context.Background(), "node", "k1")
	if !crawler.IsPermanent(err) || crawler.PermanentClass(err) != crawler.FaultSignature {
		t.Fatalf("err = %v, want a permanent FaultSignature", err)
	}
}

func TestNewRegistryKeySource_NoNodeIDIsPermanent(t *testing.T) {
	keys := newRegistryKeySource(&fakeSubscriptionRegistry{})
	_, err := keys(context.Background(), "", "k1")
	if !crawler.IsPermanent(err) || crawler.PermanentClass(err) != crawler.FaultSignature {
		t.Fatalf("err = %v, want a permanent FaultSignature", err)
	}
}

func TestNewRegistryKeySource_NoSuchKeyIsPermanent(t *testing.T) {
	reg := &fakeSubscriptionRegistry{subs: nil}
	keys := newRegistryKeySource(reg)
	_, err := keys(context.Background(), "node", "k1")
	if !crawler.IsPermanent(err) || crawler.PermanentClass(err) != crawler.FaultSignature {
		t.Fatalf("err = %v, want a permanent FaultSignature", err)
	}
}

func TestNewRegistryKeySource_UnusableStatusIsPermanent(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := &fakeSubscriptionRegistry{subs: []model.Subscription{registrySub("UNSUBSCRIBED", pub)}}
	keys := newRegistryKeySource(reg)
	_, err = keys(context.Background(), "node", "k1")
	if !crawler.IsPermanent(err) || crawler.PermanentClass(err) != crawler.FaultSignature {
		t.Fatalf("err = %v, want a permanent FaultSignature", err)
	}
}

func TestNewRegistryKeySource_MissingSigningKeyIsPermanent(t *testing.T) {
	reg := &fakeSubscriptionRegistry{subs: []model.Subscription{{KeyID: "k1", Status: "SUBSCRIBED"}}}
	keys := newRegistryKeySource(reg)
	_, err := keys(context.Background(), "node", "k1")
	if !crawler.IsPermanent(err) || crawler.PermanentClass(err) != crawler.FaultSignature {
		t.Fatalf("err = %v, want a permanent FaultSignature", err)
	}
}

func TestNewRegistryKeySource_BadBase64IsPermanent(t *testing.T) {
	reg := &fakeSubscriptionRegistry{subs: []model.Subscription{{KeyID: "k1", Status: "SUBSCRIBED", SigningPublicKey: "not-base64!!"}}}
	keys := newRegistryKeySource(reg)
	_, err := keys(context.Background(), "node", "k1")
	if !crawler.IsPermanent(err) || crawler.PermanentClass(err) != crawler.FaultSignature {
		t.Fatalf("err = %v, want a permanent FaultSignature", err)
	}
}

func TestNewRegistryKeySource_WrongKeySizeIsPermanent(t *testing.T) {
	reg := &fakeSubscriptionRegistry{subs: []model.Subscription{{
		KeyID: "k1", Status: "SUBSCRIBED", SigningPublicKey: base64.StdEncoding.EncodeToString([]byte("too-short")),
	}}}
	keys := newRegistryKeySource(reg)
	_, err := keys(context.Background(), "node", "k1")
	if !crawler.IsPermanent(err) || crawler.PermanentClass(err) != crawler.FaultSignature {
		t.Fatalf("err = %v, want a permanent FaultSignature", err)
	}
}

func TestNewRegistryKeySource_OutageIsTransient(t *testing.T) {
	reg := &fakeSubscriptionRegistry{err: errors.New("registry unreachable")}
	keys := newRegistryKeySource(reg)
	_, err := keys(context.Background(), "node", "k1")
	if err == nil {
		t.Fatal("expected an error")
	}
	if crawler.IsPermanent(err) {
		t.Fatalf("a registry outage must stay transient (retryable), got permanent: %v", err)
	}
}

func TestNewRegistryKeySource_NoCaching(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := &fakeSubscriptionRegistry{subs: []model.Subscription{registrySub("SUBSCRIBED", pub)}}
	keys := newRegistryKeySource(reg)

	for i := 0; i < 3; i++ {
		if _, err := keys(context.Background(), "node", "k1"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	// Deliberately NOT cached here: the registry plugin (definition.RegistryLookup)
	// owns caching, so every call must reach it.
	if reg.n != 3 {
		t.Fatalf("registry called %d times, want 3 (no caching at this layer)", reg.n)
	}
}
