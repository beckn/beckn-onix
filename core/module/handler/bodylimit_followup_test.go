package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// nackStatus returns message.ack.status from a response body, or "" when the
// body is not a NACK envelope.
func nackStatus(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Message struct {
			Ack struct {
				Status string `json:"status"`
			} `json:"ack"`
			Error *model.Error `json:"error"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response is not JSON: %v: %s", err, body)
	}
	if resp.Message.Error == nil {
		return ""
	}
	return resp.Message.Ack.Status
}

func TestRejectBodyTooLarge(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bpp/receiver/select", nil)

	RejectBodyTooLarge(rr, req, 16)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rr.Code)
	}
	if got := nackStatus(t, rr.Body.Bytes()); got != "NACK" {
		t.Errorf("message.ack.status = %q, want NACK: %s", got, rr.Body.String())
	}
	if got := rr.Header().Get("Connection"); got != "close" {
		t.Errorf("Connection = %q, want close", got)
	}
}

// Every route answers an oversized body with the same NACK envelope.
func TestServeHTTP_BodyOverLimitIsNack(t *testing.T) {
	h := &stdHandler{SubscriberID: "test-sub", role: model.RoleBPP, moduleName: "test-module"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bpp/receiver/discover", strings.NewReader(strings.Repeat("a", 64)))
	req.Body = http.MaxBytesReader(rr, req.Body, 16)

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge || nackStatus(t, rr.Body.Bytes()) != "NACK" {
		t.Errorf("status = %d, body = %s; want 413 with a NACK envelope", rr.Code, rr.Body.String())
	}
}

// The step context must use the bytes ServeHTTP already read, not a copy.
func TestStepCtxFromBodyDoesNotCopy(t *testing.T) {
	h := &stdHandler{SubscriberID: "test-sub", role: model.RoleBPP, moduleName: "test-module"}
	body := []byte(`{"context":{"action":"select","version":"2.0.0"}}`)
	req := httptest.NewRequest(http.MethodPost, "/bpp/receiver/select", nil)

	sctx := h.stepCtxFromBody(req, http.Header{}, body)

	if &sctx.Body[0] != &body[0] {
		t.Error("stepCtxFromBody copied the request body")
	}
	if sctx.ProtocolVersion != "2.0.0" {
		t.Errorf("ProtocolVersion = %q, want 2.0.0", sctx.ProtocolVersion)
	}
}

func TestReadRequestBody(t *testing.T) {
	t.Run("declared length is read into one exact allocation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", 5000)))
		body, err := readRequestBody(req)
		if err != nil || len(body) != 5000 {
			t.Fatalf("len = %d, err = %v", len(body), err)
		}
		if cap(body) > 5000+512 {
			t.Errorf("cap = %d for a 5000 byte body, want no over-allocation", cap(body))
		}
	})
	t.Run("a declared length is not trusted beyond what arrives", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("abc"))
		req.ContentLength = 1 << 40
		body, err := readRequestBody(req)
		if err != nil || string(body) != "abc" {
			t.Fatalf("body = %q, err = %v", body, err)
		}
		if cap(body) > 1<<20 {
			t.Errorf("cap = %d: allocation followed the declared length", cap(body))
		}
	})
	t.Run("undeclared length", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", struct{ *strings.Reader }{strings.NewReader("abc")})
		body, err := readRequestBody(req)
		if err != nil || string(body) != "abc" {
			t.Fatalf("body = %q, err = %v", body, err)
		}
	})
}

func proxyTo(t *testing.T, upstream http.HandlerFunc, limit int64) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	sctx := &model.StepContext{Context: context.Background(), Route: &model.Route{TargetType: "url", URL: u}, MaxResponseBodyBytes: limit}
	rr := httptest.NewRecorder()
	var responseBody []byte
	proxy(sctx, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)), rr, http.DefaultClient, nil, &responseBody)
	return rr, responseBody
}

func TestProxyLimitsResponseBody(t *testing.T) {
	const limit = 1024
	big := strings.Repeat("a", limit+1)

	t.Run("declared length over the limit", func(t *testing.T) {
		rr, captured := proxyTo(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(big)) }, limit)
		if rr.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", rr.Code)
		}
		if rr.Body.Len() >= limit || len(captured) != 0 {
			t.Errorf("relayed %d bytes, captured %d; want the oversized response dropped", rr.Body.Len(), len(captured))
		}
	})
	t.Run("streamed response over the limit", func(t *testing.T) {
		rr, captured := proxyTo(t, func(w http.ResponseWriter, _ *http.Request) {
			for i := 0; i < 4; i++ {
				_, _ = w.Write([]byte(big[:limit/2]))
				w.(http.Flusher).Flush()
			}
		}, limit)
		if rr.Code != http.StatusBadGateway || len(captured) != 0 {
			t.Errorf("status = %d, captured %d bytes; want 502 and nothing captured", rr.Code, len(captured))
		}
	})
	t.Run("response at the limit is relayed whole", func(t *testing.T) {
		rr, captured := proxyTo(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(big[:limit])) }, limit)
		if rr.Code != http.StatusOK || rr.Body.Len() != limit || len(captured) != limit {
			t.Errorf("status = %d, relayed %d, captured %d; want 200 and %d bytes", rr.Code, rr.Body.Len(), len(captured), limit)
		}
	})
}

func TestMaxResponseBodyBytesDefault(t *testing.T) {
	if got := (&HttpClientConfig{}).maxResponseBodyBytes(); got != DefaultMaxBodyBytes {
		t.Errorf("unset = %d, want the default %d", got, DefaultMaxBodyBytes)
	}
	if got := (&HttpClientConfig{MaxResponseBodyBytes: 2048}).maxResponseBodyBytes(); got != 2048 {
		t.Errorf("set = %d, want 2048", got)
	}
}
