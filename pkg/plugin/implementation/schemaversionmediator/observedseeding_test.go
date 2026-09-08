package schemaversionmediator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"gopkg.in/yaml.v3"
)

// --- parseObservedRef ---

func TestParseObservedRef_Valid(t *testing.T) {
	ref := SchemaObjectRef{PayloadRef: PayloadRef{
		ContextURL: "https://schema.beckn.io/retail/Order/v2.1/context.jsonld",
		Type:       "Order",
	}}
	typeName, version, baseURL, ok := parseObservedRef(ref)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if typeName != "Order" {
		t.Errorf("typeName = %q, want Order", typeName)
	}
	if version != "v2.1" {
		t.Errorf("version = %q, want v2.1", version)
	}
	if baseURL != "https://schema.beckn.io/retail/Order" {
		t.Errorf("baseURL = %q, want https://schema.beckn.io/retail/Order", baseURL)
	}
}

func TestParseObservedRef_CompactIRINormalized(t *testing.T) {
	ref := SchemaObjectRef{PayloadRef: PayloadRef{
		ContextURL: "https://schema.beckn.io/retail/RetailConsideration/v2.1/context.jsonld",
		Type:       "rcpa:RetailConsideration",
	}}
	typeName, _, _, ok := parseObservedRef(ref)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if typeName != "RetailConsideration" {
		t.Errorf("typeName = %q, want RetailConsideration", typeName)
	}
}

func TestParseObservedRef_MalformedContextURL_Skipped(t *testing.T) {
	cases := []string{
		"not-a-url",
		"/relative/path/v1.0/context.jsonld",
		"",
	}
	for _, c := range cases {
		ref := SchemaObjectRef{PayloadRef: PayloadRef{ContextURL: c, Type: "Order"}}
		if _, _, _, ok := parseObservedRef(ref); ok {
			t.Errorf("expected ok=false for context URL %q", c)
		}
	}
}

func TestParseObservedRef_NoVersionSegment_Skipped(t *testing.T) {
	ref := SchemaObjectRef{PayloadRef: PayloadRef{
		ContextURL: "https://schema.beckn.io/retail/Order/context.jsonld",
		Type:       "Order",
	}}
	if _, _, _, ok := parseObservedRef(ref); ok {
		t.Errorf("expected ok=false when no version segment present")
	}
}

func TestParseObservedRef_EmptyType_Skipped(t *testing.T) {
	ref := SchemaObjectRef{PayloadRef: PayloadRef{
		ContextURL: "https://schema.beckn.io/retail/Order/v2.1/context.jsonld",
		Type:       "",
	}}
	if _, _, _, ok := parseObservedRef(ref); ok {
		t.Errorf("expected ok=false for empty type")
	}
}

// --- newObservedSeeder ---

func TestNewObservedSeeder_EmptyPathDisabled(t *testing.T) {
	if s := newObservedSeeder(""); s != nil {
		t.Errorf("expected nil seeder for empty path, got %+v", s)
	}
}

// --- observedSeeder.observe ---

func writeManifestFile(t *testing.T, dir string, nm *model.NodeManifest) string {
	t.Helper()
	path := filepath.Join(dir, "node-manifest.yaml")
	out, err := yaml.Marshal(nm)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func readManifestFile(t *testing.T, path string) *model.NodeManifest {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	nm, err := model.ParseNodeManifest(content)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return nm
}

func baseTestManifest() *model.NodeManifest {
	return &model.NodeManifest{
		ManifestVersion: "1.0",
		ManifestType:    "node-manifest",
		SubscriberID:    "test/test/test",
		Schema: model.NodeManifestSchema{
			SchemaObjects: []model.SchemaObject{
				{
					Type:              "Order",
					BaseURL:           "https://schema.beckn.io/retail/Order",
					SupportedVersions: []string{"v2.0"},
				},
			},
		},
		Governance: model.NodeManifestGovernance{EffectiveFrom: "2020-01-01T00:00:00Z"},
	}
}

func TestObservedSeeder_AppendsNewType(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, baseTestManifest())
	s := newObservedSeeder(path)

	refs := []SchemaObjectRef{{PayloadRef: PayloadRef{
		ContextURL: "https://schema.beckn.io/retail/Offer/v1.0/context.jsonld",
		Type:       "Offer",
	}}}
	if err := s.observe(refs); err != nil {
		t.Fatalf("observe: %v", err)
	}

	nm := readManifestFile(t, path)
	if len(nm.Schema.SchemaObjects) != 2 {
		t.Fatalf("expected 2 schema objects, got %d", len(nm.Schema.SchemaObjects))
	}
	found := false
	for _, obj := range nm.Schema.SchemaObjects {
		if obj.Type == "Offer" {
			found = true
			if obj.BaseURL != "https://schema.beckn.io/retail/Offer" {
				t.Errorf("unexpected baseUrl: %s", obj.BaseURL)
			}
			if len(obj.SupportedVersions) != 1 || obj.SupportedVersions[0] != "v1.0" {
				t.Errorf("unexpected supportedVersions: %v", obj.SupportedVersions)
			}
		}
	}
	if !found {
		t.Errorf("expected new Offer schema object to be appended")
	}
}

// A manifest with no governance.effectiveUntil must stay that way after a rewrite.
func TestObservedSeeder_RewriteOmitsEmptyEffectiveUntil(t *testing.T) {
	dir := t.TempDir()
	nm := baseTestManifest()
	nm.Governance.EffectiveUntil = ""
	path := writeManifestFile(t, dir, nm)
	s := newObservedSeeder(path)

	refs := []SchemaObjectRef{{PayloadRef: PayloadRef{
		ContextURL: "https://schema.beckn.io/retail/Offer/v1.0/context.jsonld",
		Type:       "Offer",
	}}}
	if err := s.observe(refs); err != nil {
		t.Fatalf("observe: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Contains(string(raw), "effectiveUntil") {
		t.Errorf("rewritten manifest must omit effectiveUntil when indefinite, got:\n%s", raw)
	}
}

func TestObservedSeeder_AppendsNewVersionToExistingType(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, baseTestManifest())
	s := newObservedSeeder(path)

	refs := []SchemaObjectRef{{PayloadRef: PayloadRef{
		ContextURL: "https://schema.beckn.io/retail/Order/v2.1/context.jsonld",
		Type:       "Order",
	}}}
	if err := s.observe(refs); err != nil {
		t.Fatalf("observe: %v", err)
	}

	nm := readManifestFile(t, path)
	if len(nm.Schema.SchemaObjects) != 1 {
		t.Fatalf("expected 1 schema object, got %d", len(nm.Schema.SchemaObjects))
	}
	versions := nm.Schema.SchemaObjects[0].SupportedVersions
	if len(versions) != 2 || versions[0] != "v2.0" || versions[1] != "v2.1" {
		t.Errorf("unexpected supportedVersions: %v", versions)
	}
}

func TestObservedSeeder_NoChange_NoWrite(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, baseTestManifest())
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	s := newObservedSeeder(path)

	refs := []SchemaObjectRef{{PayloadRef: PayloadRef{
		ContextURL: "https://schema.beckn.io/retail/Order/v2.0/context.jsonld",
		Type:       "Order",
	}}}
	if err := s.observe(refs); err != nil {
		t.Fatalf("observe: %v", err)
	}

	newInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after observe: %v", err)
	}
	if info.ModTime() != newInfo.ModTime() {
		t.Errorf("expected file unchanged when no new schema object is observed")
	}
}

func TestObservedSeeder_MalformedRefsSkipped(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, baseTestManifest())
	s := newObservedSeeder(path)

	refs := []SchemaObjectRef{
		{PayloadRef: PayloadRef{ContextURL: "not-a-url", Type: "Bogus"}},
		{PayloadRef: PayloadRef{ContextURL: "https://schema.beckn.io/retail/Order/v2.0/context.jsonld", Type: ""}},
	}
	if err := s.observe(refs); err != nil {
		t.Fatalf("observe: %v", err)
	}

	nm := readManifestFile(t, path)
	if len(nm.Schema.SchemaObjects) != 1 {
		t.Fatalf("expected malformed refs to be skipped, got %d schema objects", len(nm.Schema.SchemaObjects))
	}
}

func TestObservedSeeder_MissingFile_ReturnsError(t *testing.T) {
	s := newObservedSeeder(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	refs := []SchemaObjectRef{{PayloadRef: PayloadRef{
		ContextURL: "https://schema.beckn.io/retail/Order/v2.0/context.jsonld",
		Type:       "Order",
	}}}
	if err := s.observe(refs); err == nil {
		t.Fatalf("expected error for missing manifest file")
	}
}

func TestObservedSeeder_NilReceiver_NoOp(t *testing.T) {
	var s *observedSeeder
	if err := s.observe([]SchemaObjectRef{{PayloadRef: PayloadRef{ContextURL: "https://x/y/v1.0/context.jsonld", Type: "T"}}}); err != nil {
		t.Fatalf("expected nil-receiver observe to be a no-op, got %v", err)
	}
}

func TestObservedSeeder_EmptyRefs_NoOp(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, baseTestManifest())
	s := newObservedSeeder(path)
	if err := s.observe(nil); err != nil {
		t.Fatalf("expected empty refs to be a no-op, got %v", err)
	}
}

func TestObservedSeeder_Concurrent_NoRaceOrCorruption(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, baseTestManifest())
	s := newObservedSeeder(path)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			refs := []SchemaObjectRef{{PayloadRef: PayloadRef{
				ContextURL: "https://schema.beckn.io/retail/Concurrent" + string(rune('A'+i%5)) + "/v1.0/context.jsonld",
				Type:       "Concurrent" + string(rune('A'+i%5)),
			}}}
			if err := s.observe(refs); err != nil {
				t.Errorf("observe: %v", err)
			}
		}()
	}
	wg.Wait()

	nm := readManifestFile(t, path)
	// 1 original (Order) + up to 5 distinct Concurrent types.
	if len(nm.Schema.SchemaObjects) < 2 || len(nm.Schema.SchemaObjects) > 6 {
		t.Errorf("unexpected schema object count after concurrent writes: %d", len(nm.Schema.SchemaObjects))
	}
}

// --- mediator.observeSeed / Mediate integration ---

func TestMediate_ObservedSeeding_NilSeeder_NoOp(t *testing.T) {
	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{}, localManifestWith(model.SchemaObject{
		Type: "Order", BaseURL: "https://schema.beckn.io/retail/Order", SupportedVersions: []string{"v2.0"},
	}))
	// m.seeder is nil by default from newTestMediatorFull.
	body := []byte(`{"context":{"network_id":"net1"},"message":{"@context":"https://schema.beckn.io/retail/Order/v2.0/context.jsonld","@type":"Order"}}`)
	ctx := stepCtxWithRemoteID(body, "bap.example.com")
	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMediate_ObservedSeeding_CompatiblePath_SeedsNewType(t *testing.T) {
	dir := t.TempDir()
	lm := localManifestWith(model.SchemaObject{
		Type: "Order", BaseURL: "https://schema.beckn.io/retail/Order", SupportedVersions: []string{"v2.0"},
	})
	path := writeManifestFile(t, dir, lm)

	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{}, lm)
	m.seeder = newObservedSeeder(path)

	// Order is compatible (no translation); Offer is unknown and should still get seeded.
	body := []byte(`{"context":{"network_id":"net1"},"message":{` +
		`"@context":"https://schema.beckn.io/retail/Order/v2.0/context.jsonld","@type":"Order",` +
		`"offer":{"@context":"https://schema.beckn.io/retail/Offer/v1.0/context.jsonld","@type":"Offer"}` +
		`}}`)
	ctx := stepCtxWithRemoteID(body, "bap.example.com")
	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	nm := readManifestFile(t, path)
	found := false
	for _, obj := range nm.Schema.SchemaObjects {
		if obj.Type == "Offer" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Offer to be seeded into manifest file after successful Mediate")
	}
}

func TestMediate_ObservedSeeding_TranslationPath_SeedsFromOriginalPayload(t *testing.T) {
	srv := artifactServer(t, map[string]string{
		"/retail/v2.0/Order_from_v1.0.jsonata": `$`,
	})
	defer srv.Close()

	dir := t.TempDir()
	lm := localManifestWith(model.SchemaObject{
		Type: "Order", BaseURL: srv.URL + "/retail", SupportedVersions: []string{"v2.0"},
	})
	path := writeManifestFile(t, dir, lm)

	m := newTestMediatorFull(t, &mockManifestLoader{}, map[string]string{}, lm)
	m.httpClient = srv.Client()
	m.seeder = newObservedSeeder(path)

	body := []byte(`{"context":{"network_id":"net1"},"message":{"@context":"` + srv.URL + `/retail/v1.0/Order.jsonld","@type":"Order"}}`)
	ctx := stepCtxWithRemoteID(body, "bap.example.com")
	if err := m.Mediate(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	nm := readManifestFile(t, path)
	versions := nm.Schema.SchemaObjects[0].SupportedVersions
	if !containsVersion(versions, "v1.0") {
		t.Errorf("expected v1.0 to be seeded from the original (pre-translation) payload, got %v", versions)
	}
}

func TestNew_ManifestPathConfiguresSeeder(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, baseTestManifest())

	loader := &mockManifestLoader{bySubscriberID: func(ctx context.Context, subscriberID string) (*model.ManifestDocument, error) {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return &model.ManifestDocument{Content: content}, nil
	}}

	m, closeFn, err := New(context.Background(), loader, map[string]string{"nodeId": "test/test/test", "manifestPath": path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = closeFn() }()

	impl, ok := m.(*mediator)
	if !ok {
		t.Fatalf("expected *mediator, got %T", m)
	}
	if impl.seeder == nil {
		t.Errorf("expected seeder to be configured when manifestPath is set")
	}
}

func TestNew_NoManifestPath_SeederDisabled(t *testing.T) {
	loader := &mockManifestLoader{bySubscriberID: func(ctx context.Context, subscriberID string) (*model.ManifestDocument, error) {
		return nil, nil
	}}
	m, closeFn, err := New(context.Background(), loader, map[string]string{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = closeFn() }()

	impl, ok := m.(*mediator)
	if !ok {
		t.Fatalf("expected *mediator, got %T", m)
	}
	if impl.seeder != nil {
		t.Errorf("expected seeder to be nil when manifestPath is not set")
	}
}
