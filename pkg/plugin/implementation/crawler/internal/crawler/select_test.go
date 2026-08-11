package crawler

// select_test.go — the composition root's source selection must agree with
// config.LoadSettings' source-required check. LoadSettings accepts a bare
// CRAWLER_REGISTRY_URL (no networks) ONLY when a static CRAWLER_INDEX_URLS list
// is also present, because a registry URL with nothing to look up in it is not a
// usable source on its own. selectSource must honor that: with a registry URL
// but no networks it must fall back to the static list, not build an empty
// registry source that discovers nothing and silently drops the operator's URLs.

import (
	"context"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/config"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/source"
)

func TestSelectSource_RegistryURLWithoutNetworks_FallsBackToConfig(t *testing.T) {
	s := config.Settings{
		RegistryURL: "https://registry.example/dedi",  // set, but...
		NetworkIDs:  nil,                              // ...nothing to look up in it
		IndexURLs:   []string{"https://p/index.json"}, // the operator's actual source
	}
	src, mode, count := selectSource(s, nil)
	if mode != source.KindConfig || count != 1 {
		t.Fatalf("mode=%q count=%d, want config/1 (registry URL with no networks is not a source)", mode, count)
	}
	refs, err := src.IndexRefs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].IndexURL != "https://p/index.json" || refs[0].Source != source.KindConfig {
		t.Fatalf("refs = %+v, want the one configured index URL, not an empty registry pass", refs)
	}
}

func TestSelectSource_RegistryURLWithNetworks_SelectsRegistry(t *testing.T) {
	s := config.Settings{
		RegistryURL: "https://registry.example/dedi",
		NetworkIDs:  []string{"beckn.one/testnet"},
		IndexURLs:   []string{"https://p/index.json"}, // present, but a usable registry source wins
	}
	_, mode, count := selectSource(s, nil)
	if mode != source.KindRegistry || count != 1 {
		t.Fatalf("mode=%q count=%d, want registry/1", mode, count)
	}
}

func TestSelectSource_NoRegistry_UsesConfig(t *testing.T) {
	s := config.Settings{
		IndexURLs: []string{"https://a/i.json", "https://b/i.json"},
	}
	_, mode, count := selectSource(s, nil)
	if mode != source.KindConfig || count != 2 {
		t.Fatalf("mode=%q count=%d, want config/2", mode, count)
	}
}
