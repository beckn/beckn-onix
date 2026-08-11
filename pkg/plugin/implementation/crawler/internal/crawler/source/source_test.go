package source

// source_test.go — covers both Source implementations: the config source maps a
// fixed URL list, and the registry source dedups providers by index URL.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestConfigSource(t *testing.T) {
	s := NewConfigSource([]string{"https://a/i.json", "https://b/i.json"})
	refs, err := s.IndexRefs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2", len(refs))
	}
	if refs[0].IndexURL != "https://a/i.json" || refs[0].Source != KindConfig {
		t.Fatalf("ref[0] = %+v", refs[0])
	}
}

type fakeRegistry struct{ byNet map[string][]Provider }

func (f fakeRegistry) Providers(_ context.Context, net string) ([]Provider, error) {
	return f.byNet[net], nil
}

func TestRegistrySource_DedupsByIndexURL(t *testing.T) {
	reg := fakeRegistry{byNet: map[string][]Provider{
		"net1": {{ParticipantID: "p1", IndexURL: "https://p1/i"}},
		"net2": {{ParticipantID: "p2", IndexURL: "https://p2/i"}, {ParticipantID: "p1", IndexURL: "https://p1/i"}},
	}}
	s := NewRegistrySource(reg, []string{"net1", "net2"}, nil)
	refs, err := s.IndexRefs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// p1 appears in both networks but must be crawled once.
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2 (deduped)", len(refs))
	}
	for _, r := range refs {
		if r.Source != KindRegistry {
			t.Errorf("ref %+v source != registry", r)
		}
	}
}

// errRegistry always fails a lookup — the registry source's only error path.
type errRegistry struct{ err error }

func (e errRegistry) Providers(context.Context, string) ([]Provider, error) { return nil, e.err }

// A failed network lookup must abort the whole pass with a wrapped error naming
// the network, not be silently swallowed into an empty (looks-idle) ref set.
func TestRegistrySource_PropagatesLookupError(t *testing.T) {
	sentinel := errors.New("registry unreachable")
	s := NewRegistrySource(errRegistry{err: sentinel}, []string{"beckn.one/net1"}, nil)
	_, err := s.IndexRefs(context.Background())
	if err == nil {
		t.Fatal("expected IndexRefs to fail when a network lookup fails")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v does not wrap the underlying lookup error", err)
	}
	if !strings.Contains(err.Error(), "beckn.one/net1") {
		t.Fatalf("error %q should name the failing network", err.Error())
	}
}

// The participant id must survive the Provider → IndexRef mapping (it is the
// publisher identity a signing-enabled build verifies catalog files against).
func TestRegistrySource_CarriesParticipantIDIntoRef(t *testing.T) {
	reg := fakeRegistry{byNet: map[string][]Provider{
		"net1": {{ParticipantID: "prov.one.example", IndexURL: "https://p1/i"}},
	}}
	refs, err := NewRegistrySource(reg, []string{"net1"}, nil).IndexRefs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ParticipantID != "prov.one.example" {
		t.Fatalf("refs = %+v, want ParticipantID carried into the IndexRef", refs)
	}
}
