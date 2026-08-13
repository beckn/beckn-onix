package publish

// publish_test.go — covers push-body assembly (BuildPushBody context/message
// shape, visibleTo omission), byte-size batching (BatchCatalog), and outcome
// rollup (Rollup) for the publish package.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func resourceIDs(t *testing.T, catalog []byte) []string {
	t.Helper()
	var doc struct {
		Resources []struct {
			ID string `json:"id"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(catalog, &doc); err != nil {
		t.Fatalf("unmarshal catalog: %v", err)
	}
	ids := make([]string, 0, len(doc.Resources))
	for _, r := range doc.Resources {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestBuildPushBody(t *testing.T) {
	catalog := []byte(`{"id":"p/electronics-2026","descriptor":{"name":"E"},"resources":[{"id":"r1"}]}`)
	meta := PushMeta{
		ParticipantID: "publisher.example.com",
		BppURI:        "https://publisher.example.com/beckn",
		MessageID:     "msg-1",
		TransactionID: "txn-1",
		Timestamp:     "2026-07-28T00:00:00Z",
		UpdateMode:    UpdateModeFull,
		CatalogType:   "REGULAR",
		VisibleTo:     []string{"network-a.example.com"},
	}

	body, err := BuildPushBody(meta, catalog)
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		Context struct {
			Action        string `json:"action"`
			BppID         string `json:"bppId"`
			MessageID     string `json:"messageId"`
			TransactionID string `json:"transactionId"`
			Timestamp     string `json:"timestamp"`
			Version       string `json:"version"`
		} `json:"context"`
		Message struct {
			Catalogs          []json.RawMessage `json:"catalogs"`
			PublishDirectives []struct {
				CatalogID   string   `json:"catalogId"`
				CatalogType string   `json:"catalogType"`
				UpdateMode  string   `json:"updateMode"`
				VisibleTo   []string `json:"visibleTo"`
			} `json:"publishDirectives"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal push body: %v", err)
	}

	if got.Context.Action != "catalog/push" {
		t.Errorf("action = %q, want catalog/push", got.Context.Action)
	}
	if got.Context.BppID != meta.ParticipantID {
		t.Errorf("bppId = %q, want %q", got.Context.BppID, meta.ParticipantID)
	}
	if got.Context.Version != "2.0.0" {
		t.Errorf("version = %q, want 2.0.0", got.Context.Version)
	}
	if got.Context.MessageID != "msg-1" || got.Context.TransactionID != "txn-1" || got.Context.Timestamp != "2026-07-28T00:00:00Z" {
		t.Errorf("context ids/timestamp not carried through: %+v", got.Context)
	}
	if len(got.Message.Catalogs) != 1 {
		t.Fatalf("catalogs len = %d, want 1", len(got.Message.Catalogs))
	}
	var gotCat, wantCat map[string]any
	json.Unmarshal(got.Message.Catalogs[0], &gotCat)
	json.Unmarshal(catalog, &wantCat)
	if !reflect.DeepEqual(gotCat, wantCat) {
		t.Errorf("catalog not embedded verbatim")
	}
	if len(got.Message.PublishDirectives) != 1 {
		t.Fatalf("directives len = %d, want 1", len(got.Message.PublishDirectives))
	}
	d := got.Message.PublishDirectives[0]
	if d.CatalogID != "p/electronics-2026" || d.CatalogType != "REGULAR" || d.UpdateMode != "FULL" || !reflect.DeepEqual(d.VisibleTo, []string{"network-a.example.com"}) {
		t.Errorf("directive = %+v, want {p/electronics-2026 REGULAR FULL [network-a.example.com]}", d)
	}
}

func TestBatchCatalog(t *testing.T) {
	catalog := []byte(`{"id":"p/c","descriptor":{"name":"C"},"resources":[{"id":"r1"},{"id":"r2"},{"id":"r3"}]}`)
	full := int64(len(catalog))

	if one, err := BatchCatalog(catalog, full+100, UpdateModeFull); err != nil {
		t.Fatal(err)
	} else if len(one) != 1 || one[0].UpdateMode != UpdateModeFull {
		t.Fatalf("fit = %+v, want 1 FULL", one)
	}

	budget := full - 1
	b, err := BatchCatalog(catalog, budget, UpdateModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 2 {
		t.Fatalf("want >=2 batches under budget, got %d", len(b))
	}
	if b[0].UpdateMode != UpdateModeFull {
		t.Fatalf("lead batch must be FULL, got %s", b[0].UpdateMode)
	}
	var got []string
	for i, batch := range b {
		if i > 0 && batch.UpdateMode != UpdateModeMerge {
			t.Fatalf("batch %d must be MERGE, got %s", i, batch.UpdateMode)
		}
		if int64(len(batch.Doc)) > budget {
			t.Fatalf("batch %d doc = %d bytes, exceeds budget %d", i, len(batch.Doc), budget)
		}
		var meta struct {
			Descriptor json.RawMessage `json:"descriptor"`
		}
		json.Unmarshal(batch.Doc, &meta)
		if len(meta.Descriptor) == 0 {
			t.Fatalf("batch %d dropped catalog metadata", i)
		}
		got = append(got, resourceIDs(t, batch.Doc)...)
	}
	if !reflect.DeepEqual(got, []string{"r1", "r2", "r3"}) {
		t.Fatalf("reassembled resources = %v, want [r1 r2 r3]", got)
	}

	m, err := BatchCatalog(catalog, budget, UpdateModeMerge)
	if err != nil {
		t.Fatal(err)
	}
	for i, batch := range m {
		if batch.UpdateMode != UpdateModeMerge {
			t.Fatalf("merge batch %d = %s, want MERGE", i, batch.UpdateMode)
		}
	}

	big := []byte(`{"id":"p/c","resources":[{"id":"huge","blob":"` + strings.Repeat("x", 500) + `"}]}`)
	bb, err := BatchCatalog(big, 50, UpdateModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(bb) != 1 {
		t.Fatalf("oversize single resource: want 1 batch, got %d", len(bb))
	}
}

func TestBuildPushBody_PublicOmitsVisibleTo(t *testing.T) {
	catalog := []byte(`{"id":"p/c","resources":[]}`)
	body, err := BuildPushBody(PushMeta{ParticipantID: "p", UpdateMode: UpdateModeFull}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	json.Unmarshal(body, &raw)
	msg := raw["message"].(map[string]any)
	dir := msg["publishDirectives"].([]any)[0].(map[string]any)
	if _, present := dir["visibleTo"]; present {
		t.Errorf("visibleTo should be omitted for public catalog, got %v", dir["visibleTo"])
	}
}

// context.schemaContext mirrors the index entry's schemaTypes, so Discovery's
// schema-type resolution doesn't depend on every resource carrying its own
// resourceAttributes["@type"]. Omitted entirely when the entry declares none.
func TestBuildPushBody_SchemaContext(t *testing.T) {
	catalog := []byte(`{"id":"p/c","resources":[]}`)

	t.Run("carried through when the entry declares schemaTypes", func(t *testing.T) {
		body, err := BuildPushBody(PushMeta{
			ParticipantID: "p", UpdateMode: UpdateModeFull,
			SchemaContext: []string{"https://schema.beckn.org/retail/schema/1.1.0/context.jsonld"},
		}, catalog)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Context struct {
				SchemaContext []string `json:"schemaContext"`
			} `json:"context"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		want := []string{"https://schema.beckn.org/retail/schema/1.1.0/context.jsonld"}
		if !reflect.DeepEqual(got.Context.SchemaContext, want) {
			t.Errorf("schemaContext = %v, want %v", got.Context.SchemaContext, want)
		}
	})

	t.Run("omitted when the entry declares none", func(t *testing.T) {
		body, err := BuildPushBody(PushMeta{ParticipantID: "p", UpdateMode: UpdateModeFull}, catalog)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		json.Unmarshal(body, &raw)
		ctx := raw["context"].(map[string]any)
		if _, present := ctx["schemaContext"]; present {
			t.Errorf("schemaContext should be omitted when empty, got %v", ctx["schemaContext"])
		}
	})
}

func TestRollup(t *testing.T) {
	ok := BatchOutcome{Acked: true, HTTPStatus: 200}
	bad := BatchOutcome{Acked: false, HTTPStatus: 400, Reason: "schema"}

	tests := []struct {
		name       string
		outcomes   []BatchOutcome
		wantStatus string
		wantFailed int
		wantAcked  int
	}{
		{"all acked -> success", []BatchOutcome{ok, ok}, SyncOK, 0, 2},
		{"some acked -> partial", []BatchOutcome{ok, bad}, SyncPartial, 1, 1},
		{"none acked -> failed", []BatchOutcome{bad, bad}, SyncFailed, 2, 0},
		{"empty -> success", nil, SyncOK, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, failed, acked := Rollup(tt.outcomes)
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			if len(failed) != tt.wantFailed {
				t.Errorf("failed count = %d, want %d", len(failed), tt.wantFailed)
			}
			if acked != tt.wantAcked {
				t.Errorf("acked = %d, want %d", acked, tt.wantAcked)
			}
		})
	}
}
