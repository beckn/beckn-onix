package catalogcrawler

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBuildPushBody(t *testing.T) {
	catalog := []byte(`{"id":"p/electronics-2026","descriptor":{"name":"E"},"resources":[{"id":"r1"}]}`)
	meta := PushMeta{
		ParticipantID: "publisher.example.com",
		BppURI:        "https://publisher.example.com/beckn",
		MessageID:     "msg-1",
		TransactionID: "txn-1",
		Timestamp:     "2026-07-28T00:00:00Z",
		UpdateMode:    UpdateModeFull,
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
				CatalogID  string   `json:"catalogId"`
				UpdateMode string   `json:"updateMode"`
				VisibleTo  []string `json:"visibleTo"`
			} `json:"publishDirectives"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal push body: %v", err)
	}

	if got.Context.Action != "catalog/publish" {
		t.Errorf("action = %q, want catalog/publish", got.Context.Action)
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
	if d.CatalogID != "p/electronics-2026" || d.UpdateMode != "FULL" || !reflect.DeepEqual(d.VisibleTo, []string{"network-a.example.com"}) {
		t.Errorf("directive = %+v, want {p/electronics-2026 FULL [network-a.example.com]}", d)
	}
}

func TestBatchCatalog(t *testing.T) {
	catalog := []byte(`{"id":"p/c","descriptor":{"name":"C"},"resources":[{"id":"r1"},{"id":"r2"},{"id":"r3"}]}`)

	// Fits in one batch -> single batch carrying baseMode.
	if one, err := BatchCatalog(catalog, 10, UpdateModeFull); err != nil {
		t.Fatal(err)
	} else if len(one) != 1 || one[0].UpdateMode != UpdateModeFull {
		t.Fatalf("fit = %+v, want 1 FULL", one)
	}

	// FULL base, batchSize 2 -> FULL[r1,r2], MERGE[r3].
	b, err := BatchCatalog(catalog, 2, UpdateModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 2 || b[0].UpdateMode != UpdateModeFull || b[1].UpdateMode != UpdateModeMerge {
		t.Fatalf("batches = %+v, want FULL then MERGE", b)
	}
	if ids := resourceIDs(t, b[0].Doc); !reflect.DeepEqual(ids, []string{"r1", "r2"}) {
		t.Fatalf("batch 0 resources = %v, want [r1 r2]", ids)
	}
	if ids := resourceIDs(t, b[1].Doc); !reflect.DeepEqual(ids, []string{"r3"}) {
		t.Fatalf("batch 1 resources = %v, want [r3]", ids)
	}
	// Catalog metadata must ride on every batch (incl. the MERGE one).
	var meta struct {
		Descriptor json.RawMessage `json:"descriptor"`
	}
	json.Unmarshal(b[1].Doc, &meta)
	if len(meta.Descriptor) == 0 {
		t.Fatal("MERGE batch must keep catalog metadata")
	}

	// MERGE base, batchSize 2 -> all MERGE.
	m, err := BatchCatalog(catalog, 2, UpdateModeMerge)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m[0].UpdateMode != UpdateModeMerge || m[1].UpdateMode != UpdateModeMerge {
		t.Fatalf("merge batches = %+v, want all MERGE", m)
	}
}

func TestBuildPushBody_PublicOmitsVisibleTo(t *testing.T) {
	catalog := []byte(`{"id":"p/c","resources":[]}`)
	body, err := BuildPushBody(PushMeta{ParticipantID: "p", UpdateMode: UpdateModeFull}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	// visibleTo must be absent for a public catalog.
	var raw map[string]any
	json.Unmarshal(body, &raw)
	msg := raw["message"].(map[string]any)
	dir := msg["publishDirectives"].([]any)[0].(map[string]any)
	if _, present := dir["visibleTo"]; present {
		t.Errorf("visibleTo should be omitted for public catalog, got %v", dir["visibleTo"])
	}
}
