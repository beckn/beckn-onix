package source

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestDediQueryClient_ParsesNativeArrayMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"records":[
			{"state":"live","details":{"subscriber_id":"p1"},"meta":{"catalog_index_urls":[{"url":"https://a/index"},{"url":"https://b/index"}]}},
			{"state":"inactive","details":{"subscriber_id":"p2"},"meta":{"catalog_index_urls":[{"url":"https://c/index"}]}},
			{"state":"live","details":null,"meta":null}
		]}}`))
	}))
	defer srv.Close()

	c := NewDediQueryClient(srv.URL, 5*time.Second)
	provs, err := c.Providers(context.Background(), "beckn.one/testnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(provs) != 2 || provs[0].IndexURL != "https://a/index" || provs[1].IndexURL != "https://b/index" {
		t.Fatalf("providers = %+v, want two live-node URLs (inactive/null-meta records excluded)", provs)
	}
}

func TestDediQueryClient_ParsesDoubleEncodedMeta(t *testing.T) {
	// catalog_index_urls as a JSON string containing the array, not a native
	// array -- some records double-encode it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"records":[
			{"state":"live","details":{"subscriber_id":"p1"},"meta":{"catalog_index_urls":"[{\"url\":\"https://a/index\"}]"}}
		]}}`))
	}))
	defer srv.Close()

	c := NewDediQueryClient(srv.URL, 5*time.Second)
	provs, err := c.Providers(context.Background(), "beckn.one/testnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(provs) != 1 || provs[0].IndexURL != "https://a/index" {
		t.Fatalf("providers = %+v, want one entry from the double-encoded shape", provs)
	}
}

func TestDediQueryClient_NonOKStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewDediQueryClient(srv.URL, 5*time.Second)
	if _, err := c.Providers(context.Background(), "beckn.one/testnet"); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}
