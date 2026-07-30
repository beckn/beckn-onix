package source

// source_test.go — covers both Source implementations: the config source maps a
// fixed URL list, and the registry source dedups providers by index URL.

import (
	"context"
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
	s := NewRegistrySource(reg, []string{"net1", "net2"})
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
