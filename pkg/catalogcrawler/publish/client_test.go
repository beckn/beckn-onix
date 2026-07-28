package publish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_Push(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/bad", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte("schema invalid"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(5 * time.Second)
	ctx := context.Background()

	if out, err := c.Push(ctx, srv.URL+"/ok", []byte(`{}`)); err != nil || !out.Acked || out.HTTPStatus != 200 {
		t.Fatalf("push ok = %+v err=%v, want acked 200", out, err)
	}
	out, err := c.Push(ctx, srv.URL+"/bad", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Acked || out.HTTPStatus != 400 || out.Reason == "" {
		t.Fatalf("push bad = %+v, want not-acked 400 with reason", out)
	}
}
