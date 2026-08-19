package sink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beckn/catalog-core/pkg/catalog"
)

func TestBuildPushBody_CarriesEntryMetadata(t *testing.T) {
	meta := PushMeta{
		ParticipantID: "p1", BppURI: "https://p1.example", MessageID: "m1", TransactionID: "t1",
		Timestamp: "2026-01-01T00:00:00Z", UpdateMode: UpdateModeFull, CatalogType: "REGULAR",
		VisibleTo: []string{"beckn.one/testnet"}, SchemaContext: []string{"https://schema.example/retail"},
	}
	body, err := BuildPushBody(meta, []byte(`{"id":"p/c","resources":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	ctx := got["context"].(map[string]any)
	if ctx["bppId"] != "p1" || ctx["action"] != "catalog/push" {
		t.Fatalf("context = %+v, want bppId=p1 action=catalog/push", ctx)
	}
	directives := got["message"].(map[string]any)["publishDirectives"].([]any)
	directive := directives[0].(map[string]any)
	if directive["catalogId"] != "p/c" || directive["catalogType"] != "REGULAR" || directive["updateMode"] != UpdateModeFull {
		t.Fatalf("directive = %+v, want catalogId=p/c catalogType=REGULAR updateMode=FULL", directive)
	}
}

func TestBatchCatalog_FitsInOneBatch(t *testing.T) {
	doc := []byte(`{"id":"p/c","resources":[{"id":"r1"}]}`)
	batches, err := BatchCatalog(doc, 0, UpdateModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || batches[0].UpdateMode != UpdateModeFull {
		t.Fatalf("batches = %+v, want one FULL batch", batches)
	}
}

func TestBatchCatalog_SplitsOversizedCatalog(t *testing.T) {
	resources := make([]map[string]string, 20)
	for i := range resources {
		resources[i] = map[string]string{"id": strings.Repeat("r", 50)}
	}
	doc, err := json.Marshal(map[string]any{"id": "p/c", "resources": resources, "offers": []any{map[string]string{"id": "o1"}}})
	if err != nil {
		t.Fatal(err)
	}
	batches, err := BatchCatalog(doc, 300, UpdateModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) < 2 {
		t.Fatalf("batches = %d, want more than one for an oversized catalog", len(batches))
	}
	if batches[0].UpdateMode != UpdateModeFull {
		t.Fatalf("lead batch mode = %q, want FULL", batches[0].UpdateMode)
	}
	for _, b := range batches[1:] {
		if b.UpdateMode != UpdateModeMerge {
			t.Fatalf("spillover batch mode = %q, want MERGE", b.UpdateMode)
		}
		if strings.Contains(string(b.Doc), `"offers"`) {
			t.Fatalf("spillover batch %s carries offers, want lead-only", b.Doc)
		}
	}
	totalResources := 0
	for _, b := range batches {
		r, _ := DocCounts(b.Doc)
		totalResources += r
	}
	if totalResources != len(resources) {
		t.Fatalf("totalResources across batches = %d, want %d (none dropped)", totalResources, len(resources))
	}
}

func TestRollup_AllAckedIsAccepted(t *testing.T) {
	accepted, reason := Rollup([]BatchOutcome{{Acked: true}, {Acked: true}})
	if !accepted || reason != "" {
		t.Fatalf("accepted=%v reason=%q, want true/empty", accepted, reason)
	}
}

func TestRollup_AnyFailureIsRejected(t *testing.T) {
	accepted, reason := Rollup([]BatchOutcome{{Acked: true}, {Acked: false, Reason: "schema invalid"}})
	if accepted || !strings.Contains(reason, "schema invalid") {
		t.Fatalf("accepted=%v reason=%q, want false and the failure reason", accepted, reason)
	}
}

func TestDiscoverySink_Send_PushesAndReportsAccepted(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewDiscoverySink(srv.URL, "p1", "https://p1.example", 0, 5*time.Second)
	entry := catalog.CatalogEntry{CatalogID: "p/c", CatalogType: "REGULAR", NetworkIDs: []string{"beckn.one/testnet"}}
	outcome, err := s.Send(context.Background(), entry, []byte(`{"id":"p/c","resources":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Accepted {
		t.Fatalf("outcome = %+v, want accepted", outcome)
	}
	directive := gotBody["message"].(map[string]any)["publishDirectives"].([]any)[0].(map[string]any)
	if directive["catalogType"] != "REGULAR" {
		t.Fatalf("directive = %+v, want catalogType REGULAR from the entry", directive)
	}
}

func TestDiscoverySink_Send_RejectionIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("schema validation failed"))
	}))
	defer srv.Close()

	s := NewDiscoverySink(srv.URL, "p1", "https://p1.example", 0, 5*time.Second)
	outcome, err := s.Send(context.Background(), catalog.CatalogEntry{CatalogID: "p/c"}, []byte(`{"id":"p/c","resources":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Accepted || !strings.Contains(outcome.Reason, "schema validation failed") {
		t.Fatalf("outcome = %+v, want rejected with the response body as reason", outcome)
	}
}
