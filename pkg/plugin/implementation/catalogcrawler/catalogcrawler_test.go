package catalogcrawler

import (
	"context"
	"errors"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
)

type fakeRegistry struct{}

func (fakeRegistry) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	return nil, nil
}

func TestProvider_New_RequiresRegistry(t *testing.T) {
	_, _, err := Provider{}.New(context.Background(), nil, map[string]string{cfgDBDSN: "x", cfgDiscoveryURL: "https://x"})
	if err == nil {
		t.Fatal("expected an error with no registry configured")
	}
}

func TestProvider_New_RequiresDBDSN(t *testing.T) {
	_, _, err := Provider{}.New(context.Background(), fakeRegistry{}, map[string]string{cfgDiscoveryURL: "https://x"})
	if err == nil {
		t.Fatal("expected an error with no dbDsn configured")
	}
}

func TestProvider_New_RequiresDiscoveryURL(t *testing.T) {
	_, _, err := Provider{}.New(context.Background(), fakeRegistry{}, map[string]string{cfgDBDSN: "postgres://x"})
	if err == nil {
		t.Fatal("expected an error with no discoveryPushUrl configured")
	}
}

func TestProvider_New_BadDSNFailsFast(t *testing.T) {
	// store.Open doesn't dial (sql.Open never connects), so a malformed DSN
	// only surfaces on Migrate -- which is exactly what New calls before
	// returning, so a bad DSN should fail construction, not surface later at
	// Start.
	_, _, err := Provider{}.New(context.Background(), fakeRegistry{}, map[string]string{
		cfgDBDSN: "postgres://user:pass@nonexistent-host-xyz.invalid:5432/db?connect_timeout=1", cfgDiscoveryURL: "https://x",
	})
	if err == nil {
		t.Fatal("expected migration against an unreachable host to fail construction")
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
