package source

import (
	"context"
	"testing"
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
