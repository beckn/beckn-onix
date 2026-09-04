package catalogcrawler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
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
	// errByNetwork lets a test fail QueryByNetwork for specific network IDs
	// only, so partial-failure and all-failed scenarios can both be
	// expressed precisely.
	errByNetwork map[string]error
}

func (fakeMetadataLookup) LookupRegistry(context.Context, string, string) (*model.RegistryMetadata, error) {
	panic("not used by registryDiscoverer")
}

func (fakeMetadataLookup) LookupNode(context.Context, string) (*model.SubscriberRecord, error) {
	panic("not used by registryDiscoverer")
}

func (f fakeMetadataLookup) QueryByNetwork(_ context.Context, networkID string) ([]model.SubscriberRecord, error) {
	if err, ok := f.errByNetwork[networkID]; ok {
		return nil, err
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

// TestRegistryDiscoverer_SkipsFailingNetworkWithoutAbortingOthers guards
// against #921: a single network's lookup failing (e.g. an unregistered
// networkId 404ing against the registry) must not abort discovery for the
// other configured networks.
func TestRegistryDiscoverer_SkipsFailingNetworkWithoutAbortingOthers(t *testing.T) {
	lookup := fakeMetadataLookup{
		byNetwork: map[string][]model.SubscriberRecord{
			"net-a": {
				{
					Subscription: model.Subscription{Subscriber: model.Subscriber{SubscriberID: "p1"}},
					MetaArrays:   map[string][]string{"catalog_index_urls": {"https://x/index"}},
				},
			},
		},
		errByNetwork: map[string]error{"net-b": errors.New("registry down: 404")},
	}
	d := &registryDiscoverer{lookup: lookup, networkIDs: []string{"net-a", "net-b"}, log: slog.Default()}
	refs, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover returned error %v, want nil (per-network failures must not abort the tick)", err)
	}
	if len(refs) != 1 || refs[0].IndexURL != "https://x/index" {
		t.Fatalf("refs = %+v, want the one ref from net-a despite net-b failing", refs)
	}
}

// TestRegistryDiscoverer_AllNetworksFailingReturnsError guards the other
// half of #921: partial failures must be tolerated, but a registry that is
// unreachable for every configured network is a real outage and must still
// surface as an error, not silently discover zero indexes.
func TestRegistryDiscoverer_AllNetworksFailingReturnsError(t *testing.T) {
	lookup := fakeMetadataLookup{
		errByNetwork: map[string]error{
			"net-a": errors.New("registry down: 404"),
			"net-b": errors.New("registry down: timeout"),
		},
	}
	d := &registryDiscoverer{lookup: lookup, networkIDs: []string{"net-a", "net-b"}, log: slog.Default()}
	refs, err := d.Discover(context.Background())
	if err == nil {
		t.Fatal("Discover returned nil error, want an error when every configured network fails")
	}
	if refs != nil {
		t.Fatalf("refs = %+v, want nil when every configured network fails", refs)
	}
}

// TestRegistryDiscoverer_NoNetworksConfiguredIsNotAFailure guards against a
// regression of the all-failed check: zero configured networks is a valid,
// non-error state (e.g. only staticIndexUrls in use) and must not be
// mistaken for "0 out of 0 failed".
func TestRegistryDiscoverer_NoNetworksConfiguredIsNotAFailure(t *testing.T) {
	d := &registryDiscoverer{lookup: fakeMetadataLookup{}, networkIDs: nil, log: slog.Default()}
	refs, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover returned error %v, want nil when no networks are configured", err)
	}
	if refs != nil {
		t.Fatalf("refs = %+v, want nil", refs)
	}
}

// TestRegistryDiscoverer_TracksConsecutiveFailuresPerNetworkAcrossTicks
// checks the streak-tracking a persistently broken network needs to
// eventually escalate from Warn to Error logging (consecutiveFailEscalateThreshold):
// the streak must accumulate across repeated Discover calls (one call per
// poll tick) for a network that keeps failing, and reset the moment that
// network succeeds again, without affecting an unrelated healthy network.
func TestRegistryDiscoverer_TracksConsecutiveFailuresPerNetworkAcrossTicks(t *testing.T) {
	lookup := fakeMetadataLookup{
		byNetwork:    map[string][]model.SubscriberRecord{"net-ok": {}},
		errByNetwork: map[string]error{"net-bad": errors.New("registry down")},
	}
	d := &registryDiscoverer{lookup: lookup, networkIDs: []string{"net-ok", "net-bad"}, log: slog.Default()}

	for tick := 1; tick <= consecutiveFailEscalateThreshold; tick++ {
		if _, err := d.Discover(context.Background()); err != nil {
			t.Fatalf("tick %d: Discover returned error %v, want nil (net-ok keeps the call from being all-failed)", tick, err)
		}
	}
	if got := d.consecutiveFails["net-bad"]; got != consecutiveFailEscalateThreshold {
		t.Fatalf("net-bad consecutive failures = %d, want %d after %d failing ticks", got, consecutiveFailEscalateThreshold, consecutiveFailEscalateThreshold)
	}
	if _, tracked := d.consecutiveFails["net-ok"]; tracked {
		t.Fatalf("net-ok should never accumulate a failure streak, got %d", d.consecutiveFails["net-ok"])
	}

	// net-bad recovers: its streak must reset.
	lookup.errByNetwork = nil
	d.lookup = lookup
	if _, err := d.Discover(context.Background()); err != nil {
		t.Fatalf("Discover returned error %v, want nil once net-bad recovers", err)
	}
	if _, tracked := d.consecutiveFails["net-bad"]; tracked {
		t.Fatalf("net-bad's failure streak should be cleared after a successful lookup, got %d", d.consecutiveFails["net-bad"])
	}
}

// TestRegistryDiscoverer_EscalatesLogLevelAtThreshold complements the
// counter-only check above by asserting the actual observable behavior the
// streak exists to drive: log output stays at Warn for ticks before the
// threshold and switches to Error exactly on the tick that reaches it. A
// bug in the `streak >= consecutiveFailEscalateThreshold` branch itself
// (off-by-one, inverted comparison, wrong log method) would pass a
// counter-only assertion but fail this one.
func TestRegistryDiscoverer_EscalatesLogLevelAtThreshold(t *testing.T) {
	lookup := fakeMetadataLookup{errByNetwork: map[string]error{"net-bad": errors.New("registry down")}}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	d := &registryDiscoverer{lookup: lookup, networkIDs: []string{"net-bad"}, log: log}

	for tick := 1; tick < consecutiveFailEscalateThreshold; tick++ {
		buf.Reset()
		if _, err := d.Discover(context.Background()); err == nil {
			t.Fatalf("tick %d: Discover returned nil error, want an error (net-bad is the only configured network)", tick)
		}
		out := buf.String()
		if !strings.Contains(out, "level=WARN") {
			t.Fatalf("tick %d: log output = %q, want a WARN-level per-network line before the escalation threshold", tick, out)
		}
		if strings.Contains(out, "level=ERROR") && strings.Contains(out, "registry lookup failing repeatedly") {
			t.Fatalf("tick %d: log output = %q, escalated to ERROR before reaching the threshold", tick, out)
		}
	}

	buf.Reset()
	if _, err := d.Discover(context.Background()); err == nil {
		t.Fatal("final tick: Discover returned nil error, want an error")
	}
	out := buf.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "registry lookup failing repeatedly") {
		t.Fatalf("final tick: log output = %q, want the per-network line escalated to ERROR at the threshold", out)
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
	m := multiSource{sources: []crawlmanager.Source{a, b}, log: slog.Default()}
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
	m := multiSource{sources: []crawlmanager.Source{stubSource{err: wantErr}}, log: slog.Default()}
	if _, err := m.Discover(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wrapping %v", err, wantErr)
	}
}

// TestMultiSource_ToleratesOneSourceFailingWhenAnotherSucceeds guards the
// source-level half of #921/#922: one source erroring (e.g. a registry
// outage) must not discard refs a sibling source (e.g. staticIndexUrls)
// already found.
func TestMultiSource_ToleratesOneSourceFailingWhenAnotherSucceeds(t *testing.T) {
	ok := stubSource{refs: []crawlmanager.IndexRef{{IndexURL: "https://static/index"}}}
	broken := stubSource{err: errors.New("registry unreachable")}
	m := multiSource{sources: []crawlmanager.Source{ok, broken}, log: slog.Default()}
	refs, err := m.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover returned error %v, want nil (one failing source must not abort the tick)", err)
	}
	if len(refs) != 1 || refs[0].IndexURL != "https://static/index" {
		t.Fatalf("refs = %+v, want the one ref from the succeeding source", refs)
	}
}

// TestMultiSource_AllSourcesFailingReturnsError mirrors
// TestRegistryDiscoverer_AllNetworksFailingReturnsError one layer up: if
// every source fails, that's a real outage and must still surface as an
// error rather than silently returning zero indexes.
func TestMultiSource_AllSourcesFailingReturnsError(t *testing.T) {
	m := multiSource{
		sources: []crawlmanager.Source{
			stubSource{err: errors.New("registry unreachable")},
			stubSource{err: errors.New("static config fetch failed")},
		},
		log: slog.Default(),
	}
	refs, err := m.Discover(context.Background())
	if err == nil {
		t.Fatal("Discover returned nil error, want an error when every source fails")
	}
	if refs != nil {
		t.Fatalf("refs = %+v, want nil when every source fails", refs)
	}
}

// TestMultiSource_LogsErrorOnPartialSourceFailure guards the round-2
// self-review finding that a total registry outage would otherwise be
// invisible whenever a healthy staticIndexUrls source papers over it: the
// tick still succeeds (partial tolerance), but the partial failure must
// still be logged at Error so it's discoverable without correlating tick
// success against per-source internals.
func TestMultiSource_LogsErrorOnPartialSourceFailure(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	ok := stubSource{refs: []crawlmanager.IndexRef{{IndexURL: "https://static/index"}}}
	broken := stubSource{err: errors.New("registry unreachable")}
	m := multiSource{sources: []crawlmanager.Source{ok, broken}, log: log}
	if _, err := m.Discover(context.Background()); err != nil {
		t.Fatalf("Discover returned error %v, want nil", err)
	}
	out := buf.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "one or more discovery sources failed") {
		t.Fatalf("log output = %q, want an ERROR-level line about the partial source failure", out)
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
