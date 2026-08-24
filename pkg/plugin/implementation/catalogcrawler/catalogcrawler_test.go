package catalogcrawler

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
)

type fakeRegistry struct{}

func (fakeRegistry) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	return nil, nil
}

func (fakeRegistry) LookupRegistry(context.Context, string, string) (*model.RegistryMetadata, error) {
	return nil, nil
}

func (fakeRegistry) LookupNode(context.Context, string) (*model.SubscriberRecord, error) {
	return nil, nil
}

func (fakeRegistry) QueryByNetwork(context.Context, string) ([]model.SubscriberRecord, error) {
	return nil, nil
}

func TestProvider_New_RequiresRegistry(t *testing.T) {
	_, _, err := Provider{}.New(context.Background(), nil, fakeRegistry{}, map[string]string{cfgDBDSN: "x", cfgDiscoveryURL: "https://x"})
	if err == nil {
		t.Fatal("expected an error with no registry configured")
	}
}

func TestProvider_New_RequiresDBDSN(t *testing.T) {
	_, _, err := Provider{}.New(context.Background(), fakeRegistry{}, fakeRegistry{}, map[string]string{cfgDiscoveryURL: "https://x"})
	if err == nil {
		t.Fatal("expected an error with no dbDsn configured")
	}
}

func TestProvider_New_RequiresDiscoveryURL(t *testing.T) {
	_, _, err := Provider{}.New(context.Background(), fakeRegistry{}, fakeRegistry{}, map[string]string{cfgDBDSN: "postgres://x"})
	if err == nil {
		t.Fatal("expected an error with no discoveryPushUrl configured")
	}
}

func TestProvider_New_BadDSNFailsFast(t *testing.T) {
	// store.Open doesn't dial (sql.Open never connects), so a malformed DSN
	// only surfaces on Migrate -- which is exactly what New calls before
	// returning, so a bad DSN should fail construction, not surface later at
	// Start.
	_, _, err := Provider{}.New(context.Background(), fakeRegistry{}, fakeRegistry{}, map[string]string{
		cfgDBDSN: "postgres://user:pass@nonexistent-host-xyz.invalid:5432/db?connect_timeout=1", cfgDiscoveryURL: "https://x",
	})
	if err == nil {
		t.Fatal("expected migration against an unreachable host to fail construction")
	}
}

func TestProvider_New_RequiresMetadataLookupWhenNetworksConfigured(t *testing.T) {
	_, _, err := Provider{}.New(context.Background(), fakeRegistry{}, nil, map[string]string{
		cfgDBDSN: "postgres://x", cfgDiscoveryURL: "https://x", cfgNetworks: "beckn.one/testnet",
	})
	if err == nil {
		t.Fatal("expected an error when networks is configured without a RegistryMetadataLookup")
	}
}

type fakeMetadataLookup struct {
	byNetwork map[string][]model.SubscriberRecord
	err       error
}

func (fakeMetadataLookup) LookupRegistry(context.Context, string, string) (*model.RegistryMetadata, error) {
	panic("not used by registryDiscoverer")
}

func (fakeMetadataLookup) LookupNode(context.Context, string) (*model.SubscriberRecord, error) {
	panic("not used by registryDiscoverer")
}

func (f fakeMetadataLookup) QueryByNetwork(_ context.Context, networkID string) ([]model.SubscriberRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byNetwork[networkID], nil
}

func TestRegistryDiscoverer_MapsRecordsAndDedupsAcrossNetworks(t *testing.T) {
	lookup := fakeMetadataLookup{byNetwork: map[string][]model.SubscriberRecord{
		"net-a": {
			{
				Subscription: model.Subscription{Subscriber: model.Subscriber{SubscriberID: "p1"}},
				MetaArrays:   map[string][]string{"catalog_index_urls": {"https://x/index", "https://y/index"}},
			},
		},
		"net-b": {
			{
				Subscription: model.Subscription{Subscriber: model.Subscriber{SubscriberID: "p2"}},
				MetaArrays:   map[string][]string{"catalog_index_urls": {"https://x/index", ""}},
			},
			{
				Subscription: model.Subscription{Subscriber: model.Subscriber{SubscriberID: "p3"}},
			},
		},
	}}
	d := &registryDiscoverer{lookup: lookup, networkIDs: []string{"net-a", "net-b"}, log: slog.Default()}
	refs, err := d.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("refs = %+v, want 2 (x/index deduped across networks, empty URL and p3's absent list excluded)", refs)
	}
	if refs[0].IndexURL != "https://x/index" || refs[0].ParticipantID != "p1" {
		t.Errorf("refs[0] = %+v, want p1's x/index (first seen wins the dedup)", refs[0])
	}
	if refs[1].IndexURL != "https://y/index" || refs[1].ParticipantID != "p1" {
		t.Errorf("refs[1] = %+v, want p1's y/index", refs[1])
	}
}

func TestRegistryDiscoverer_LookupErrorPropagates(t *testing.T) {
	wantErr := errors.New("registry down")
	d := &registryDiscoverer{lookup: fakeMetadataLookup{err: wantErr}, networkIDs: []string{"net-a"}, log: slog.Default()}
	if _, err := d.Discover(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wrapping %v", err, wantErr)
	}
}

type stubSource struct {
	refs []crawlmanager.IndexRef
	err  error
}

func (s stubSource) Discover(context.Context) ([]crawlmanager.IndexRef, error) { return s.refs, s.err }

func TestMultiSource_UnionsAndDedupsAcrossSources(t *testing.T) {
	a := stubSource{refs: []crawlmanager.IndexRef{{IndexURL: "https://a"}, {IndexURL: "https://shared"}}}
	b := stubSource{refs: []crawlmanager.IndexRef{{IndexURL: "https://shared"}, {IndexURL: "https://b"}}}
	m := multiSource{a, b}
	refs, err := m.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 3 {
		t.Fatalf("refs = %+v, want 3 deduped entries", refs)
	}
}

func TestMultiSource_PropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	m := multiSource{stubSource{err: wantErr}}
	if _, err := m.Discover(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wrapping %v", err, wantErr)
	}
}

func TestSplitNonEmpty(t *testing.T) {
	got := splitNonEmpty(" a , b,, c ")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("got %v, want [a b c]", got)
	}
}

func TestDurationSecondsOr_FallsBackOnInvalid(t *testing.T) {
	if got := durationSecondsOr("not-a-number", DefaultIndexInterval); got != DefaultIndexInterval {
		t.Fatalf("got %v, want default", got)
	}
	if got := durationSecondsOr("60", DefaultIndexInterval); got.Seconds() != 60 {
		t.Fatalf("got %v, want 60s", got)
	}
}
