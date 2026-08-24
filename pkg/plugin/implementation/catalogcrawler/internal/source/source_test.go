package source

import (
	"context"
	"errors"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

func TestConfigSource_DedupsByURL(t *testing.T) {
	s := NewConfigSource([]string{"https://a/index", "https://b/index", "https://a/index"})
	refs, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("refs = %+v, want 2 deduped entries", refs)
	}
}

type fakeRegistryClient struct {
	byNetwork map[string][]Provider
	err       error
}

func (f fakeRegistryClient) Providers(_ context.Context, networkID string) ([]Provider, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byNetwork[networkID], nil
}

func TestRegistrySource_DedupsAcrossNetworks(t *testing.T) {
	client := fakeRegistryClient{byNetwork: map[string][]Provider{
		"net-a": {{ParticipantID: "p1", IndexURL: "https://x/index"}},
		"net-b": {{ParticipantID: "p2", IndexURL: "https://x/index"}, {ParticipantID: "p3", IndexURL: "https://y/index"}},
	}}
	s := NewRegistrySource(client, []string{"net-a", "net-b"}, nil)
	refs, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("refs = %+v, want 2 (x/index deduped across networks)", refs)
	}
}

func TestRegistrySource_SkipsEmptyURL(t *testing.T) {
	client := fakeRegistryClient{byNetwork: map[string][]Provider{
		"net-a": {{ParticipantID: "p1", IndexURL: ""}},
	}}
	s := NewRegistrySource(client, []string{"net-a"}, nil)
	refs, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("refs = %+v, want none (empty URL skipped)", refs)
	}
}

func TestRegistrySource_LookupErrorPropagates(t *testing.T) {
	wantErr := errors.New("registry down")
	client := fakeRegistryClient{err: wantErr}
	s := NewRegistrySource(client, []string{"net-a"}, nil)
	_, err := s.Discover(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wrapping %v", err, wantErr)
	}
}

type fakeMetadataLookup struct {
	byNetwork map[string][]model.SubscriberRecord
	err       error
}

func (f fakeMetadataLookup) LookupRegistry(_ context.Context, _, _ string) (*model.RegistryMetadata, error) {
	panic("not used by metadataLookupClient")
}

func (f fakeMetadataLookup) LookupNode(_ context.Context, _ string) (*model.SubscriberRecord, error) {
	panic("not used by metadataLookupClient")
}

func (f fakeMetadataLookup) QueryByNetwork(_ context.Context, networkID string) ([]model.SubscriberRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byNetwork[networkID], nil
}

func TestMetadataLookupClient_MapsRecordsToProviders(t *testing.T) {
	lookup := fakeMetadataLookup{byNetwork: map[string][]model.SubscriberRecord{
		"beckn.one/testnet": {
			{
				Subscription: model.Subscription{Subscriber: model.Subscriber{SubscriberID: "p1"}},
				MetaArrays:   map[string][]string{"catalog_index_urls": {"https://a/index", "https://b/index"}},
			},
			{
				Subscription: model.Subscription{Subscriber: model.Subscriber{SubscriberID: "p2"}},
				MetaArrays:   map[string][]string{"catalog_index_urls": {""}},
			},
			{
				Subscription: model.Subscription{Subscriber: model.Subscriber{SubscriberID: "p3"}},
			},
		},
	}}
	c := NewMetadataLookupClient(lookup)
	provs, err := c.Providers(context.Background(), "beckn.one/testnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(provs) != 2 || provs[0].IndexURL != "https://a/index" || provs[1].IndexURL != "https://b/index" {
		t.Fatalf("providers = %+v, want two entries from p1's catalog_index_urls (empty URL and p2/p3 with none excluded)", provs)
	}
	if provs[0].ParticipantID != "p1" || provs[1].ParticipantID != "p1" {
		t.Fatalf("providers = %+v, want both attributed to p1", provs)
	}
}

func TestMetadataLookupClient_LookupErrorPropagates(t *testing.T) {
	wantErr := errors.New("registry down")
	lookup := fakeMetadataLookup{err: wantErr}
	c := NewMetadataLookupClient(lookup)
	if _, err := c.Providers(context.Background(), "beckn.one/testnet"); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wrapping %v", err, wantErr)
	}
}
